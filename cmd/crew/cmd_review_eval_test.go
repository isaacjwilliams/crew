package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// taskIn writes a task file named id in dir.
func taskIn(t *testing.T, dir, id, content string) Task {
	t.Helper()
	p := filepath.Join(dir, id+".md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

// Review 1 read the whole PR and review 2 the commits after it. Round 1 comes
// before any review, round 2 only reports red CI, round 3 follows review 1 and
// round 4 review 2.
const evalTask = "---\nstatus: merged\npr: https://github.com/o/r/pull/12\n---\n" +
	"## Automated reviews\n" +
	"### Automated review 1 — 2026-11-10 10:00\n\n- Range: `f1..h1`, 4 commits on `b` since `main` (fork point from the PR's base)\n- Reviewers: Claude reviewer\n\nNo findings.\n\n" +
	"### Automated review 2 — 2026-11-10 12:00\n\n- Range: `h1..h2`, 2 commits on `b` since review 1 (fork point `f1`)\n- Reviewers: Claude reviewer\n\nNo findings.\n\n" +
	"## Review rounds\n" +
	"### Round 1 — 2026-11-10 09:00 (PR #12)\n\n**Conversation comments**\n- [x] comment 4 — [link](u4)\n  > too early\n\n" +
	"### Round 2 — 2026-11-10 10:30 (PR #12)\n\n- [x] CI failed on h1: test\n\n" +
	"### Round 3 — 2026-11-10 11:00 (PR #12)\n\n**Review summaries**\n- [x] review 7 (changes_requested)\n  > Two things.\n\n" +
	"**Inline comments**\n- [x] `a.go:3` — comment 9 (in thread 2) — [link](u9)\n  > why a second reader?\n\n" +
	"### Round 4 — 2026-11-10 13:00 (PR #12)\n\n**Conversation comments**\n- [x] comment 12 — [link](u12)\n  > name it for the domain\n\n" +
	"## Log\n"

func TestEvalCases(t *testing.T) {
	dir := t.TempDir()
	fix := func(status Status) string {
		return "---\nstatus: " + string(status) + "\nkind: fix\nfixes: 010-merged\ntitle: Fix the race\n---\n## Goal\n> it raced\n\n## Log\n"
	}
	tasks := []Task{
		taskIn(t, dir, "010-merged", evalTask),
		taskIn(t, dir, "011-fix", fix(PROpen)),
		taskIn(t, dir, "012-fix-abandoned", fix(Abandoned)),
		taskIn(t, dir, "013-open", strings.Replace(evalTask, "status: merged", "status: pr-open", 1)),
		taskIn(t, dir, "014-no-range", strings.ReplaceAll(evalTask, "- Range:", "- Scope:")),
		taskIn(t, dir, "015-fixes-unknown", strings.Replace(fix(Merged), "fixes: 010-merged", "fixes: unknown", 1)),
	}
	var got []string
	for _, c := range evalCases(tasks) {
		got = append(got, c.id+" "+c.review.Fork+".."+c.review.Head+": "+c.miss)
	}
	want := []string{
		"010-merged/round-3/review-7 f1..h1: Two things.",
		"010-merged/round-3/comment-9 f1..h1: On `a.go:3`: why a second reader?",
		"010-merged/round-4/comment-12 f1..h2: name it for the domain",
		"010-merged/fix-011-fix f1..h2: Fix the race\n\n> it raced",
	}
	if !slices.Equal(got, want) {
		t.Errorf("evalCases =\n%q\nwant\n%q", got, want)
	}
}

func TestEvalBriefing(t *testing.T) {
	task := taskIn(t, t.TempDir(), "020-a", "---\nbase: main\n---\n"+
		"## Goal\n> Make search fast.\n\nRestatement: an assistant's words.\n\n"+
		"## Structure\nA cache package of its own.\n\n"+
		"## Decisions\n"+
		"- 2026-11-09 — Cache per user (user)\n"+
		"- 2026-11-09 — Use Redis (lead)\n"+
		"- 2026-11-10 — Cache per team after all (user)\n\n"+
		"## Review rounds\n"+
		"### Round 1 — 2026-11-10 11:00 (PR #3)\n\n**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > an earlier point\n  > on two lines\n\n"+
		"### Round 2 — 2026-11-10 13:00 (PR #3)\n\n**Conversation comments**\n- [x] comment 6 — [link](u6)\n  > the miss itself\n\n"+
		"## Log\n")
	at, _ := time.ParseInLocation(logTime, "2026-11-10 12:00", time.Local)
	review := AutomatedReview{Number: 1, At: at, Fork: "f1", Head: "h1"}
	got := evalBriefing(task, review, "/work/_eval", 2, "----- h1\nAdd a cache\n\n## Why\nIt was slow.\n----- h0\n# Start")

	if !strings.HasPrefix(got, "Code review\n") {
		t.Errorf("first line: %q", strings.SplitN(got, "\n", 2)[0])
	}
	var headings []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "## ") {
			headings = append(headings, l)
		}
	}
	if !slices.Equal(headings, briefingHeadings) {
		t.Errorf("headings %q, want %q", headings, briefingHeadings)
	}
	for _, in := range []string{
		"`f1..HEAD`, 2 commits from fork point `f1` on `main` to HEAD `h1`", "`/work/_eval`",
		"### The user's request\n\n> Make search fast.\n",
		"### The user's decisions\n\n- 2026-11-09 — Cache per user (user)\n",
		"### The user's review comments\n\n#### Round 1\n\n> an earlier point\n> on two lines\n",
		"### Commits\n\n----- h1\nAdd a cache\n\n#### Why\nIt was slow.\n----- h0\n#### Start",
	} {
		if !strings.Contains(got, in) {
			t.Errorf("briefing lacks %q:\n%s", in, got)
		}
	}
	// Nothing from after the review, nobody's words but the user's, and nothing
	// the briefing can't know as it was then.
	for _, out := range []string{"the miss itself", "Round 2", "Cache per team", "Redis", "Restatement", "A cache package", "### Pull request"} {
		if strings.Contains(got, out) {
			t.Errorf("briefing has %q:\n%s", out, got)
		}
	}
}

