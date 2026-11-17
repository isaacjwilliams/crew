package main

// The ledger's files: tasks, goals and proposals. Each is Markdown with flat
// `key: value` front matter between the first two `---` lines (there's no YAML
// parser), then `## ` sections, the last of which is `## Log`.
//
// A Doc reads its file afresh for every value and writes it back at once, under
// crew's ledger lock: agents edit these files too, so nothing is held in memory.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Doc struct{ Path string }

// ID is the file's name without .md: 007-saved-filters, say.
func (d Doc) ID() string { return strings.TrimSuffix(filepath.Base(d.Path), ".md") }

// Get is the value of a front-matter key, or nothing.
func (d Doc) Get(key string) string {
	v, _ := d.Lookup(key)
	return v
}

// Lookup is the value of a front-matter key, and whether the key is there. A
// value in double quotes is unquoted.
func (d Doc) Lookup(key string) (string, bool) {
	ls := d.lines()
	if len(ls) == 0 || ls[0] != "---" {
		return "", false
	}
	for _, l := range ls[1:] {
		if l == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			v = strings.Trim(v, " \t")
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
			}
			return v, true
		}
	}
	return "", false
}

// Set writes a front-matter key, adding it before the closing --- when it's
// missing. A value goes on one line, and in double quotes when it has ": ", " #"
// or a quote in it.
func (d Doc) Set(key, value string) error {
	value = strings.ReplaceAll(value, "\n", " ")
	if strings.Contains(value, ": ") || strings.Contains(value, " #") || strings.Contains(value, `"`) {
		value = `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	line := key + ": " + value
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	inFM, done := false, false
	for i, l := range ls {
		switch {
		case i == 0 && l == "---":
			inFM = true
		case inFM && l == "---":
			if !done {
				out = append(out, line)
			}
			inFM = false
		case inFM && strings.HasPrefix(l, key+":"):
			if !done {
				out = append(out, line)
				done = true
			}
			continue
		}
		out = append(out, l)
	}
	return d.writeLines(out)
}

// Touch sets updated: to now.
func (d Doc) Touch() error { return d.Set("updated", now()) }

// logTime is how the ledger gives a time, to the minute: in a Log line, and
// in the heading of a review round or an automated review.
const logTime = "2006-01-02 15:04"

// Log appends a dated line to the end of the file, which is the Log section's.
func (d Doc) Log(msg string) error {
	return d.appendRaw("- " + time.Now().Format(logTime) + " — " + msg + "\n")
}

// A line of a file's Log: when it was written, and its message.
type logEntry struct {
	at  time.Time
	msg string
}

// LogEntries is the file's Log, oldest first: the lines Log wrote, read back.
// A line that isn't one, such as one that continues a message, is skipped.
func (d Doc) LogEntries() []logEntry {
	var entries []logEntry
	for _, l := range strings.Split(d.Section("## Log"), "\n") {
		item, ok := strings.CutPrefix(l, "- ")
		if !ok {
			continue
		}
		stamp, msg, ok := strings.Cut(item, " — ")
		if at, err := time.ParseInLocation(logTime, stamp, time.Local); ok && err == nil {
			entries = append(entries, logEntry{at, msg})
		}
	}
	return entries
}

// status is the status the entry changed the task to, when SetStatus wrote it.
// A message that only starts like one, with no status after it, isn't one.
func (e logEntry) status() (Status, bool) {
	s, ok := strings.CutPrefix(e.msg, statusLogged)
	return Status(s), ok && slices.Contains(taskStatuses, Status(s))
}

// Section is a section's text, without HTML comments and blank lines.
func (d Doc) Section(heading string) string {
	var out []string
	on, inComment := false, false
	for _, l := range d.lines() {
		if !on {
			on = l == heading
			continue
		}
		if l == heading {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			break
		}
		var b strings.Builder
		for l != "" {
			if inComment {
				i := strings.Index(l, "-->")
				if i < 0 {
					l = ""
					break
				}
				l, inComment = l[i+3:], false
			} else {
				i := strings.Index(l, "<!--")
				if i < 0 {
					b.WriteString(l)
					l = ""
				} else {
					b.WriteString(l[:i])
					l, inComment = l[i+4:], true
				}
			}
		}
		if strings.Trim(b.String(), " \t") != "" {
			out = append(out, b.String())
		}
	}
	return strings.Join(out, "\n")
}

// OpenQuestions counts the unchecked items under Open questions: each is a
// question still waiting on the user.
func (d Doc) OpenQuestions() int { return unchecked(d.Section("## Open questions")) }

// unchecked counts the lines of text that are unchecked items, "- [ ] …". In
// the ledger, each is something still open.
func unchecked(text string) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "- [ ]") {
			n++
		}
	}
	return n
}

// InsertBefore puts text, and a blank line, before the first line that is
// heading, or at the end of a file without one.
func (d Doc) InsertBefore(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	for i, l := range ls {
		if l == heading {
			out := append(append(append([]string{}, ls[:i]...), splitLines(text)...), "")
			return d.writeLines(append(out, ls[i:]...))
		}
	}
	return d.appendRaw(text)
}

// AppendToSection adds text at the end of a section, just before the next
// heading. A file without the section gets it, before ## Log.
func (d Doc) AppendToSection(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	on, done := false, false
	put := func() {
		out = append(append(out, splitLines(text)...), "")
		done = true
	}
	for _, l := range ls {
		if on && !done && strings.HasPrefix(l, "## ") {
			put()
		}
		out = append(out, l)
		if l == heading {
			on = true
		}
	}
	if !on {
		return d.InsertBefore("## Log", heading+"\n\n"+text)
	}
	if !done {
		out = append(out, "")
		put()
	}
	return d.writeLines(out)
}

// ReplaceSection replaces a section's body with text, keeping a blank line
// before the next heading.
func (d Doc) ReplaceSection(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	skip := false
	for _, l := range ls {
		switch {
		case l == heading:
			out = append(append(out, l), splitLines(text)...)
			skip = true
			continue
		case skip && strings.HasPrefix(l, "## "):
			out = append(out, "")
			skip = false
		}
		if !skip {
			out = append(out, l)
		}
	}
	return d.writeLines(out)
}

// lines is the file's lines, or none when it can't be read.
func (d Doc) lines() []string {
	ls, _ := d.read()
	return ls
}

func (d Doc) read() ([]string, error) {
	b, err := os.ReadFile(d.Path)
	return splitLines(string(b)), err
}

// splitLines splits text into lines the way awk reads them: a final newline ends
// the last line rather than starting another.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// writeLines rewrites the file in place, each line ending in a newline.
func (d Doc) writeLines(ls []string) error {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l + "\n")
	}
	return os.WriteFile(d.Path, []byte(b.String()), 0o644)
}

// appendRaw adds text at the end of the file, after a newline if the file
// doesn't end with one.
func (d Doc) appendRaw(text string) error {
	b, err := os.ReadFile(d.Path)
	if err != nil {
		return err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		text = "\n" + text
	}
	f, err := os.OpenFile(d.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func now() string { return time.Now().Format("2006-01-02T15:04:05-07:00") }

// A task's status. "In flight" means working, needs-input, addressing-feedback
// or pr-open.
type Status string

const (
	Draft              Status = "draft"
	Ready              Status = "ready"
	Working            Status = "working"
	NeedsInput         Status = "needs-input"
	PROpen             Status = "pr-open"
	AddressingFeedback Status = "addressing-feedback"
	Merged             Status = "merged"
	Done               Status = "done"
	Abandoned          Status = "abandoned"
)

var taskStatuses = []Status{Draft, Ready, Working, NeedsInput, PROpen, AddressingFeedback, Merged, Done, Abandoned}

func (s Status) InFlight() bool {
	return s == Working || s == NeedsInput || s == AddressingFeedback || s == PROpen
}

// Finished is merged, done or abandoned: nothing more happens to the task.
func (s Status) Finished() bool { return s.Delivered() || s == Abandoned }

// Delivered is merged or done: what the task was for is in, on the default
// branch or in its report, so nothing is left to merge and its worktree and
// local branch can go.
func (s Status) Delivered() bool { return s == Merged || s == Done }

// A task's kind: what it makes, which decides what its brief needs and what its
// hand-over checks. A build makes a PR; a spike tries a path on a branch nobody
// merges, and makes a report. A fix is a build that mends a defect live in
// production, and names the task that caused it and the check that missed it. A
// refine is any other change to existing work, with a build's rules, so that a
// fix means only a defect in production.
type Kind string

const (
	KindBuild  Kind = "build"
	KindSpike  Kind = "spike"
	KindFix    Kind = "fix"
	KindRefine Kind = "refine"
)

// kindRules is what a kind asks of its task. Commands ask the task's rules and
// never the kind's name.
type kindRules struct {
	kind Kind
	// brief is the sections crew ready needs filled in, and design the ones it
	// needs empty, since how to meet the objective is the worker's call. keys are
	// the front-matter keys it needs set besides a risk tier, each to a value
	// crew set takes.
	brief  []string
	design []string
	keys   []string
	// pr: the kind makes a PR, so its branch is pushed for the user to review,
	// and it needs a risk tier (the look its PR needs). Only such a kind takes
	// part in stacks: a restack has its worker push, and a task stacked on
	// another needs that branch on origin. A kind that makes no PR keeps its
	// branch local and throws it away.
	pr bool
	// link is the front-matter key that ties the task to another, or nothing when
	// the kind takes none; crew new sets it from the flag of the same name
	// (--informs, --fixes). linkTo is the status that other task must have, and
	// linkUnknown lets the link be "unknown" instead, for a task that can't be
	// traced.
	link        string
	linkTo      Status
	linkUnknown bool
	// handOver is what crew hand-over checks (handOverPR, handOverReport, in
	// cmd_new.go), and handedOver the status it moves the task to.
	handOver   func(*Crew, Task) error
	handedOver Status
}

// buildBrief is the sections a build's brief needs, and a fix's and a refine's:
// the objective, the gates that show it's met, and the lead's size estimate.
var buildBrief = []string{"## Goal", "## Acceptance criteria", "## Size"}

// buildDesign is the sections a build's brief leaves empty, and a fix's and a
// refine's: how to meet the objective is the worker's call. The template keeps
// a Plan only because a spike's brief needs one.
var buildDesign = []string{"## Structure", "## Plan", "## Constraints and conventions"}

// kinds are the kinds of task, build first.
var kinds = []kindRules{
	{
		kind:       KindBuild,
		brief:      buildBrief,
		design:     buildDesign,
		pr:         true,
		handOver:   handOverPR,
		handedOver: PROpen,
	},
	{
		kind:       KindSpike,
		brief:      []string{"## Goal", "## Plan", "## Acceptance criteria"},
		link:       "informs",
		linkTo:     Draft,
		handOver:   handOverReport,
		handedOver: Done,
	},
	{
		// A spike needn't inform a draft, but a fix is ready only once it names
		// the task it fixes, so fixes is one of its keys as well as its link.
		kind:        KindFix,
		brief:       buildBrief,
		design:      buildDesign,
		pr:          true,
		keys:        []string{"fixes", "missed_by"},
		link:        "fixes",
		linkTo:      Merged,
		linkUnknown: true,
		handOver:    handOverPR,
		handedOver:  PROpen,
	},
	{
		kind:       KindRefine,
		brief:      buildBrief,
		design:     buildDesign,
		pr:         true,
		handOver:   handOverPR,
		handedOver: PROpen,
	},
}

// Valid: crew knows the kind.
func (k Kind) Valid() bool {
	_, ok := k.rules()
	return ok
}

// rules are what the kind asks, and whether crew knows it.
func (k Kind) rules() (kindRules, bool) {
	for _, r := range kinds {
		if r.kind == k {
			return r, true
		}
	}
	return kindRules{}, false
}

// linkRules is the kind whose link key is key, and whether there is one.
func linkRules(key string) (kindRules, bool) {
	for _, r := range kinds {
		if r.link != "" && r.link == key {
			return r, true
		}
	}
	return kindRules{}, false
}

// kindNames lists the kinds as a sentence does: "build, spike, fix or refine".
func kindNames() string {
	names := make([]string, len(kinds))
	for i, r := range kinds {
		names[i] = string(r.kind)
	}
	return orList(names)
}

// missedBy are the checks a fix's missed_by can name, the one that should have
// caught the defect, in the order work passes them.
var missedBy = []string{"plan-review", "tests", "pre-pr-review", "security-review", "automated-review", "ci", "user-review"}

// orList lists items as a sentence does: "low, medium or high".
func orList[T ~string](items []T) string { return listJoin(items, " or ") }

// andList lists items as a sentence does: "Structure, Plan and Constraints".
func andList[T ~string](items []T) string { return listJoin(items, " and ") }

// listJoin joins items with commas, and the last two with last (" or ", say).
func listJoin[T ~string](items []T, last string) string {
	s := make([]string, len(items))
	for i, item := range items {
		s[i] = string(item)
	}
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + last + s[len(s)-1]
}

// A task's risk tier: what kind of look its PR needs from the user. The lead
// sets it at intake, and crew raises it to high when the branch touches one of
// the project's sensitive paths. Crew never lowers it.
type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// risks are the tiers, cheapest look first.
var risks = []Risk{RiskLow, RiskMedium, RiskHigh}

func (r Risk) Valid() bool { return slices.Contains(risks, r) }

// Look is what the tier asks of the user before they merge. A task without a
// tier, one marked ready before crew ready asked for one say, gets the safe
// look: the diff.
func (r Risk) Look() string {
	switch r {
	case RiskLow:
		return "approve from the evidence"
	case RiskMedium:
		return "read the diff"
	case RiskHigh:
		return "read the diff; security review ran"
	}
	return "read the diff"
}

// Label names the tier, or says there's none.
func (r Risk) Label() string {
	if !r.Valid() {
		return "no risk tier"
	}
	return string(r)
}

// order puts tiers cheapest look first. A task without a tier needs the diff
// read, so it comes after high.
func (r Risk) order() int {
	if i := slices.Index(risks, r); i >= 0 {
		return i
	}
	return len(risks)
}

// A goal's status.
type GoalStatus string

const (
	GoalDraft     GoalStatus = "draft"
	GoalRunning   GoalStatus = "running"
	GoalDone      GoalStatus = "done"
	GoalAbandoned GoalStatus = "abandoned"
)

var goalStatuses = []GoalStatus{GoalDraft, GoalRunning, GoalDone, GoalAbandoned}

type Task struct{ Doc }

func (t Task) Status() Status    { return Status(t.Get("status")) }
func (t Task) Project() string   { return t.Get("project") }
func (t Task) Branch() string    { return t.Get("branch") }
func (t Task) Base() string      { return t.Get("base") }
func (t Task) PR() string        { return t.Get("pr") }
func (t Task) Worktree() string  { return t.Get("worktree") }
func (t Task) WorkerJob() string { return t.Get("worker_job") }
func (t Task) Goal() string      { return t.Get("goal") }
func (t Task) Risk() Risk        { return Risk(t.Get("risk")) }

// Kind is the task's kind. A task without one, made before kinds, is a build.
func (t Task) Kind() Kind {
	if k := Kind(t.Get("kind")); k != "" {
		return k
	}
	return KindBuild
}

// Fixes is the ID of the merged task a fix mends, or "unknown" when nobody
// could trace it.
func (t Task) Fixes() string { return t.Get("fixes") }

// MissedBy is the check that should have caught the defect a fix mends, one of
// missedBy.
func (t Task) MissedBy() string { return t.Get("missed_by") }

// rules are what the task's kind asks, or an error for a kind crew doesn't know.
func (t Task) rules() (kindRules, error) {
	r, ok := t.Kind().rules()
	if !ok {
		return r, fmt.Errorf("%s: kind '%s' isn't %s; fix it with crew set %s kind <kind>", t.ID(), t.Kind(), kindNames(), t.ID())
	}
	return r, nil
}

// StillToLearn counts the unchecked items under Still to learn: what the plan
// rests on that nobody has checked yet. crew ready refuses until there are none.
func (t Task) StillToLearn() int { return unchecked(t.Section("## Still to learn")) }

// DesignInBrief is an error when the brief says how to meet its objective, in
// a section its kind leaves empty. crew ready and crew dispatch both refuse it.
func (t Task) DesignInBrief() error {
	r, err := t.rules()
	if err != nil {
		return err
	}
	var filled []string
	for _, h := range r.design {
		if t.Section(h) != "" {
			filled = append(filled, strings.TrimPrefix(h, "## "))
		}
	}
	if len(filled) == 0 {
		return nil
	}
	return fmt.Errorf("%s's brief still says how to meet its objective, under %s, and that's the worker's call. "+
		"Rewrite it as SETUP.md's \"Rewriting briefs that still have a plan\" says, then run crew ready %s",
		t.ID(), andList(filled), t.ID())
}

// Answer is a spike's answer: the first line of its ## Report, after "Answer:",
// or nothing when the report doesn't start with one.
func (t Task) Answer() string {
	first, _, _ := strings.Cut(t.Section("## Report"), "\n")
	answer, ok := strings.CutPrefix(first, "Answer:")
	if !ok {
		return ""
	}
	return strings.TrimSpace(answer)
}

// statusLogged begins the Log line of every change to a task's or a proposal's
// status: "status → pr-open", say. crew stats reads a task's waits on the user
// from them.
const statusLogged = "status → "

// recordedForTheUser ends the Log line crew feedback and crew ci-fix write when
// they put a question to the user under Open questions: a collaborator's PR
// comment, or red CI past MAX_CI_FIXES. crew stats reads it.
const recordedForTheUser = "recorded under Open questions for the user"

// SetStatus writes the task's status and logs the change. Every change of a
// task's status goes through setStatus, so the Log has a line for each.
func (t Task) SetStatus(s Status) error { return t.setStatus(string(s)) }

// setStatus writes the file's status and logs the change: a task's, or a
// proposal's through crew set. It takes a string, since a proposal's statuses
// aren't a task's.
func (d Doc) setStatus(s string) error {
	if err := d.Set("status", s); err != nil {
		return err
	}
	return d.Log(statusLogged + s)
}

// Priority orders crew tick's dispatch of a running goal's ready tasks: higher
// goes first. A task without one has priority 0.
func (t Task) Priority() (int, error) {
	v := t.Get("priority")
	n, ok := parsePriority(v)
	if !ok {
		return 0, fmt.Errorf("%s: priority '%s' isn't a whole number", t.ID(), v)
	}
	return n, nil
}

// parsePriority reads a task's priority: a whole number, negative to push a task
// back, or empty for 0.
func parsePriority(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// PRNumber is the number at the end of the task's PR URL.
func (t Task) PRNumber() string {
	_, n := splitLast(t.PR())
	return n
}

// Dispatched: crew records a worker or a worktree for it.
func (t Task) Dispatched() bool { return t.WorkerJob() != "" || t.Worktree() != "" }

// HasOpenPR: the task is in flight and has a PR that wasn't closed unmerged.
// crew sync leaves the status of a task whose PR was closed as it was.
func (t Task) HasOpenPR() bool {
	return t.PR() != "" && t.Status().InFlight() && t.Get("pr_state") != "closed"
}

// NewestReview is the number of the newest automated review the task records,
// or 0 when it records none.
func (t Task) NewestReview() int {
	reviews := t.AutomatedReviews()
	if len(reviews) == 0 {
		return 0
	}
	return reviews[len(reviews)-1].Number
}

// An automated review in the task's record: its number and when it was
// recorded, from its heading, the commits it reviewed, whether the Claude
// reviewer reported, and each finding.
type AutomatedReview struct {
	Number int
	At     time.Time
	// Fork is the fork point of the PR it reviewed, and Head the commit its range
	// ends at, so Fork..Head is the whole PR as it was then. Both are empty when
	// the review has no Range line crew can read.
	Fork, Head     string
	ClaudeReported bool
	Findings       []Finding
}

// A finding of an automated review: the rest of its "- Agreed severity:" and
// "- Outcome:" lines in the record.
type Finding struct{ severity, outcome string }

// AutomatedReviews is the reviews under ## Automated reviews, oldest first, in
// the record's format (pr-review's SKILL.md step 8, and LEDGER.md). A review's
// "- Reviewers:" line says "Claude reviewer (failed: …)" when that reviewer
// didn't report; one that failed only in a round reported first.
func (t Task) AutomatedReviews() []AutomatedReview {
	var reviews []AutomatedReview
	var r *AutomatedReview // the review the line is in
	var f *Finding         // the finding the line is in
	for _, l := range strings.Split(t.Section("## Automated reviews"), "\n") {
		rng, isRange := strings.CutPrefix(l, "- Range: ")
		reviewers, isReviewers := strings.CutPrefix(l, "- Reviewers: ")
		severity, isSeverity := strings.CutPrefix(l, "- Agreed severity:")
		outcome, isOutcome := strings.CutPrefix(l, "- Outcome:")
		heading, isHeading := strings.CutPrefix(l, "### Automated review ")
		switch {
		case isHeading:
			n, at := numberedHeading(heading)
			reviews = append(reviews, AutomatedReview{Number: n, At: at, ClaudeReported: true})
			r, f = &reviews[len(reviews)-1], nil
		case r == nil:
		case isRange:
			r.Fork, r.Head = reviewedRange(rng)
		case isReviewers:
			r.ClaudeReported = !strings.Contains(reviewers, "Claude reviewer (failed:")
		case strings.HasPrefix(l, "#### "):
			r.Findings = append(r.Findings, Finding{})
			f = &r.Findings[len(r.Findings)-1]
		case f == nil:
		case isSeverity:
			f.severity = strings.TrimSpace(severity)
		case isOutcome:
			f.outcome = strings.TrimSpace(outcome)
		}
	}
	return reviews
}

// reviewedRange reads the rest of a review's Range line: the fork point of the
// PR it reviewed, and the head its range ends at. A review that read only the
// commits since an earlier one starts its range there and names its fork point
// at the end, "`<from>..<head>`, 2 commits on `b` since review 1 (fork point
// `<fork>`)"; one that read the whole PR starts its range at the fork point,
// and older records don't name it again. Both are empty for a line that has no
// range in backticks.
func reviewedRange(line string) (fork, head string) {
	_, rest, _ := strings.Cut(line, "`")
	rng, _, _ := strings.Cut(rest, "`")
	from, head, ok := strings.Cut(rng, "..")
	if !ok || from == "" || head == "" {
		return "", ""
	}
	if _, named, ok := strings.Cut(line, "(fork point `"); ok {
		fork, _, _ = strings.Cut(named, "`")
		return fork, head
	}
	return from, head
}

// numberedHeading reads what follows "### Round " or "### Automated review " in
// a heading: the number, and the time after the dash, as in "3 — 2026-11-11
// 10:00 (PR #12)". The time is zero when the heading has none.
func numberedHeading(rest string) (int, time.Time) {
	num, stamp, _ := strings.Cut(rest, " — ")
	stamp, _, _ = strings.Cut(stamp, " (")
	at, _ := time.ParseInLocation(logTime, stamp, time.Local)
	return atoiOr0(num), at
}

// Clean is the rule for a review that ends a pass, as implementer.md's
// "Workflow: automated review" step 3 states it: the worker fixed nothing at
// must fix or should fix for it, and the Claude reviewer reported.
func (r AutomatedReview) Clean() bool {
	for _, f := range r.Findings {
		if f.Outcome() == OutcomeFixed && f.mustOrShouldFix() {
			return false
		}
	}
	return r.ClaudeReported
}

// mustOrShouldFix: the finding is held at must fix or should fix, whatever
// labels come before that ("split: held at pre-existing: must fix, …") and
// notes after it ("should fix (lowered from must fix: …)").
func (f Finding) mustOrShouldFix() bool {
	s := f.severity
	for _, label := range []string{"flipped: ", "split: held at ", "pre-existing: "} {
		s = strings.TrimPrefix(s, label)
	}
	return strings.HasPrefix(s, "must fix") || strings.HasPrefix(s, "should fix")
}

// What the worker did about a finding, from the first words of its outcome:
// "left for the user: pre-existing, …" is OutcomeLeftForUser, say.
type Outcome string

const (
	OutcomeFixed       Outcome = "fixed"             // fixed in <sha>
	OutcomeProposal    Outcome = "proposal"          // proposal <file>
	OutcomeRecorded    Outcome = "recorded"          // a nitpick outside the PR's lines
	OutcomeWithdrawn   Outcome = "withdrawn"         // withdrawn in round <n>
	OutcomeNotADefect  Outcome = "not a defect"      // not a defect: <evidence>
	OutcomeLeftForUser Outcome = "left for the user" // left for the user[: <reason>]
	OutcomeOpen        Outcome = "open"              // nothing yet (not a prefix)
	OutcomeOther       Outcome = "other"             // a form crew doesn't know (not a prefix)
)

func (f Finding) Outcome() Outcome {
	if f.outcome == "" {
		return OutcomeOpen
	}
	for _, o := range []Outcome{OutcomeFixed, OutcomeProposal, OutcomeRecorded, OutcomeWithdrawn, OutcomeNotADefect, OutcomeLeftForUser} {
		if strings.HasPrefix(f.outcome, string(o)) {
			return o
		}
	}
	return OutcomeOther
}

// StillOpen: the finding repeats one an earlier review left for the user,
// which the user hasn't decided yet ("left for the user: still open from
// review <N>").
func (f Finding) StillOpen() bool {
	return strings.HasPrefix(f.outcome, string(OutcomeLeftForUser)+": still open from review")
}

// SetAside: the worker judged the finding not a defect, either settling a
// split that way or disputing a finding no reviewer refuted.
func (f Finding) SetAside() bool {
	return f.Outcome() == OutcomeNotADefect || strings.HasPrefix(f.severity, "disputed")
}

// AcceptanceCriteria is the first line of each of the task's criteria: the
// top-level "- " items under ## Acceptance criteria, in order. Nested items and
// the lines that continue an item aren't criteria of their own.
func (t Task) AcceptanceCriteria() []string {
	var criteria []string
	for _, l := range strings.Split(t.Section("## Acceptance criteria"), "\n") {
		if item, ok := strings.CutPrefix(l, "- "); ok {
			criteria = append(criteria, item)
		}
	}
	return criteria
}

// EvidenceGaps is the numbers of the acceptance criteria that have no evidence
// yet. The evidence for criterion n is a "### n." entry under ## Evidence
// (evidenceEntries). It counts the entries and doesn't judge them.
func (t Task) EvidenceGaps() []int {
	recorded := map[int]bool{}
	for _, h := range t.evidenceEntries() {
		num, _, _ := strings.Cut(strings.TrimPrefix(h, "### "), ".")
		if n, err := strconv.Atoi(num); err == nil {
			recorded[n] = true
		}
	}
	var gaps []int
	for i := range t.AcceptanceCriteria() {
		if !recorded[i+1] {
			gaps = append(gaps, i+1)
		}
	}
	return gaps
}

// evidenceEntries is the heading of each entry under ## Evidence that has
// something under it, such as "### 1. the criterion". A heading inside a fenced
// block is output, not an entry.
func (t Task) evidenceEntries() []string {
	var entries []string
	heading, filled := "", false
	fence := "" // the fence the line is inside, or "" outside one
	for _, l := range strings.Split(t.Section("## Evidence"), "\n") {
		inFence := fence != ""
		if !inFence {
			fence = fenceOpening(l)
		} else if strings.HasPrefix(l, fence) && strings.Trim(l, fence[:1]+" ") == "" {
			fence = ""
		}
		if strings.HasPrefix(l, "### ") && !inFence {
			if filled {
				entries = append(entries, heading)
			}
			heading, filled = strings.TrimRight(l, " \t"), false
		} else if heading != "" {
			filled = true
		}
	}
	if filled {
		entries = append(entries, heading)
	}
	return entries
}

// fenceOpening is the run of three or more backticks or tildes that opens a
// fenced block on line l, or "". A line of the same character at least as long
// closes it.
func fenceOpening(l string) string {
	for _, c := range "`~" {
		run := l[:len(l)-len(strings.TrimLeft(l, string(c)))]
		if len(run) >= 3 {
			return run
		}
	}
	return ""
}

// securityReview heads the entry under ## Evidence that records a high-risk
// task's security review: what it found and what the worker did about it.
const securityReview = "### Security review"

// SecurityReviewed: ## Evidence has a securityReview entry with something under
// it, which a high-risk task needs before its hand-over. The entry isn't
// numbered, so EvidenceGaps doesn't count it as a criterion's.
func (t Task) SecurityReviewed() bool {
	return slices.Contains(t.evidenceEntries(), securityReview)
}

// AwaitsSecurityReview: the task is high risk and its security review isn't
// recorded yet.
func (t Task) AwaitsSecurityReview() bool { return t.Risk() == RiskHigh && !t.SecurityReviewed() }

// The lines that report red CI on a PR's head commit (its short SHA): a review
// round's, which sends it to the worker, and an open question's, which asks the
// user once the worker has had MAX_CI_FIXES rounds of them. They are the only
// record of what was reported: crew ci-fix writes them, the Task methods below
// read them back, and the worker ticks them, so their wording is a contract.
const (
	ciFailedOn   = "CI failed on "       // - [ ] CI failed on <sha>: <check>, <check>
	ciStillRedOn = "CI is still red on " // - [ ] CI is still red on <sha> after …
)

// The line that reports a PR's conflict with its base branch, at a commit of
// the base (its short SHA), in a review round that sends it to the worker. Like
// the CI lines, it's the only record: crew catch-up writes it, ConflictReported
// reads it back, and the worker ticks it once it has merged the base in.
const conflictsWith = "Conflicts with " // - [ ] Conflicts with <base> at <sha>

// crewReports are the kinds of line crew writes into a review round of its own,
// to report what it found on the PR rather than pass on the user's feedback.
var crewReports = []string{ciFailedOn, conflictsWith}

// ciLineHead is the head commit an item reports red CI on, when it's a line of
// that kind (ciFailedOn or ciStillRedOn), checked or not.
func ciLineHead(l, kind string) (string, bool) {
	item, ok := checklistItem(l)
	if !ok {
		return "", false
	}
	rest, ok := strings.CutPrefix(item, kind)
	if !ok {
		return "", false
	}
	head, _, _ := strings.Cut(rest, " ")
	return strings.TrimSuffix(head, ":"), true
}

// checklistItem is the text of a checklist item, "- [ ] …" or "- [x] …".
func checklistItem(l string) (string, bool) {
	rest, ok := strings.CutPrefix(l, "- [")
	if !ok || len(rest) < 3 || rest[1:3] != "] " {
		return "", false
	}
	return rest[3:], true
}

// CIFailures is the head commits the task's review rounds reported red CI on,
// oldest first: one for each time its worker was sent a red build to fix.
func (t Task) CIFailures() []string {
	var heads []string
	for _, l := range strings.Split(t.Section("## Review rounds"), "\n") {
		if head, ok := ciLineHead(l, ciFailedOn); ok {
			heads = append(heads, head)
		}
	}
	return heads
}

// ConflictReported: a review round sent the worker the PR's conflict with
// its base at the base commit sha.
func (t Task) ConflictReported(sha string) bool {
	for _, l := range strings.Split(t.Section("## Review rounds"), "\n") {
		item, _ := checklistItem(l)
		rest, ok := strings.CutPrefix(item, conflictsWith)
		if _, at, _ := strings.Cut(rest, " at "); ok && at == sha {
			return true
		}
	}
	return false
}

// CIReported: red CI on head was sent to the worker in a review round, or put
// to the user under Open questions.
func (t Task) CIReported(head string) bool {
	if slices.Contains(t.CIFailures(), head) {
		return true
	}
	for _, l := range strings.Split(t.Section("## Open questions"), "\n") {
		if h, ok := ciLineHead(l, ciStillRedOn); ok && h == head {
			return true
		}
	}
	return false
}

// FeedbackRounds counts the review rounds that hold the user's feedback: every
// round but those that only hold crew's own reports (crewReports), of red CI
// or a conflict with the base. MAX_REVIEW_ROUNDS limits these.
func (t Task) FeedbackRounds() int {
	n := 0
	hasFeedback := true // nothing before the first round counts
	for _, l := range strings.Split(t.Section("## Review rounds"), "\n") {
		item, _ := checklistItem(l)
		report := slices.ContainsFunc(crewReports, func(kind string) bool { return strings.HasPrefix(item, kind) })
		switch {
		case strings.HasPrefix(l, "### Round "):
			hasFeedback = false
		case !report && !hasFeedback:
			n++
			hasFeedback = true
		}
	}
	return n
}

// A review round: its number, when crew recorded it, and the user's comments
// in it.
type ReviewRound struct {
	Number   int
	At       time.Time
	Comments []RoundComment
}

// A comment of the user's in a review round: its checklist item, "review 7
// (changes_requested)", "comment 10 — [link](u10)" or "`a.go:3` — comment 9 —
// [link](u9)", which names it, and its text, which the round quotes on the
// lines below the item.
type RoundComment struct{ Item, Text string }

// ReviewRounds is the task's review rounds, oldest first, as reviewRound.add
// writes them. Red CI and requests for another automated review aren't
// comments, so a round of only those has none.
func (t Task) ReviewRounds() []ReviewRound {
	var rounds []ReviewRound
	var cm *RoundComment // the comment whose quote the line may continue
	for _, l := range strings.Split(t.Section("## Review rounds"), "\n") {
		quote, isQuote := strings.CutPrefix(l, "  >")
		if rest, ok := strings.CutPrefix(l, "### Round "); ok {
			n, at := numberedHeading(rest)
			rounds = append(rounds, ReviewRound{Number: n, At: at})
			cm = nil
		} else if item, ok := checklistItem(l); ok && isComment(item) && len(rounds) > 0 {
			r := &rounds[len(rounds)-1]
			r.Comments = append(r.Comments, RoundComment{Item: item})
			cm = &r.Comments[len(r.Comments)-1]
		} else if isQuote && cm != nil {
			if cm.Text != "" {
				cm.Text += "\n"
			}
			cm.Text += strings.TrimPrefix(quote, " ")
		} else {
			cm = nil
		}
	}
	return rounds
}

// isComment: the checklist item in a review round is one of the user's
// comments: "review 7 (changes_requested)", "comment 10 — …" or
// "`a.go:3` — comment 9 — …".
func isComment(item string) bool {
	return strings.HasPrefix(item, "review ") || strings.HasPrefix(item, "comment ") || strings.Contains(item, " — comment ")
}

// ReviewComments counts the user's comments in the task's review rounds.
func (t Task) ReviewComments() int {
	n := 0
	for _, r := range t.ReviewRounds() {
		n += len(r.Comments)
	}
	return n
}

// A comment the user made after automated review had been through the PR:
// the round it's in, and Review, the newest automated review recorded before
// that round.
type CommentAfterReview struct {
	Round   int
	Comment RoundComment
	Review  AutomatedReview
}

// CommentsAfterReview is the user's comments in the rounds recorded after an
// automated review, oldest first: each is a point that review could have
// raised first. A review or round without a time in its heading can't be
// placed, so it counts as neither.
func (t Task) CommentsAfterReview() []CommentAfterReview {
	reviews := t.AutomatedReviews()
	var after []CommentAfterReview
	for _, round := range t.ReviewRounds() {
		var followed *AutomatedReview // the last before the round: reviews are oldest first
		for i, rv := range reviews {
			if !rv.At.IsZero() && rv.At.Before(round.At) {
				followed = &reviews[i]
			}
		}
		if followed == nil {
			continue
		}
		for _, cm := range round.Comments {
			after = append(after, CommentAfterReview{Round: round.Number, Comment: cm, Review: *followed})
		}
	}
	return after
}

type Goal struct{ Doc }

func (g Goal) Status() GoalStatus { return GoalStatus(g.Get("status")) }
func (g Goal) Project() string    { return g.Get("project") }
func (g Goal) LeadJob() string    { return g.Get("lead_job") }

type Proposal struct{ Doc }

// CheckSet refuses a crew set that would take an accepted proposal away from
// the task that took up its work: a new or empty task, or another status. It
// guards against mistakes: a lead who means it edits the front matter by hand.
// Any other key passes, and so does the value a key already has.
func (p Proposal) CheckSet(key, value string) error {
	if (key != "task" && key != "status") || value == p.Get(key) {
		return nil
	}
	task := p.Get("task")
	if p.Get("status") != "accepted" || task == "" {
		return nil
	}
	return fail("proposal %s is accepted for task %s, which took up its work, so crew set won't change its %s. Leave it as it is, and raise it with the lead.", p.ID(), task, key)
}

// Author is who wrote the proposal, from its source, as a sentence names them.
// It's empty for a proposal without a source.
func (p Proposal) Author() string {
	s := p.Get("source")
	if task, ok := strings.CutPrefix(s, "worker:"); ok {
		return "the worker on " + task
	}
	return map[string]string{"gardener": "the gardener", "misses": "the misses agent", "user": "the user", "lead": "a lead"}[s]
}

// TaskContext is the Context of the task the proposal becomes: where it came
// from, then each of its sections that has text. A short proposal may have
// only Why it matters.
func (p Proposal) TaskContext() string {
	var b strings.Builder
	fmt.Fprintf(&b, "From proposal `%s` (%s)", filepath.Base(p.Path), p.Path)
	if a := p.Author(); a != "" {
		fmt.Fprintf(&b, ", written by %s", a)
	}
	b.WriteString(". Run normal intake: confirm the objective and its gates with the user.\n")
	if r := p.Get("revisit_when"); r != "" {
		fmt.Fprintf(&b, "\nRevisit when: %s\n", r)
	}
	for _, h := range []string{"Why it matters", "Proposal", "First step"} {
		if text := p.Section("## " + h); text != "" {
			fmt.Fprintf(&b, "\n%s:\n%s\n", h, text)
		}
	}
	return b.String()
}
