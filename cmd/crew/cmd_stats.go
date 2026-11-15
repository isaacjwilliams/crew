package main

// crew stats: how much of the user's time each merged task took, how its
// automated review went, and which tasks later needed a fix or a revert, read
// from the ledger and the history of the project's default branch.

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// crew stats [--since YYYY-MM-DD] [--all|--project P]: a row for each task
// merged since the date, 30 days ago by default, then totals and the median
// waits, the same tasks by risk tier, and the fixes merged and reverts made
// since the date, each traced to the task that caused it. It only reads, so it
// needs no lock and runs on any machine; without the project's clone it says
// it didn't check for reverts.
func cmdStats(c *Crew, args []string) error {
	y, m, d := time.Now().Date()
	since := time.Date(y, m, d-30, 0, 0, 0, 0, time.Local)
	all, proj := false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		case args[0] == "--since" && len(args) > 1:
			var err error
			if since, err = time.ParseInLocation("2006-01-02", args[1], time.Local); err != nil {
				return fail("--since takes a date, YYYY-MM-DD, not '%s'", args[1])
			}
			args = args[1:]
		default:
			return usage("crew stats [--since YYYY-MM-DD] [--all|--project P]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	for i, name := range c.projects(scope) {
		p, err := c.ledgerProject(name)
		if err != nil {
			return err
		}
		tasks := c.tasks(name)
		h := historyOf(p, tasks)
		var stats []taskStats
		for _, t := range tasks {
			if t.Status() != Merged {
				continue
			}
			if s := statsOf(t, h); !s.merged.Before(since) {
				stats = append(stats, s)
			}
		}
		slices.SortFunc(stats, func(a, b taskStats) int { return a.merged.Compare(b.merged) })
		if i > 0 {
			fmt.Println()
		}
		printStats(name, since, stats, h)
	}
	return nil
}

// taskStats is how much of the user's time a merged task took, and how its
// automated review went.
type taskStats struct {
	task   Task
	merged time.Time

	rounds, comments int           // the user's review rounds, and their comments in them
	prWait           time.Duration // the PR waiting on the user
	questionWait     time.Duration // the task's questions waiting on the user

	reviews    int // automated reviews
	cleanAfter int // the reviews it took to come back clean, or 0 if none did

	// The reviews' findings the user would ask about: those fixed, those the
	// worker judged not a defect, and those left for the user (a disputed
	// finding is both of the last two). Proposals, recorded nitpicks and
	// withdrawn findings aren't reported.
	fixed, setAside, leftForUser int

	// The task's fixes that merged, and the reverts of its PR, whenever they came.
	laterFixes, reverts int
}

// add adds o's counts and waits to s's.
func (s *taskStats) add(o taskStats) {
	s.rounds += o.rounds
	s.comments += o.comments
	s.prWait += o.prWait
	s.questionWait += o.questionWait
	s.reviews += o.reviews
	s.fixed += o.fixed
	s.setAside += o.setAside
	s.leftForUser += o.leftForUser
	s.laterFixes += o.laterFixes
	s.reverts += o.reverts
}

// cells is the task's row after its ID and kind, under statsHeadings. clean is
// the "clean after" cell.
func (s taskStats) cells(clean string) []string {
	return []string{strconv.Itoa(s.rounds), strconv.Itoa(s.comments), roughly(s.prWait), roughly(s.questionWait),
		strconv.Itoa(s.reviews), clean, strconv.Itoa(s.fixed), strconv.Itoa(s.setAside), strconv.Itoa(s.leftForUser),
		strconv.Itoa(s.laterFixes), strconv.Itoa(s.reverts)}
}

// cleanAfterText is the "clean after" cell: the number of reviews, "never",
// or "-" for a task with no automated review.
func (s taskStats) cleanAfterText() string {
	switch {
	case s.cleanAfter > 0:
		return strconv.Itoa(s.cleanAfter)
	case s.reviews > 0:
		return "never"
	}
	return "-"
}

