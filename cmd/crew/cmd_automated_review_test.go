package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// A sampleReview is an automated review as pr-review settles it: the reviewers
// crew automated-review takes, its Reviewers line, and its findings.
type sampleReview struct {
	reviewers, failed string // --reviewers and --failed
	line              string // the review's Reviewers line
	findings          []sampleFinding
}

// A sampleFinding is a finding as pr-review settles it and the worker acts on
// it: the severity and labels crew finding takes, its Agreed severity line, the
// outcome the worker writes ("" before it acts) and the user's decision.
type sampleFinding struct {
	title, severity  string
	labels           []string
	agreed           string
	outcome, decided string
}

// reviewSample is reviewedTask's four reviews (ledger_test.go) and the fifth
// of TestStatsOfReview, with the user's decisions there: the second review's
// Claude reviewer failed, the third's failed only in a round, and the fifth
// repeats two findings still open from the third.
var reviewSample = []sampleReview{
	{"claude,codex", "", "Claude reviewer and Codex", []sampleFinding{
		{"a", "must fix", nil, "must fix", "fixed in abc1234", ""},
		{"b", "should fix", []string{"--split", "--pre-existing"}, "split: held at pre-existing: should fix, Codex refutes", "not a defect: `b.go:2` checks it first", ""},
		{"c", "nitpick", nil, "nitpick", "recorded", ""},
		{"d", "fix later", nil, "fix later", "proposal 012-d.md", ""},
	}},
	{"claude,codex", "claude", "Claude reviewer (failed: it stalled) and Codex", []sampleFinding{
		{"e", "nitpick", nil, "nitpick", "fixed in def5678", ""},
		{"f", "withdrawn", nil, "withdrawn", "withdrawn in round 1", ""},
	}},
	{"claude,codex", "codex", "Claude reviewer (failed in round 2: it stalled) and Codex (failed: signed out)", []sampleFinding{
		{"g", "disputed", nil, "disputed", "left for the user", "no change"},
		{"h", "must fix", []string{"--pre-existing"}, "pre-existing: must fix", "left for the user: pre-existing, proposal 013-h.md", "no change"},
		{"i", "must fix", []string{"--flipped"}, "flipped: must fix, was withdrawn in review 2", "left for the user: verdict changed since review 2", ""},
		{"j", "nitpick", nil, "nitpick", "", ""},
	}},
	{"claude", "", "Claude reviewer", nil},
	{"claude", "", "Claude reviewer", []sampleFinding{
		{"h", "must fix", nil, "must fix", "left for the user: still open from review 3", "no change"},
		{"g", "disputed", nil, "disputed", "left for the user: still open from review 3", "no change"},
	}},
}