func TestParseJudgement(t *testing.T) {
	for answer, want := range map[string][2]string{
		"found\n#### 2. [should fix] A second reader of rounds — `a.go:3`": {"found", "#### 2. [should fix] A second reader of rounds — `a.go:3`"},
		"**Found.**\n\n> #### 1. [nitpick] Name — `b.go:1`\n":              {"found", "#### 1. [nitpick] Name — `b.go:1`"},
		"found: #### 1. [nitpick] Name":                                    {"found", "#### 1. [nitpick] Name"},
		"found—#### 1. [nitpick] Name":                                     {"found", "#### 1. [nitpick] Name"},
		"Missed\nnone":                                                     {"missed", ""},
		"missed":                                                           {"missed", ""},
		"not a review finding\nNone.":                                      {"not a review finding", ""},
	} {
		v, finding, ok := parseJudgement(answer)
		if !ok || string(v) != want[0] || finding != want[1] {
			t.Errorf("parseJudgement(%q) = %q, %q, %v; want %q, %q", answer, v, finding, ok, want[0], want[1])
		}
	}
	for _, answer := range []string{"", "foundation work", "I think it was found", "maybe"} {
		if v, _, ok := parseJudgement(answer); ok {
			t.Errorf("parseJudgement(%q) = %q, want no verdict", answer, v)
		}
	}
}

// The judge's not-a-finding list is the misses agent's not-a-miss list, in
// its words.
func TestNotAFindingIsTheMissesAgentsList(t *testing.T) {
	b, err := os.ReadFile("../../agents/misses.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range notAFinding {
		if !strings.Contains(string(b), "- "+kind+";") {
			t.Errorf("agents/misses.md has no not-a-miss item %q", kind)
		}
	}
}