// statsOf measures a task from its file, and from the project's history for
// its later fixes and reverts.
func statsOf(t Task, h history) taskStats {
	log := withStatusLines(t.LogEntries())
	s := taskStats{
		task:     t,
		merged:   mergedAt(log),
		rounds:   t.FeedbackRounds(),
		comments: t.ReviewComments(),
		prWait:   waited(log, changedTo(PROpen), anyChange),
	}
	asked := changedTo(NeedsInput)
	if t.Goal() != "" {
		// The goal lead answers most of its tasks' workers' questions itself.
		// Only those it leaves for the user wait on them, and for each it logs a
		// "For the user: …" line (goal-lead.md). A collaborator's comment and red
		// CI past MAX_CI_FIXES go to the user directly.
		asked = func(e logEntry) bool {
			return strings.HasPrefix(e.msg, "For the user:") || strings.HasSuffix(e.msg, recordedForTheUser)
		}
	}
	// A question is answered when the task leaves needs-input, or merges.
	// crew tick marks it needs-input only once its worker is idle, so the
	// worker's own changes before then (a review round sent, say) answer
	// nothing.
	answered := func(from, to Status) bool { return from == NeedsInput && to != NeedsInput || to == Merged }
	s.questionWait = waited(log, asked, answered)
	s.laterFixes, s.reverts = tally(h.causedBy(t))

	reviews := t.AutomatedReviews()
	s.reviews = len(reviews)
	for i, r := range reviews {
		if s.cleanAfter == 0 && r.Clean() {
			s.cleanAfter = i + 1
		}
		for _, f := range r.Findings {
			if f.StillOpen() {
				continue // counted in the review that first left it for the user
			}
			if f.Outcome() == OutcomeFixed {
				s.fixed++
			}
			if f.Outcome() == OutcomeLeftForUser {
				s.leftForUser++
			}
			if f.SetAside() {
				s.setAside++
			}
		}
	}
	return s
}

// history is what came after a project's tasks merged: the fix tasks that
// merged, and the reverts on the default branch. crew stats builds it once for
// each project.
type history struct {
	corrections []correction
	unchecked   string // why the reverts weren't read, or nothing when they were
}

// A correction is a fix task that merged, or a revert on the default branch,
// traced where crew can to the task that caused it.
type correction struct {
	at    time.Time // when the fix merged, or the revert was committed
	fix   *Task     // the fix task, or nil for a revert
	pr    string    // the reverted PR's number, for a revert
	cause *Task     // the task it traces to, or nil when nobody can tell
}

// historyOf traces the project's merged fixes, by their fixes key, and the
// reverts on its default branch, by the PR each reverted, to their tasks.
func historyOf(p *Project, tasks []Task) history {
	byID, byPR := map[string]*Task{}, map[string]*Task{}
	for _, t := range tasks {
		byID[t.ID()] = &t
		if t.PR() != "" {
			byPR[t.PRNumber()] = &t
		}
	}
	var h history
	for _, t := range tasks {
		if t.Kind() == KindFix && t.Status() == Merged {
			fixedAt := mergedAt(withStatusLines(t.LogEntries()))
			h.corrections = append(h.corrections, correction{at: fixedAt, fix: &t, cause: byID[t.Fixes()]})
		}
	}
	reverts, err := p.reverts()
	if err != nil {
		h.unchecked = err.Error()
	}
	for _, r := range reverts {
		r.cause = byPR[r.pr]
		h.corrections = append(h.corrections, r)
	}
	return h
}

// causedBy is the corrections traced to t.
func (h history) causedBy(t Task) []correction {
	var out []correction
	for _, c := range h.corrections {
		if c.cause != nil && c.cause.ID() == t.ID() {
			out = append(out, c)
		}
	}
	return out
}

// since is the corrections made since the day, oldest first.
func (h history) since(day time.Time) []correction {
	var out []correction
	for _, c := range h.corrections {
		if !c.at.Before(day) {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b correction) int { return a.at.Compare(b.at) })
	return out
}

// tally counts the fixes and the reverts among the corrections.
func tally(cs []correction) (fixes, reverts int) {
	for _, c := range cs {
		if c.fix != nil {
			fixes++
		} else {
			reverts++
		}
	}
	return fixes, reverts
}

// String is the correction's line in the report: "040-x fixes 031-y (low
// risk), missed by tests", or "revert of #35 → 029-z (medium risk)".
func (c correction) String() string {
	cause := "an unknown task"
	if c.cause != nil {
		cause = fmt.Sprintf("%s (%s)", c.cause.ID(), riskWords(c.cause.Risk()))
	}
	if c.fix == nil {
		return fmt.Sprintf("revert of #%s → %s", c.pr, cause)
	}
	line := c.fix.ID() + " fixes " + cause
	if c.fix.MissedBy() != "" {
		line += ", missed by " + c.fix.MissedBy()
	}
	return line
}