// reviewCrew is a home whose task 001-task has a PR and, on its branch, a
// commit past main.
func reviewCrew(t *testing.T) (followupCrew, Task, gitRepo) {
	t.Helper()
	f := newFollowupCrew(t, "git")
	task := f.task(t, "pr: u")
	content, err := os.ReadFile(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.Path, []byte(strings.Replace(string(content), "## Log\n", "## Automated reviews\n\n## Log\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	branch := gitRepo{t, f.worktree()}
	branch.commit("first")
	t.Cleanup(f.unlock)
	return f, task, branch
}

// recorded runs a crew command and gives back what it printed, failing the test if
// the command refuses.
func recorded(t *testing.T, f followupCrew, cmd func(*Crew, []string) error, args ...string) string {
	t.Helper()
	said := watchTerminal(t)
	err := cmd(f.Crew, args)
	out := strings.TrimSuffix(said(), "\n")
	if err != nil {
		t.Fatalf("%q: %v\n%s", args, err, out)
	}
	return out
}

// recordSample records the sample's reviews, a commit apiece, as SKILL.md
// step 8 and implementer.md say: through crew, with the text beside it that
// takes the numbers crew prints. It gives back the text, and each review's
// heading and Range line.
func recordSample(t *testing.T, f followupCrew, task Task, branch gitRepo) (text string, headings, ranges []string) {
	t.Helper()
	fork := branch.git("merge-base", "origin/main", "HEAD")
	from, since := fork, "`main`"
	for i, r := range reviewSample {
		if i > 0 {
			branch.commit(fmt.Sprintf("fixes for review %d", i))
		}
		head := branch.git("rev-parse", "HEAD")
		args := []string{task.ID(), from + ".." + head, "--fork", fork, "--reviewers", r.reviewers}
		if r.failed != "" {
			args = append(args, "--failed", r.failed)
		}
		heading := recorded(t, f, cmdAutomatedReview, args...)
		if want := fmt.Sprintf("### Automated review %d — ", i+1); !strings.HasPrefix(heading, want) {
			t.Fatalf("crew automated-review printed %q, want %q and the time", heading, want)
		}
		rng := fmt.Sprintf("`%s..%s`, 1 commits on `%s` since %s (fork point `%s`)", from, head, followupBranch, since, fork)
		headings, ranges = append(headings, heading), append(ranges, rng)
		text += heading + "\n\n- Range: " + rng + "\n- Reviewers: " + r.line + "\n\n"
		for k, s := range r.findings {
			n := recorded(t, f, cmdFinding, append([]string{task.ID(), s.severity, s.title}, s.labels...)...)
			if want := fmt.Sprintf("%d.%d", i+1, k+1); n != want {
				t.Fatalf("crew finding printed %q, want %q", n, want)
			}
			text += fmt.Sprintf("#### %d. [%s] %s — `%s.go:%d`\n- Agreed severity: %s\n- Outcome:", k+1, s.severity, s.title, s.title, k+1, s.agreed)
			if s.outcome != "" {
				recorded(t, f, cmdOutcome, task.ID(), n, s.outcome)
				text += " " + s.outcome
			}
			if s.decided != "" {
				recorded(t, f, cmdUserDecided, task.ID(), n, s.decided)
				text += "; decided by the user: " + s.decided
			}
			text += "\n\n"
		}
		from, since = head, fmt.Sprintf("review %d", i+1)
	}
	if err := task.ReplaceSection("## Automated reviews", "\n"+text); err != nil {
		t.Fatal(err)
	}
	return text, headings, ranges
}

// reviewFacts are what crew reads of each review and finding, one line apiece.
// The text gives only whether the Claude reviewer reported, not which others
// failed.
func reviewFacts(reviews []AutomatedReview) []string {
	var facts []string
	for _, r := range reviews {
		facts = append(facts, fmt.Sprintf("review %d at %s read %s..%s from %s; Claude reported %v, clean %v, %d awaiting the user",
			r.Number, r.At.Local().Format(logTime), r.From, r.Head, r.Fork, r.ClaudeReported(), r.Clean(), r.AwaitingUser()))
		for _, f := range r.Findings {
			facts = append(facts, fmt.Sprintf("  %d.%d %s (split %v, flipped %v, pre-existing %v), must or should fix %v: %q, still open from %d (%v), decided %q (%v), set aside %v",
				r.Number, f.Number, f.Severity, f.Split, f.Flipped, f.PreExisting, f.mustOrShouldFix(),
				f.Outcome.Kind, f.Outcome.StillOpenFrom, f.StillOpen(), f.Decision, f.Decided(), f.SetAside()))
		}
	}
	return facts
}

// What the worker records through crew reads back as what crew reads from the
// same reviews written the old way, and from the text the new way writes.
func TestAutomatedReviewRecords(t *testing.T) {
	f, task, branch := reviewCrew(t)
	_, headings, ranges := recordSample(t, f, task, branch)

	// The old way: reviewedTask and TestStatsOfReview's fifth review, the user's
	// decisions added as the worker adds them, each review under the heading
	// crew printed and with its Range line.
	reviews, _, _ := strings.Cut(reviewedTask, "## Review rounds")
	old := decideAll(reviews + stillOpenReview)
	for i := range headings {
		heading := regexp.MustCompile(fmt.Sprintf(`### Automated review %d — [^\n]*\n`, i+1))
		old = heading.ReplaceAllLiteralString(old, headings[i]+"\n\n- Range: "+ranges[i]+"\n")
	}
	oldWay := Task{docWith(t, "---\npr: u\nbranch: "+followupBranch+"\nbase: main\n---\n"+old+"## Log\n")}

	recorded, err := task.RecordedReviews()
	if err != nil {
		t.Fatal(err)
	}
	records, text, want := reviewFacts(recorded), reviewFacts(task.AutomatedReviews()), reviewFacts(oldWay.AutomatedReviews())
	if !slices.Equal(records, want) {
		t.Errorf("the records read as:\n%s\nwant, as the old way's text:\n%s", strings.Join(records, "\n"), strings.Join(want, "\n"))
	}
	if !slices.Equal(text, want) {
		t.Errorf("the new way's text reads as:\n%s\nwant, as the old way's:\n%s", strings.Join(text, "\n"), strings.Join(want, "\n"))
	}

	// Until crew decides from the records, what it decides from the text is as
	// it was. Hand-over's checks: a review there, and not stale until the branch
	// moves on from what it read.
	for _, tk := range []Task{task, oldWay} {
		if tk.AwaitsAutomatedReview("3") {
			t.Errorf("hand-over awaits an automated review of %s", tk.Path)
		}
		if why := staleReview(tk, f.repo); why != "" {
			t.Errorf("hand-over finds %s's newest review stale: %s", tk.Path, why)
		}
	}
	branch.git("commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
	if got, want := staleReview(task, f.repo), staleReview(oldWay, f.repo); got != want || got == "" {
		t.Errorf("once the branch is rewritten, hand-over finds the review stale as %q, and the old way as %q", got, want)
	}
	// The merge check's count, stats, and crew review-summary's review number.
	newest := func(tk Task) int { r := tk.AutomatedReviews(); return r[len(r)-1].AwaitingUser() }
	if got, want := newest(task), newest(oldWay); got != want {
		t.Errorf("the merge check counts %d findings left for the user, the old way %d", got, want)
	}
	stats, oldStats := statsOf(task, history{}), statsOf(oldWay, history{})
	stats.task, oldStats.task = Task{}, Task{}
	if stats != oldStats {
		t.Errorf("stats: %+v\nthe old way: %+v", stats, oldStats)
	}
	if got, want := task.NewestReview(), oldWay.NewestReview(); got != want || got != len(reviewSample) {
		t.Errorf("crew review-summary would summarize review %d, the old way %d", got, want)
	}
}

// A review the text numbered before crew recorded reviews still counts: the
// next one crew records is one past it.
func TestAutomatedReviewNumberedAfterTheText(t *testing.T) {
	f, task, branch := reviewCrew(t)
	if err := task.ReplaceSection("## Automated reviews", "\n### Automated review 1 — 2026-11-10 10:00\n\n### Automated review 2 — 2026-11-10 11:00\n"); err != nil {
		t.Fatal(err)
	}
	head := branch.git("rev-parse", "HEAD")
	fork := branch.git("merge-base", "origin/main", "HEAD")
	if heading := recorded(t, f, cmdAutomatedReview, task.ID(), fork+".."+head, "--fork", fork, "--reviewers", "claude"); !strings.HasPrefix(heading, "### Automated review 3 — ") {
		t.Errorf("crew automated-review printed %q, want review 3", heading)
	}
	if n := recorded(t, f, cmdFinding, task.ID(), "nitpick", "a"); n != "3.1" {
		t.Errorf("crew finding printed %q, want 3.1", n)
	}
}

// crew refuses what it doesn't know, saying what's wrong, and leaves the
// ledger as it was.
func TestAutomatedReviewRefusals(t *testing.T) {
	f, task, branch := reviewCrew(t)
	recordSample(t, f, task, branch)
	fork := branch.git("merge-base", "origin/main", "HEAD")
	head := branch.git("rev-parse", "HEAD")
	runIn(t, f.repo, "git", "checkout", "-q", "-b", "elsewhere", "main")
	gitRepo{t, f.repo}.commit("not on the branch")
	elsewhere := gitRepo{t, f.repo}.git("rev-parse", "HEAD")
	id := task.ID()

	for _, c := range []struct {
		cmd  func(*Crew, []string) error
		args []string
		says string
	}{
		// Severities and outcomes crew doesn't know.
		{cmdFinding, []string{id, "critical", "x"}, "'critical' isn't a severity crew knows"},
		{cmdFinding, []string{id, "disputed", "x", "--split"}, "isn't held at a severity"},
		{cmdFinding, []string{id, "nitpick", "x", "--flipped"}, "only a finding held at must fix or should fix is flipped"},
		{cmdFinding, []string{id, "must fix", "x", "--flipped", "--split"}, "goes on its Verdict line"},
		{cmdOutcome, []string{id, "3.4", "left for user"}, "'left for user' doesn't start with an outcome crew knows"},
		{cmdOutcome, []string{id, "3.4", "fixedin abc1234"}, "doesn't start with an outcome crew knows"},
		{cmdOutcome, []string{id, "3.4", "left for the user: still open from review two"}, "needs the number of the review"},
		{cmdOutcome, []string{id, "3.4", "left for the user: still open from review 3"}, "earlier review than 3"},
		{cmdAutomatedReview, []string{id, fork + ".." + head, "--fork", fork, "--reviewers", "gemini"}, "'gemini' isn't a reviewer crew knows"},
		{cmdAutomatedReview, []string{id, fork + ".." + head, "--fork", fork, "--reviewers", "claude", "--failed", "codex"}, "'codex' failed but isn't among the reviewers"},
		// An outcome or a decision for a finding that isn't recorded, or that
		// already has one.
		{cmdOutcome, []string{id, "3.9", "recorded"}, "records no finding 3.9"},
		{cmdOutcome, []string{id, "6.1", "recorded"}, "records no finding 6.1"},
		{cmdUserDecided, []string{id, "9.1", "no change"}, "records no finding 9.1"},
		{cmdOutcome, []string{id, "1.1", "recorded"}, "1.1 already has its outcome, 'fixed in abc1234'"},
		{cmdUserDecided, []string{id, "3.1", "take the fix"}, "the user already decided 3.1"},
		{cmdUserDecided, []string{id, "1.1", "no change"}, "1.1 wasn't left for the user"},
		{cmdUserDecided, []string{id, "3.4", "no change"}, "3.4 has no outcome yet"},
		// A range crew can't resolve to commits of the task's branch.
		{cmdAutomatedReview, []string{id, "nope.." + head, "--fork", fork, "--reviewers", "claude"}, "crew can't find a commit 'nope'"},
		{cmdAutomatedReview, []string{id, head, "--fork", fork, "--reviewers", "claude"}, "as FROM..HEAD"},
		{cmdAutomatedReview, []string{id, fork + ".." + elsewhere, "--fork", fork, "--reviewers", "claude"}, "isn't on " + followupBranch},
		{cmdAutomatedReview, []string{id, head + ".." + fork, "--fork", fork, "--reviewers", "claude"}, head + " doesn't come before " + fork},
		{cmdAutomatedReview, []string{id, head + ".." + head, "--fork", fork, "--reviewers", "claude"}, "holds no commits"},
		{cmdAutomatedReview, []string{id, fork + ".." + head, "--fork", head, "--reviewers", "claude"}, "the fork point " + head + " doesn't come before"},
	} {
		before := ledgerState(t, f, task)
		said := watchTerminal(t)
		err := c.cmd(f.Crew, c.args)
		said()
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: %v, want a refusal saying %q", c.args, err, c.says)
		}
		if after := ledgerState(t, f, task); after != before {
			t.Errorf("%q changed the ledger:\n%s\nwas:\n%s", c.args, after, before)
		}
	}
}

// ledgerState is the task's files and the home's commits, for telling whether
// a command changed them.
func ledgerState(t *testing.T, f followupCrew, task Task) string {
	t.Helper()
	var state []string
	for _, p := range []string{task.Path, task.RecordsPath()} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		state = append(state, string(b))
	}
	head, _ := git(f.Home, "rev-parse", "HEAD")
	status, _ := git(f.Home, "status", "--porcelain")
	return strings.Join(append(state, head, status), "\n---\n")
}

// crew show puts the recorded reviews at the top of Automated reviews, after
// the section's comment and before the text written beside them.
func TestShownTaskReviews(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	fork, from, head := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	file := "# A\n\n## Automated reviews\n<!-- Reviews. -->\n\n### Automated review 1 — 2026-11-25 10:00\n\n## Log\n- x\n"
	task := learnTask(t, file)
	if got, _ := shownTask(task.Path); got != file {
		t.Errorf("shownTask of a task with no review records = %q; want the file as it is", got)
	}
	for _, r := range []Record{
		{ID: "r1", At: at, By: "crew automated-review", AutomatedReview: &AutomatedReview{Number: 1, From: from, Fork: fork, Head: head, Reviewers: []string{"claude", "codex"}, Failed: []string{"codex"}}},
		{ID: "f1", At: at, By: "crew finding", Finding: &Finding{Review: "r1", Number: 1, Title: "a nil map", Severity: SeverityMustFix}},
		{ID: "f2", At: at, By: "crew finding", Finding: &Finding{Review: "r1", Number: 2, Title: "an old race", Severity: SeverityShouldFix, Split: true, PreExisting: true}},
		{ID: "f3", At: at, By: "crew finding", Finding: &Finding{Review: "r1", Number: 3, Title: "a name", Severity: SeverityNitpick}},
		{ID: "o1", At: at, By: "crew outcome", Outcome: &FindingOutcome{Finding: "f1", Kind: OutcomeFixed, Text: "fixed in abc1234"}},
		{ID: "o2", At: at, By: "crew outcome", Outcome: &FindingOutcome{Finding: "f2", Kind: OutcomeLeftForUser, Text: "left for the user: pre-existing, a day's work; proposal 012-race.md"}},
		{ID: "d2", At: at, By: "crew user-decided", Decision: &FindingDecision{Finding: "f2", Decision: "leave it for now"}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := shownTask(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# A\n\n## Automated reviews\n<!-- Reviews. -->\n\nRecorded by crew:\n" +
		"- Review 1, 2026-11-25 10:00: `bbbbbbbbbbbb..cccccccccccc` (fork point `aaaaaaaaaaaa`), by claude and codex (codex didn't report)\n" +
		"  - 1.1 [must fix] a nil map: fixed in abc1234\n" +
		"  - 1.2 [split: held at pre-existing: should fix] an old race: left for the user: pre-existing, a day's work; proposal 012-race.md\n" +
		"    - decided by the user: leave it for now\n" +
		"  - 1.3 [nitpick] a name: no outcome yet\n" +
		"\n### Automated review 1 — 2026-11-25 10:00\n\n## Log\n- x\n"
	if got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
	}

	noSection := learnTask(t, "# B\n\n## Log\n- x\n")
	os.WriteFile(noSection.RecordsPath(), []byte("oops\n"), 0o644)
	if got, _ := shownTask(noSection.Path); !strings.Contains(got, "## Automated reviews\n\ncrew can't read the reviews' records: 001-a.records.jsonl:1: not a record crew can read") {
		t.Errorf("shownTask with an unreadable records file = %q; want Automated reviews to say so", got)
	}
}
