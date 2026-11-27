package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The pr-review skill records each automated review and its findings through
// these commands, and the worker each finding's outcome and the user's
// decision on one left for them. Each writes one record, refusing a value crew
// doesn't know, and none touches the task file: the skill and the worker write
// the review's text there for people. Until crew decides from these records
// alone, it still reads that text (Task.AutomatedReviews), so crew numbers each
// review and finding, and the text takes the numbers crew printed.

// crew automated-review TASK FROM..HEAD --fork SHA --reviewers NAME[,NAME]
// [--failed NAME[,NAME]]: records a review that read FROM..HEAD of the task's
// branch, and prints the heading its text goes under, with its number.
func cmdAutomatedReview(c *Crew, args []string) error {
	const use = "crew automated-review TASK FROM..HEAD --fork SHA --reviewers claude[,codex] [--failed NAME[,NAME]]"
	var fork, ran, failed string
	words, err := parseArgs(args, map[string]*string{"--fork": &fork, "--reviewers": &ran, "--failed": &failed}, nil, use)
	if err != nil {
		return err
	}
	if len(words) != 2 || fork == "" || ran == "" {
		return usage(use)
	}
	t, err := c.resolveTask(words[0])
	if err != nil {
		return err
	}
	from, head, ok := strings.Cut(words[1], "..")
	if !ok {
		return fail("give the commits the review read as FROM..HEAD, not '%s'", words[1])
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	review := AutomatedReview{Reviewers: names(ran), Failed: names(failed)}
	if review.From, review.Fork, review.Head, err = branchRange(p.Repo(), t.Branch(), from, fork, head); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	// Reviews recorded before crew had these records are numbered in the text.
	review.Number = t.NewestReview()
	for _, r := range recordedReviews(records) {
		review.Number = max(review.Number, r.Number)
	}
	review.Number++
	r := newRecord("crew automated-review", records)
	r.AutomatedReview = &review
	if err := t.AddRecords(r); err != nil {
		return fail("%v", err)
	}
	c.commit("automated-review " + t.ID())
	fmt.Printf("### Automated review %d — %s\n", review.Number, r.At.Local().Format(logTime))
	return nil
}

// names reads a list of reviewers, "claude,codex".
func names(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

// branchRange resolves a review's range and fork point in repo to full commit
// IDs, refusing any it can't resolve, and a range that isn't commits of branch:
// fork comes first, then from, then head, which branch holds, with at least
// one commit from from to head.
func branchRange(repo, branch, from, fork, head string) (string, string, string, error) {
	ids := map[string]string{}
	for _, rev := range []string{from, fork, head} {
		id, _ := git(repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if id == "" {
			return "", "", "", fail("crew can't find a commit '%s' in %s", rev, repo)
		}
		ids[rev] = id
	}
	ref := "refs/heads/" + branch
	if !gitOK(repo, "rev-parse", "--verify", "--quiet", ref) {
		return "", "", "", fail("crew can't find the task's branch %s in %s", branch, repo)
	}
	switch {
	case !gitOK(repo, "merge-base", "--is-ancestor", ids[head], ref):
		return "", "", "", fail("%s isn't on %s", head, branch)
	case ids[from] == ids[head]:
		return "", "", "", fail("%s..%s holds no commits", from, head)
	case !gitOK(repo, "merge-base", "--is-ancestor", ids[from], ids[head]):
		return "", "", "", fail("%s doesn't come before %s on %s", from, head, branch)
	case !gitOK(repo, "merge-base", "--is-ancestor", ids[fork], ids[from]):
		return "", "", "", fail("the fork point %s doesn't come before %s on %s", fork, from, branch)
	}
	return ids[from], ids[fork], ids[head], nil
}

// crew finding TASK SEVERITY "<title>" [--split | --flipped] [--pre-existing]:
// records a finding of the task's newest recorded review at the severity the
// review agreed on, and prints its number, review.finding.
func cmdFinding(c *Crew, args []string) error {
	const use = `crew finding TASK SEVERITY "<title>" [--split | --flipped] [--pre-existing]`
	var f Finding
	words, err := parseArgs(args, nil, map[string]*bool{"--split": &f.Split, "--flipped": &f.Flipped, "--pre-existing": &f.PreExisting}, use)
	if err != nil {
		return err
	}
	if len(words) != 3 {
		return usage(use)
	}
	t, err := c.resolveTask(words[0])
	if err != nil {
		return err
	}
	f.Severity, f.Title = Severity(words[1]), oneLine(words[2])
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	i := lastIndexFunc(records, func(r Record) bool { return r.AutomatedReview != nil })
	if i < 0 {
		return fail("%s has no automated review recorded: crew automated-review records one", t.ID())
	}
	f.Review = records[i].ID
	review := records[i].AutomatedReview.Number
	f.Number = 1 + len(slices.DeleteFunc(slices.Clone(records), func(r Record) bool { return r.Finding == nil || r.Finding.Review != f.Review }))
	r := newRecord("crew finding", records)
	r.Finding = &f
	if err := t.AddRecords(r); err != nil {
		return fail("%v", err)
	}
	c.commit("finding " + t.ID())
	fmt.Printf("%d.%d\n", review, f.Number)
	return nil
}

// crew outcome TASK FINDING "<outcome>": records what the worker did about a
// finding, named review.finding, once. The outcome is the one the worker
// writes on the finding's Outcome line.
func cmdOutcome(c *Crew, args []string) error {
	const use = `crew outcome TASK FINDING "<outcome>"`
	if len(args) != 3 {
		return usage(use)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	o, err := parseOutcome(args[2])
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	f, err := findingRecord(t, records, args[1])
	if err != nil {
		return err
	}
	if o.StillOpenFrom >= f.review {
		return fail("%s can only be still open from an earlier review than %d", args[1], f.review)
	}
	if f.outcome != nil {
		return fail("%s already has its outcome, '%s', from %s", args[1], f.outcome.Outcome.Text, stamp(*f.outcome))
	}
	o.Finding = f.ID
	r := newRecord("crew outcome", records)
	r.Outcome = &o
	if err := t.AddRecords(r); err != nil {
		return fail("%v", err)
	}
	c.commit("outcome " + t.ID())
	fmt.Printf("%s: %s\n", args[1], o.Kind)
	return nil
}

// parseOutcome reads an outcome as implementer.md words it: a kind crew
// knows, then the rest ("fixed in abc1234", "left for the user: still open
// from review 3"). It refuses one that doesn't start with a kind, and a finding
// still open from a review it doesn't number.
func parseOutcome(text string) (FindingOutcome, error) {
	o := FindingOutcome{Text: oneLine(text)}
	for _, kind := range outcomes {
		if rest, ok := strings.CutPrefix(o.Text, string(kind)); ok && (rest == "" || strings.ContainsAny(rest[:1], " :;,")) {
			o.Kind = kind
		}
	}
	if o.Kind == "" {
		return o, fail("'%s' doesn't start with an outcome crew knows: start it with %s", o.Text, listed(outcomes))
	}
	if from, ok := strings.CutPrefix(o.Text, string(OutcomeLeftForUser)+": still open from review "); ok {
		n, _, _ := strings.Cut(from, ";")
		if o.StillOpenFrom, _ = strconv.Atoi(n); o.StillOpenFrom < 1 {
			return o, fail("'%s' needs the number of the review it's still open from, and nothing after it but '; …'", o.Text)
		}
	}
	return o, nil
}

// crew user-decided TASK FINDING "<decision>": records the user's decision on
// a finding the worker left for them, once.
func cmdUserDecided(c *Crew, args []string) error {
	const use = `crew user-decided TASK FINDING "<the user's decision>"`
	if len(args) != 3 {
		return usage(use)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	f, err := findingRecord(t, records, args[1])
	if err != nil {
		return err
	}
	switch {
	case f.outcome == nil:
		return fail("%s has no outcome yet: only a finding left for the user takes their decision", args[1])
	case f.outcome.Outcome.Kind != OutcomeLeftForUser:
		return fail("%s wasn't left for the user: its outcome is '%s'", args[1], f.outcome.Outcome.Kind)
	case f.decision != nil:
		return fail("the user already decided %s, on %s", args[1], stamp(*f.decision))
	}
	r := newRecord("crew user-decided", records)
	r.Decision = &FindingDecision{Finding: f.ID, Decision: strings.TrimSpace(args[2])}
	if err := t.AddRecords(r); err != nil {
		return fail("%v", err)
	}
	c.commit("user-decided " + t.ID())
	fmt.Printf("%s: decided by the user\n", args[1])
	return nil
}

// lastIndexFunc is the index of the last element of s that satisfies f, or -1.
func lastIndexFunc[E any](s []E, f func(E) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// A recordedFinding is a finding's record, with the number of its review and
// the records of its outcome and the user's decision, if it has them.
type recordedFinding struct {
	Record
	review            int
	outcome, decision *Record
}

// findingRecord is the finding the task's records hold as name,
// "<review>.<finding>".
func findingRecord(t Task, records []Record, name string) (recordedFinding, error) {
	review, finding, _ := strings.Cut(name, ".")
	var f recordedFinding
	for _, r := range records {
		if r.AutomatedReview != nil && strconv.Itoa(r.AutomatedReview.Number) == review {
			f.review = r.AutomatedReview.Number
			for _, r2 := range records {
				if r2.Finding != nil && r2.Finding.Review == r.ID && strconv.Itoa(r2.Finding.Number) == finding {
					f.Record = r2
				}
			}
		}
	}
	if f.ID == "" {
		return f, fail("%s records no finding %s (crew show %s lists them)", t.ID(), name, t.ID())
	}
	for _, r := range records {
		switch {
		case r.Outcome != nil && r.Outcome.Finding == f.ID && f.outcome == nil:
			f.outcome = &r
		case r.Decision != nil && r.Decision.Finding == f.ID && f.decision == nil:
			f.decision = &r
		}
	}
	return f, nil
}

// RecordedReviews are the automated reviews in the task's records, in the
// order they were recorded, each with its findings, their outcomes and the
// user's decisions.
func (t Task) RecordedReviews() ([]AutomatedReview, error) {
	records, err := t.Records()
	if err != nil {
		return nil, err
	}
	return recordedReviews(records), nil
}

// recordedReviews puts the review records together. A merge may put a record
// before the one it names, so each kind is read in a pass of its own.
func recordedReviews(records []Record) []AutomatedReview {
	var reviews []AutomatedReview
	review := map[string]int{} // a review record's ID → its index in reviews
	for _, r := range records {
		if r.AutomatedReview != nil {
			review[r.ID] = len(reviews)
			reviews = append(reviews, *r.AutomatedReview)
			reviews[len(reviews)-1].At = r.At
		}
	}
	finding := map[string][2]int{} // a finding record's ID → its review's index, and its own there
	for _, r := range records {
		if r.Finding != nil {
			i := review[r.Finding.Review]
			finding[r.ID] = [2]int{i, len(reviews[i].Findings)}
			reviews[i].Findings = append(reviews[i].Findings, *r.Finding)
		}
	}
	at := func(id string) *Finding { i := finding[id]; return &reviews[i[0]].Findings[i[1]] }
	for _, r := range records {
		switch {
		case r.Outcome != nil:
			at(r.Outcome.Finding).Outcome = *r.Outcome
		case r.Decision != nil:
			at(r.Decision.Finding).Decision = r.Decision.Decision
		}
	}
	return reviews
}

const reviewsHeading = "## Automated reviews"

// withReviews is the task file's lines with its recorded automated reviews at
// the top of Automated reviews, before the text written beside them. When crew
// can't read the records, the section says why. A project not yet migrated has
// no review records: the commands that write them refuse there.
func withReviews(t Task, ls []string) []string {
	reviews, err := t.RecordedReviews()
	var shown []string
	switch {
	case errors.Is(err, errNotMigrated):
		return ls
	case err != nil:
		shown = []string{"crew can't read the reviews' records: " + err.Error()}
	case len(reviews) == 0:
		return ls
	default:
		shown = append([]string{"Recorded by crew:"}, reviewLines(reviews)...)
	}
	start := slices.Index(ls, reviewsHeading)
	if start < 0 {
		start = slices.IndexFunc(ls, func(l string) bool { return l == "## Review rounds" || l == "## Log" })
		if start < 0 {
			start = len(ls)
		}
		ls = slices.Insert(ls, start, reviewsHeading, "")
	}
	// After the section's own comment, if it has one.
	at := start + 1
	for _, l := range uncommented(ls[at:]) {
		if strings.TrimSpace(l) != "" {
			break
		}
		at++
	}
	return slices.Concat(ls[:at], shown, []string{""}, ls[at:])
}

// reviewLines are the reviews as Markdown: each review, its range and its
// reviewers, and under it each finding with its severity and outcome, and the
// user's decision.
func reviewLines(reviews []AutomatedReview) []string {
	var out []string
	for _, r := range reviews {
		who := strings.Join(r.Reviewers, " and ")
		if len(r.Failed) > 0 {
			who += " (" + strings.Join(r.Failed, " and ") + " didn't report)"
		}
		out = append(out, fmt.Sprintf("- Review %d, %s: `%s..%s` (fork point `%s`), by %s",
			r.Number, r.At.Local().Format(logTime), cut(r.From, 12), cut(r.Head, 12), cut(r.Fork, 12), who))
		if len(r.Findings) == 0 {
			out = append(out, "  - no findings")
		}
		for _, f := range r.Findings {
			outcome := cmp.Or(f.Outcome.Text, "no outcome yet")
			out = append(out, fmt.Sprintf("  - %d.%d [%s] %s: %s", r.Number, f.Number, f.agreed(), f.Title, outcome))
			if f.Decided() {
				out = append(out, "    - decided by the user: "+f.Decision)
			}
		}
	}
	return out
}

// agreed is the finding's severity with its labels, as its Agreed severity
// line starts.
func (f Finding) agreed() string {
	s := string(f.Severity)
	if f.PreExisting {
		s = "pre-existing: " + s
	}
	switch {
	case f.Split:
		s = "split: held at " + s
	case f.Flipped:
		s = "flipped: " + s
	}
	return s
}

// check says what's wrong with a review record.
func (r *AutomatedReview) check() error {
	switch {
	case r.Number < 1 || r.From == "" || r.Fork == "" || r.Head == "":
		return errors.New("an automated_review record needs its number and the commits it read")
	case len(r.Reviewers) == 0:
		return errors.New("an automated review needs the reviewers that ran it")
	}
	for _, name := range r.Reviewers {
		if !slices.Contains(reviewers, name) {
			return fmt.Errorf("'%s' isn't a reviewer crew knows: give %s", name, strings.Join(reviewers, " or "))
		}
	}
	for _, name := range r.Failed {
		if !slices.Contains(r.Reviewers, name) {
			return fmt.Errorf("'%s' failed but isn't among the reviewers that ran (%s)", name, strings.Join(r.Reviewers, ", "))
		}
	}
	return nil
}

// check says what's wrong with a finding record.
func (f *Finding) check() error {
	switch {
	case f.Review == "" || f.Number < 1 || strings.TrimSpace(f.Title) == "":
		return errors.New("a finding record needs its review, its number and its title")
	case !slices.Contains(severities, f.Severity):
		return fmt.Errorf("'%s' isn't a severity crew knows: give one of %s", f.Severity, listed(severities))
	case (f.Split || f.Flipped || f.PreExisting) && !f.Severity.held():
		return fmt.Errorf("a %s finding isn't held at a severity, so it can't be split, flipped or pre-existing", f.Severity)
	case f.Flipped && (f.Split || f.PreExisting):
		return errors.New("a flipped finding's split or pre-existing label goes on its Verdict line, not its severity")
	case f.Flipped && f.Severity != SeverityMustFix && f.Severity != SeverityShouldFix:
		return fmt.Errorf("only a finding held at must fix or should fix is flipped, not one at %s", f.Severity)
	}
	return nil
}

// check says what's wrong with an outcome record.
func (o *FindingOutcome) check() error {
	switch {
	case o.Finding == "":
		return errors.New("an outcome record needs its finding")
	case !slices.Contains(outcomes, o.Kind):
		return fmt.Errorf("'%s' isn't an outcome crew knows: give one of %s", o.Kind, listed(outcomes))
	case !strings.HasPrefix(o.Text, string(o.Kind)):
		return fmt.Errorf("an outcome's text starts with its kind, '%s'", o.Kind)
	case o.StillOpenFrom != 0 && o.Kind != OutcomeLeftForUser:
		return fmt.Errorf("only a finding left for the user is still open from an earlier review, not one %s", o.Kind)
	case o.StillOpenFrom < 0:
		return errors.New("an outcome is still open from a review with a number")
	}
	return nil
}

// listed lists values in quotes: 'a', 'b' or 'c'.
func listed[S ~string](values []S) string {
	var q []string
	for _, v := range values {
		q = append(q, "'"+string(v)+"'")
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}