// riskWords names a tier in a sentence: "low risk", or "no risk tier".
func riskWords(r Risk) string {
	if r.Valid() {
		return string(r) + " risk"
	}
	return r.Label()
}

// reverts reads the reverts on the project's default branch from its clone,
// after a quiet fetch, as crew sync does.
func (p *Project) reverts() ([]correction, error) {
	if p.Repo() == "" || !isDir(p.Repo()) {
		return nil, errors.New("no clone of the project on this machine")
	}
	if !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
		warn("%s: git fetch failed; reading reverts as last fetched", p.Name)
	}
	ref := "origin/" + p.DefaultBranch()
	log, err := git(p.Repo(), "log", ref, "--format="+revertLogFormat)
	if err != nil {
		return nil, fmt.Errorf("git log %s failed in %s", ref, p.Repo())
	}
	return revertedPRs(log), nil
}

// revertLogFormat is the git log format revertedPRs reads: each commit's SHA,
// time, subject and body, apart by NUL bytes, and ended by a record separator.
// A revert names the commit it reverts by SHA, so every commit is read, to find
// that commit's subject.
const revertLogFormat = "%H%x00%ct%x00%s%x00%b%x1e"

var (
	// The line git revert, and GitHub's revert button, write in the body.
	revertsCommit = regexp.MustCompile(`This reverts commit ([0-9a-f]{40})`)
	// A PR's merge commit, "Merge pull request #35 from …", or its squash
	// commit, "… (#35)".
	mergesPR = regexp.MustCompile(`^Merge pull request #(\d+) from |\(#(\d+)\)$`)
)

// revertedPRs reads git log's output, in revertLogFormat, for the reverts of
// PRs: commits whose body says "This reverts commit <sha>" of a PR's merge or
// squash commit. Each comes back with the PR's number and when the revert was
// committed.
func revertedPRs(log string) []correction {
	type commit struct {
		at            time.Time
		subject, body string
	}
	var commits []commit
	subjects := map[string]string{}
	for _, record := range strings.Split(log, "\x1e") {
		f := strings.SplitN(strings.TrimLeft(record, "\n"), "\x00", 4)
		if len(f) < 4 {
			continue
		}
		secs, _ := strconv.ParseInt(f[1], 10, 64)
		commits = append(commits, commit{time.Unix(secs, 0), f[2], f[3]})
		subjects[f[0]] = f[2]
	}
	var reverts []correction
	for _, c := range commits {
		for _, m := range revertsCommit.FindAllStringSubmatch(c.body, -1) {
			if pr := mergesPR.FindStringSubmatch(subjects[m[1]]); pr != nil {
				reverts = append(reverts, correction{at: c.at, pr: pr[1] + pr[2]})
			}
		}
	}
	return reverts
}

// mergedAt is when the task's Log says it merged, the last time if it says so
// more than once, or zero if it never does: such a task isn't reported.
func mergedAt(log []logEntry) time.Time {
	var at time.Time
	for _, e := range log {
		if changedTo(Merged)(e) {
			at = e.at
		}
	}
	return at
}

// changedTo picks the Log entries that change the task's status to s.
func changedTo(s Status) func(logEntry) bool {
	return func(e logEntry) bool {
		st, ok := e.status()
		return ok && st == s
	}
}

// waited adds up the time from each Log entry that starts a wait on the user
// to the next change of status that ends it: ends is given the status the task
// had and the one it changed to.
func waited(log []logEntry, starts func(logEntry) bool, ends func(from, to Status) bool) time.Duration {
	var total time.Duration
	var since time.Time // when the wait under way began, or zero
	var status Status   // the task's status before the entry
	for _, e := range log {
		to, changed := e.status()
		if !since.IsZero() && changed && ends(status, to) {
			total += e.at.Sub(since)
			since = time.Time{}
		}
		if since.IsZero() && starts(e) {
			since = e.at
		}
		if changed {
			status = to
		}
	}
	return total
}

// anyChange ends a wait at every change of status.
func anyChange(from, to Status) bool { return true }