func TestEvalReportAndComparison(t *testing.T) {
	result := func(id string, v verdict, detail string) evalResult {
		return evalResult{evalCase{id: id, review: AutomatedReview{Fork: "f1f1f1f1f1", Head: "h1h1h1h1h1"}, miss: "> " + id + "'s miss\n> more"}, v, detail}
	}
	before := []evalResult{
		result("1/round-1/comment-5", verdictMissed, ""),
		result("1/round-1/comment-6", verdictFound, "#### 1. [nitpick] x"),
		result("1/fix-2", verdictMissed, ""),
		result("3/round-2/review-8", verdictNotRun, "the reviewer exited with 1; see r.log"),
	}
	tested := evalSetup{rubric: "/h/rubric.md", rubricHash: "aaaa1111", reviewer: "/a/reviewer.md", reviewerHash: "bbbb2222", claude: "2.1.290"}
	at, _ := time.ParseInLocation(logTime, "2026-11-15 14:30", time.Local)
	report := evalReport("webapp", at, tested, "/h/logs/x", before, "")
	for _, line := range []string{
		"# Review eval of webapp, 2026-11-15 14:30\n",
		"- Rubric: `/h/rubric.md` (aaaa1111)\n- Reviewer: `/a/reviewer.md` (bbbb2222)\n- Claude Code: 2.1.290\n- Cases: all\n",
		"Found 1 of 3 misses. Not run: 1.\n",
		"- `1/round-1/comment-6` **found** — `f1f1f1f..h1h1h1h` — 1/round-1/comment-6's miss — #### 1. [nitpick] x\n",
		"- `1/fix-2` **missed** — `f1f1f1f..h1h1h1h` — 1/fix-2's miss\n",
	} {
		if !strings.Contains(report, line) {
			t.Errorf("report lacks %q:\n%s", line, report)
		}
	}

	prev, verdicts := parseEvalReport(report)
	if prev.rubricHash != "aaaa1111" || prev.reviewerHash != "bbbb2222" || prev.claude != "2.1.290" || len(verdicts) != 4 || verdicts["1/fix-2"] != verdictMissed {
		t.Fatalf("parseEvalReport = %+v, %q", prev, verdicts)
	}
	after := []evalResult{
		result("1/round-1/comment-5", verdictFound, "#### 2. [should fix] y"), // now found
		result("1/round-1/comment-6", verdictMissed, ""),                      // now missed
		result("1/fix-2", verdictMissed, ""),
		result("3/round-2/review-8", verdictFound, "z"), // didn't run before: not shared
		result("4/fix-5", verdictFound, "w"),            // new
	}
	now := tested
	now.rubricHash = "cccc3333"
	want := "Compared with the last full run, `2026-11-15-143000.md`: 1 now found, 1 now missed, of 3 shared case(s); 1 new case(s). " +
		"Since then: the rubric changed (aaaa1111 → cccc3333); the reviewer's prompt is the same; Claude Code is the same."
	if got := compareEvals("2026-11-15-143000.md", prev, verdicts, now, after); got != want {
		t.Errorf("compareEvals =\n%s\nwant\n%s", got, want)
	}
}