// withStatusLines adds the status lines a task dispatched before
// Task.SetStatus lacks. Back then crew dispatch, crew followup and crew sync
// changed the status without one, so each status line goes after the line the
// command did write. Remove this after 2026-12-31: tasks merged before then are
// outside the default 30 days by then.
func withStatusLines(log []logEntry) []logEntry {
	if !dispatchedBeforeSetStatus(log) {
		return log
	}
	var out []logEntry
	for _, e := range log {
		out = append(out, e)
		var status Status
		switch {
		case strings.HasPrefix(e.msg, "dispatched"):
			status = Working
		case strings.HasPrefix(e.msg, "followup ("):
			status = AddressingFeedback // or working, after a note: the worker's turn either way
		case e.msg == "PR merged":
			status = Merged
		}
		if status != "" {
			out = append(out, logEntry{e.at, statusLogged + string(status)})
		}
	}
	return out
}

// dispatchedBeforeSetStatus: the task's "dispatched" line has no status line
// just before it, as crew dispatch writes one now.
func dispatchedBeforeSetStatus(log []logEntry) bool {
	for i, e := range log {
		if strings.HasPrefix(e.msg, "dispatched") {
			return i == 0 || !changedTo(Working)(log[i-1])
		}
	}
	return false
}

// The report's columns, each headed by two lines.
var statsHeadings = [][2]string{
	{"", "task"}, {"", "kind"},
	{"review", "rounds"}, {"", "comments"},
	{"PR", "waited on you"}, {"questions", "waited on you"},
	{"automated", "reviews"}, {"clean", "after"},
	{"findings", "fixed"}, {"set", "aside"}, {"left for", "you"},
	{"later", "fixes"}, {"", "reverts"},
}

// printStats prints a project's report: a row for each task, then the totals
// and the median waits, then the tasks by risk tier, and last the fixes and
// reverts since the date.
func printStats(project string, since time.Time, stats []taskStats, h history) {
	day := since.Format("2006-01-02")
	corrections := h.since(since)
	if len(stats) == 0 {
		fmt.Printf("%s: no tasks merged since %s\n", project, day)
		printCorrections(day, corrections, h.unchecked)
		return
	}
	fmt.Printf("%s: %d task(s) merged since %s\n\n", project, len(stats), day)
	var rows [][]string
	var sum taskStats
	var prWaits, questionWaits []time.Duration
	for _, s := range stats {
		rows = append(rows, append([]string{s.task.ID(), string(s.task.Kind())}, s.cells(s.cleanAfterText())...))
		sum.add(s)
		prWaits = append(prWaits, s.prWait)
		questionWaits = append(questionWaits, s.questionWait)
	}
	rows = append(rows, nil,
		append([]string{"total", ""}, sum.cells("")...),
		[]string{"median", "", "", "", roughly(median(prWaits)), roughly(median(questionWaits))})
	printTable(statsHeadings, rows)

	merged := byTier(stats, func(s taskStats) Risk { return s.task.Risk() })
	fmt.Println("\nBy risk tier:")
	printTable(tierHeadings, tierRows(merged, corrections))
	if lowDrawsMoreComments(merged) {
		fmt.Println("\nLow-risk PRs drew more comments than medium ones: the tiers or SENSITIVE_PATHS may be off.")
	}
	printCorrections(day, corrections, h.unchecked)
}

// The columns of the table by risk tier.
var tierHeadings = [][2]string{
	{"", "risk tier"}, {"tasks", "merged"}, {"median", "rounds"}, {"comments", "per task"},
	{"later", "fixes"}, {"", "reverts"},
}

// byTier groups items by risk tier, in Risk's order, with those without a
// tier last.
func byTier[T any](items []T, risk func(T) Risk) [][]T {
	groups := make([][]T, len(risks)+1)
	for _, item := range items {
		i := risk(item).order()
		groups[i] = append(groups[i], item)
	}
	return groups
}

// tierRows is a row for each risk tier, and one for no tier, from the merged
// tasks grouped by byTier: how many merged, the median of their review rounds,
// their comments per task, and the corrections traced to tasks of the tier,
// whenever those tasks merged.
func tierRows(merged [][]taskStats, corrections []correction) [][]string {
	var traced []correction
	for _, c := range corrections {
		if c.cause != nil {
			traced = append(traced, c)
		}
	}
	caused := byTier(traced, func(c correction) Risk { return c.cause.Risk() })
	var rows [][]string
	for i, tier := range append(slices.Clone(risks), "") {
		medianRounds, perTask := "-", "-"
		if mean, ok := commentsPerTask(merged[i]); ok {
			var rounds []float64
			for _, s := range merged[i] {
				rounds = append(rounds, float64(s.rounds))
			}
			medianRounds, perTask = decimal(median(rounds)), decimal(mean)
		}
		fixes, reverts := tally(caused[i])
		rows = append(rows, []string{tier.Label(), strconv.Itoa(len(merged[i])), medianRounds, perTask,
			strconv.Itoa(fixes), strconv.Itoa(reverts)})
	}
	return rows
}

// commentsPerTask is the mean of the tasks' review comments, or false when
// there are no tasks.
func commentsPerTask(stats []taskStats) (float64, bool) {
	if len(stats) == 0 {
		return 0, false
	}
	comments := 0
	for _, s := range stats {
		comments += s.comments
	}
	return float64(comments) / float64(len(stats)), true
}

// lowDrawsMoreComments: low-risk tasks drew more comments per task than
// medium-risk ones, grouped by byTier. A low tier should need only a glance,
// so the tiers, or the sensitive paths that raise them, may be off.
func lowDrawsMoreComments(merged [][]taskStats) bool {
	low, lowOK := commentsPerTask(merged[RiskLow.order()])
	medium, mediumOK := commentsPerTask(merged[RiskMedium.order()])
	return lowOK && mediumOK && low > medium
}

// printCorrections prints the fixes merged and the reverts made since the day,
// each with the task it traces to, then the checks those fixes say missed
// their defects. unchecked is why the reverts weren't read, if they weren't.
func printCorrections(day string, corrections []correction, unchecked string) {
	fmt.Printf("\nLater fixes and reverts since %s:\n", day)
	for _, c := range corrections {
		fmt.Println("  " + c.String())
	}
	if len(corrections) == 0 {
		fmt.Println("  none")
	}
	var misses []string
	for _, check := range missedBy {
		n := 0
		for _, c := range corrections {
			if c.fix != nil && c.fix.MissedBy() == check {
				n++
			}
		}
		if n > 0 {
			misses = append(misses, fmt.Sprintf("%s %d", check, n))
		}
	}
	if len(misses) > 0 {
		fmt.Println("  missed by: " + strings.Join(misses, ", "))
	}
	if unchecked != "" {
		fmt.Printf("  reverts: not checked (%s)\n", unchecked)
	}
}

// printTable prints rows under two-line headings, each column as wide as its
// widest cell: the first column aligned left, the others right. A row may stop
// short of the last columns, and an empty row is a blank line.
func printTable(headings [][2]string, rows [][]string) {
	widths := make([]int, len(headings))
	for i, h := range headings {
		widths[i] = max(len(h[0]), len(h[1]))
	}
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], len(cell))
		}
	}
	printRow := func(cells []string) {
		line := ""
		for i, cell := range cells {
			if i == 0 {
				line += fmt.Sprintf("  %-*s", widths[i], cell)
			} else {
				line += fmt.Sprintf("  %*s", widths[i], cell)
			}
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	var top, bottom []string
	for _, h := range headings {
		top = append(top, h[0])
		bottom = append(bottom, h[1])
	}
	printRow(top)
	printRow(bottom)
	for _, r := range rows {
		printRow(r)
	}
}

// median is the middle of the values, or the mean of the two in the middle.
func median[T time.Duration | float64](xs []T) T {
	xs = slices.Sorted(slices.Values(xs))
	n := len(xs)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// decimal is x to one decimal place, without a trailing ".0".
func decimal(x float64) string { return strconv.FormatFloat(math.Round(x*10)/10, 'f', -1, 64) }

// roughly is a duration as a person needs it: "40m", "5h", "3d", or "-" for
// none. Up to two days it's in hours, so a day and a half isn't "2d".
func roughly(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Hour:
		return fmt.Sprintf("%dm", d.Round(time.Minute)/time.Minute)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", d.Round(time.Hour)/time.Hour)
	}
	return fmt.Sprintf("%dd", d.Round(24*time.Hour)/(24*time.Hour))
}