// A run narrowed by --task or --limit is a trial: the next run compares with
// the last run over every case instead.
func TestLastFullEvalSkipsPartialRuns(t *testing.T) {
	dir := t.TempDir()
	at, _ := time.ParseInLocation(logTime, "2026-11-15 14:30", time.Local)
	judged := []evalResult{{evalCase{id: "1/fix-2"}, verdictFound, "x"}}
	failed := []evalResult{{evalCase{id: "1/fix-2"}, verdictNotRun, "the reviewer exited with 1"}}
	full := evalSetup{rubricHash: "aaaa1111", reviewerHash: "bbbb2222", claude: "2.1.290"}
	trial, cut := full, full
	trial.selection = "--limit 1"
	cut.selection = "all, cut short: the project was paused"
	for name, run := range map[string]struct {
		setup   evalSetup
		results []evalResult
	}{
		"2026-11-14-090000.md": {full, judged},
		"2026-11-15-100000.md": {trial, judged},
		"2026-11-15-110000.md": {cut, judged},
		"2026-11-15-120000.md": {full, failed},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(evalReport("webapp", at, run.setup, "/l", run.results, "")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file, setup, verdicts, ok := lastFullEval(dir)
	if !ok || filepath.Base(file) != "2026-11-14-090000.md" || setup.selection != "" || verdicts["1/fix-2"] != verdictFound {
		t.Errorf("lastFullEval = %s, %+v, %v, %v", file, setup, verdicts, ok)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "2026-11-15-100000.md")); !strings.Contains(string(b), "- Cases: --limit 1\n") {
		t.Errorf("a trial's report doesn't say how it was narrowed:\n%s", b)
	}
	if _, _, _, ok := lastFullEval(t.TempDir()); ok {
		t.Error("lastFullEval found a report in an empty directory")
	}
}

// The judge gets the brief and what the user had said before the review, to
// tell a change of mind or a settled point from a miss.
func TestJudgePromptHasTheBrief(t *testing.T) {
	task := taskIn(t, t.TempDir(), "021-a", "---\nbase: main\n---\n"+
		"## Goal\n> Make search fast.\n\n"+
		"## Structure\n<!-- a template's note -->\nA cache package of its own.\n\n"+
		"## Plan\n1. Add the cache.\n\n"+
		"## Constraints and conventions\n\n"+
		"## Out of scope\nEviction.\n\n"+
		"## Log\n")
	at, _ := time.ParseInLocation(logTime, "2026-11-10 12:00", time.Local)
	got := judgePrompt("On `a.go:3`: why?", briefBefore(task, AutomatedReview{Number: 1, At: at}), "#### 1. [nitpick] x")
	for _, in := range []string{
		"## The miss\n\nOn `a.go:3`: why?\n",
		"## The brief, and what the user had said before the review\n\n### Structure\n\nA cache package of its own.\n\n### Plan\n\n1. Add the cache.\n\n### Out of scope\n\nEviction.\n\n### The user's request\n\n> Make search fast.\n",
		"## The report\n\n#### 1. [nitpick] x",
	} {
		if !strings.Contains(got, in) {
			t.Errorf("judge prompt lacks %q:\n%s", in, got)
		}
	}
	for _, out := range []string{"template's note", "### Constraints"} {
		if strings.Contains(got, out) {
			t.Errorf("judge prompt has %q:\n%s", out, got)
		}
	}
}

// A brief that says nothing about how settles points in its objective, its
// Context and its gates, so the judge gets those.
func TestJudgePromptHasTheObjectiveAndGates(t *testing.T) {
	task := taskIn(t, t.TempDir(), "022-a", "---\nbase: main\n---\n"+
		"## Goal\n> Make search fast.\n\nObjective: search answers in under 50ms.\n\n"+
		"## Context\n- The user decided results may be a minute stale.\n\n"+
		"## Acceptance criteria\n- p95 under 50ms on the seed data.\n\n"+
		"## Size\nAbout 200 lines: search.\n\n"+
		"## Log\n")
	at, _ := time.ParseInLocation(logTime, "2026-11-10 12:00", time.Local)
	got := judgePrompt("On `a.go:3`: why?", briefBefore(task, AutomatedReview{Number: 1, At: at}), "#### 1. [nitpick] x")
	want := "## The brief, and what the user had said before the review\n\n" +
		"### Objective\n\nObjective: search answers in under 50ms.\n\n" +
		"### Context\n\n- The user decided results may be a minute stale.\n\n" +
		"### Acceptance criteria\n\n- p95 under 50ms on the seed data.\n\n" +
		"### The user's request\n\n> Make search fast.\n"
	if !strings.Contains(got, want) {
		t.Errorf("judge prompt lacks %q:\n%s", want, got)
	}
}
