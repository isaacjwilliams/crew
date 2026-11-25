package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A task whose PR waited on the user twice, 3h then 2h, between a review round
// that sent it back and the merge, and whose question waited 2h.
const measuredLog = "## Log\n" +
	"- 2026-11-10 09:00 — status → working\n- 2026-11-10 09:00 — dispatched: worker a\n" +
	"- 2026-11-10 10:00 — status → needs-input\n- 2026-11-10 12:00 — status → working\n" +
	"- 2026-11-10 13:00 — status → pr-open\n- 2026-11-10 13:00 — handed over\n" +
	"- 2026-11-10 16:00 — review round 1 recorded (1 reviews, 1 inline, 0 conversation)\n" +
	"- 2026-11-10 16:00 — status → addressing-feedback\n- 2026-11-10 16:00 — followup (resume): worker a\n" +
	"- 2026-11-10 17:30 — status → pr-open\n" +
	"- 2026-11-10 19:30 — status → merged\n- 2026-11-10 19:30 — PR merged\n"

func TestStatsOfWaits(t *testing.T) {
	at := func(s string) time.Time {
		tm, _ := time.ParseInLocation(logTime, s, time.Local)
		return tm
	}
	for _, c := range []struct {
		name, task           string
		merged               time.Time
		prWait, questionWait time.Duration
	}{
		{"a task's waits run from its status lines to the next", "---\ngoal:\n---\n" + measuredLog,
			at("2026-11-10 19:30"), 5 * time.Hour, 2 * time.Hour},
		{"a goal task's question waits on the user only once the goal lead leaves it for them",
			"---\ngoal: 001-g\n---\n## Log\n" +
				"- 2026-11-10 10:00 — status → needs-input\n- 2026-11-10 10:30 — For the user: which name?\n" +
				"- 2026-11-10 11:00 — For the user: and which file?\n- 2026-11-10 14:30 — status → working\n" +
				"- 2026-11-10 15:00 — status → merged\n",
			at("2026-11-10 15:00"), 0, 4 * time.Hour},
		{"a goal task's collaborator comment waits on the user from when it's recorded until the task moves on",
			"---\ngoal: 001-g\n---\n## Log\n" +
				"- 2026-11-10 10:00 — status → pr-open\n- 2026-11-10 11:00 — 1 collaborator comment(s) " + recordedForTheUser + "\n" +
				"- 2026-11-10 11:03 — status → needs-input\n- 2026-11-10 14:00 — status → pr-open\n- 2026-11-10 15:00 — status → merged\n",
			at("2026-11-10 15:00"), 2*time.Hour + 3*time.Minute, 3 * time.Hour},
		{"a goal task's collaborator comment still waits while the worker handles a round sent with it",
			"---\ngoal: 001-g\n---\n## Log\n" +
				"- 2026-11-10 10:00 — status → pr-open\n- 2026-11-10 11:00 — 1 collaborator comment(s) " + recordedForTheUser + "\n" +
				"- 2026-11-10 11:00 — status → addressing-feedback\n- 2026-11-10 12:00 — status → pr-open\n" +
				"- 2026-11-10 12:03 — status → needs-input\n- 2026-11-10 15:00 — status → pr-open\n- 2026-11-10 16:00 — status → merged\n",
			at("2026-11-10 16:00"), 2*time.Hour + 3*time.Minute, 4 * time.Hour},
		{"so does a goal task's red CI past MAX_CI_FIXES",
			"---\ngoal: 001-g\n---\n## Log\n" +
				"- 2026-11-10 10:00 — status → pr-open\n- 2026-11-10 11:00 — CI still red on abc1234 after 2 CI rounds; " + recordedForTheUser + "\n" +
				"- 2026-11-10 11:03 — status → needs-input\n- 2026-11-10 13:00 — status → working\n- 2026-11-10 15:00 — status → merged\n",
			at("2026-11-10 15:00"), time.Hour + 3*time.Minute, 2 * time.Hour},
		{"an older task's waits end at the lines that changed its status without saying so",
			"---\ngoal:\n---\n## Log\n" +
				"- 2026-11-01 08:00 — marked ready\n- 2026-11-01 09:00 — dispatched: worker a\n" +
				"- 2026-11-01 10:00 — status → pr-open\n- 2026-11-01 12:00 — followup (resume): worker a\n" +
				"- 2026-11-01 13:00 — status → pr-open\n- 2026-11-02 13:00 — PR merged\n" +
				"- 2026-11-02 13:05 — cleaned up worktree (merged)\n",
			at("2026-11-02 13:00"), 26 * time.Hour, 0},
		{"a followup whose only news is a pause leaves the PR waiting", "---\ngoal:\n---\n## Log\n" +
			"- 2026-11-01 08:00 — status → working\n- 2026-11-01 08:00 — dispatched: worker a\n- 2026-11-01 09:00 — status → pr-open\n- 2026-11-01 10:00 — followup (resume): worker a, carrying on after a pause\n" +
			"- 2026-11-01 15:00 — status → merged\n",
			at("2026-11-01 15:00"), 6 * time.Hour, 0},
		{"an older task's followup ends a wait, though it also says a pause cut the worker short",
			"---\ngoal:\n---\n## Log\n- 2026-11-01 08:00 — dispatched: worker a\n" +
				"- 2026-11-01 09:00 — status → pr-open\n- 2026-11-01 10:00 — followup (resume): worker a, carrying on after a pause\n" +
				"- 2026-11-01 14:00 — status → pr-open\n- 2026-11-01 15:00 — PR merged\n",
			at("2026-11-01 15:00"), 2 * time.Hour, 0},
		{"a wait the merge didn't end isn't counted", "---\ngoal:\n---\n## Log\n- 2026-11-01 10:00 — status → needs-input\n",
			time.Time{}, 0, 0},
	} {
		s := statsOf(Task{docWith(t, c.task)}, history{})
		if !s.merged.Equal(c.merged) || s.prWait != c.prWait || s.questionWait != c.questionWait {
			t.Errorf("%s: merged %v, PR waited %v, questions waited %v; want %v, %v, %v",
				c.name, s.merged, s.prWait, s.questionWait, c.merged, c.prWait, c.questionWait)
		}
	}
}

func TestStatsOfReview(t *testing.T) {
	// reviewedTask (ledger_test.go): four reviews, clean from the third, and a
	// fifth that repeats two findings still left for the user, one of them
	// disputed, with one review round of two comments.
	round := "## Review rounds\n### Round 1 — 2026-11-10 16:00 (PR #12)\n\n" +
		"**Review summaries**\n- [x] review 7 (commented)\n  > see inline\n\n" +
		"**Inline comments**\n- [x] `a.go:3` — comment 9 — [link](u9)\n  > why?\n\n## Log\n"
	reviews, _, _ := strings.Cut(reviewedTask, "## Review rounds")
	reviews += "### Automated review 5 — 2026-11-12 09:00\n\n- Reviewers: Claude reviewer\n\n" +
		"#### 1. [must fix] h — `h.go:8`\n- Earlier: repeat of review 3 #2\n- Agreed severity: must fix\n- Outcome: left for the user: still open from review 3\n\n" +
		"#### 2. [must fix] g — `g.go:7`\n- Earlier: repeat of review 3 #1\n- Agreed severity: disputed\n- Outcome: left for the user: still open from review 3\n\n"
	s := statsOf(Task{docWith(t, reviews+round)}, history{})
	got := []int{s.rounds, s.comments, s.reviews, s.cleanAfter, s.fixed, s.setAside, s.leftForUser}
	if want := []int{1, 2, 5, 3, 2, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("rounds, comments, reviews, clean after, fixed, set aside, left for the user = %v, want %v", got, want)
	}

	// The user later decided what review 3 left for them, the repeat included:
	// stats still counts what automated review couldn't settle on its own.
	decided := reviews + round
	for _, o := range []string{"left for the user\n", "left for the user: pre-existing, proposal 013-h.md\n", "left for the user: still open from review 3\n"} {
		decided = strings.ReplaceAll(decided, "- Outcome: "+o, "- Outcome: "+strings.TrimSuffix(o, "\n")+"; decided by the user: no change\n")
	}
	if n := strings.Count(decided, "; decided by the user"); n != 4 {
		t.Fatalf("decided %d findings, want 4", n)
	}
	s = statsOf(Task{docWith(t, decided)}, history{})
	got = []int{s.rounds, s.comments, s.reviews, s.cleanAfter, s.fixed, s.setAside, s.leftForUser}
	if want := []int{1, 2, 5, 3, 2, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("with the findings decided: rounds, comments, reviews, clean after, fixed, set aside, left for the user = %v, want %v", got, want)
	}

	// Only the first review, which needed fixes: it never came back clean.
	first, _, _ := strings.Cut(reviewedTask, "### Automated review 2")
	if s := statsOf(Task{docWith(t, first+"## Log\n")}, history{}); s.reviews != 1 || s.cleanAfter != 0 {
		t.Errorf("a task never clean: reviews %d, clean after %d", s.reviews, s.cleanAfter)
	}
}

func TestRoughly(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "-", 40 * time.Minute: "40m", 90 * time.Minute: "2h", 36 * time.Hour: "36h",
		47*time.Hour + 20*time.Minute: "47h", 60 * time.Hour: "3d", 10 * 24 * time.Hour: "10d",
	} {
		if got := roughly(d); got != want {
			t.Errorf("roughly(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestMedian(t *testing.T) {
	h := time.Hour
	for _, c := range []struct {
		ds   []time.Duration
		want time.Duration
	}{{nil, 0}, {[]time.Duration{5 * h, 0, 1 * h}, 1 * h}, {[]time.Duration{4 * h, 0, 2 * h, 9 * h}, 3 * h}} {
		if got := median(c.ds); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.ds, got, c.want)
		}
	}
}

// taskFile is a task with the given ID and file.
func taskFile(t *testing.T, id, content string) Task {
	t.Helper()
	p := filepath.Join(t.TempDir(), id+".md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

func TestRevertedPRs(t *testing.T) {
	sha := func(id string) string { return strings.Repeat(id, 40/len(id)) }
	commit := func(id string, at int, subject, body string, parents ...string) string {
		for i, p := range parents {
			parents[i] = sha(p)
		}
		return fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x1e\n", sha(id), strings.Join(parents, " "), at, subject, body)
	}
	// GitHub's revert button on revert PRs: #41 reverted #39 and was merged with a
	// merge commit, #42 reverted #37 and was squashed; then each was reverted.
	// #43 reverted #38 with a merge commit whose body repeats the revert's line.
	// #47 reverted #46, was renamed, merged and reverted. #48's squash body
	// carries a revert of one of its own commits, and #48 was reverted.
	log := commit("cd", 2300, "Bring the export back", "This reverts commit "+sha("ce")+".\n") +
		commit("ce", 2200, "Merge pull request #47 from me/disable", "Disable export until follow-up", "cf", "da") +
		commit("da", 2100, "Disable the export", "This reverts commit "+sha("db")+".\n") +
		commit("db", 2050, "Merge pull request #46 from me/export", "") +
		commit("dc", 2000, `Revert "Fees, again (#48)"`, "This reverts commit "+sha("df")+".\n") +
		commit("df", 1950, "Fees, again (#48)", "* wip\n\n* Revert \"wip\"\n\nThis reverts commit "+sha("de")+".\n") +
		commit("a", 1900, "Revert the revert of #39", "This reverts commit "+sha("b")+".\n") +
		commit("b", 1800, "Merge pull request #41 from me/revert-39", `Revert "Fees (#39)"`, "ab", "c") +
		commit("c", 1700, `Revert "Fees (#39)"`, "This reverts commit "+sha("ab")+".\n") +
		commit("ab", 1650, "Fees (#39)", "") +
		commit("d", 1600, `Revert "Revert "Export (#37)"" (#44)`, "This reverts commit "+sha("e")+".\n") +
		commit("e", 1500, `Revert "Export (#37)" (#42)`, "This reverts commit "+sha("f")+".\n") +
		commit("f", 1400, "Export (#37)", "") +
		commit("0", 1300, "Merge pull request #43 from me/revert-38", "Revert totals\n\nThis reverts commit "+sha("9")+".\n") +
		commit("9", 1200, "Merge pull request #38 from me/totals", "") +
		commit("1", 900, `Revert "Revert "Merge pull request #35 from me/two""`, "This reverts commit "+sha("5")+".\n") +
		commit("2", 800, "Revert the tidy", "This reverts commit "+sha("8")+".\n") +
		commit("3", 700, "Back out totals", "This reverts commit "+sha("7")+".\n") +
		commit("4", 650, "Mention a revert", "This reverts commit 777777, but only in passing.\n") +
		commit("5", 600, `Revert "Merge pull request #35 from me/two"`, "This reverts commit "+sha("6")+", reversing\nchanges made to "+sha("9")+".\n") +
		commit("6", 500, "Merge pull request #35 from me/two", "Two") +
		commit("7", 400, "Round totals to the cent (#36)", "") +
		commit("8", 300, "Tidy the README", "") +
		commit("2", 290, "Back out the totals", "This reverts commit "+sha("9")+".\n")
	var got []string
	for _, c := range revertedPRs(log) {
		got = append(got, fmt.Sprintf("#%s at %d", c.pr, c.at.Unix()))
	}
	// A revert of a revert, merged (renamed or not) or squashed or not a PR's,
	// of an unrelated commit, or of a short SHA isn't a PR's revert; nor is a
	// second commit that says it reverts a commit already reverted.
	if want := []string{"#46 at 2100", "#48 at 2000", "#39 at 1700", "#37 at 1500", "#38 at 1300", "#36 at 700", "#35 at 600"}; !slices.Equal(got, want) {
		t.Errorf("revertedPRs = %v,\nwant %v: a merge commit's and a squash commit's reverts, whatever their subject, each once", got, want)
	}
}

func TestHistoryTracesFixesToTheirTask(t *testing.T) {
	merged := "## Log\n- 2026-09-06 12:00 — status → merged\n"
	cause := taskFile(t, "031-cause", "---\nrisk: low\npr: https://github.com/o/r/pull/35\n---\n"+merged)
	fix := taskFile(t, "040-fix", "---\nkind: fix\nfixes: 031-cause\nmissed_by: tests\nstatus: merged\n---\n"+
		"## Log\n- 2026-11-07 12:00 — status → merged\n")
	untraced := taskFile(t, "041-fix", "---\nkind: fix\nfixes: unknown\nmissed_by: ci\nstatus: merged\n---\n"+merged)
	unmerged := taskFile(t, "042-fix", "---\nkind: fix\nfixes: 031-cause\nstatus: working\n---\n## Log\n")
	h := historyOf(&Project{Name: "p", Settings: Settings{}}, []Task{cause, fix, untraced, unmerged})

	var lines []string
	for _, c := range h.corrections {
		lines = append(lines, c.String())
	}
	if want := []string{"040-fix fixes 031-cause (low risk), missed by tests", "041-fix fixes an unknown task, missed by ci"}; !slices.Equal(lines, want) {
		t.Errorf("corrections = %q, want the merged fixes only, %q", lines, want)
	}
	if h.unchecked != "no clone of the project on this machine" {
		t.Errorf("unchecked = %q, want it to say there's no clone", h.unchecked)
	}

	h.corrections = append(h.corrections, correction{pr: "35", cause: &cause})
	if s := statsOf(cause, h); s.laterFixes != 1 || s.reverts != 1 {
		t.Errorf("the cause's later fixes, reverts = %d, %d; want 1, 1", s.laterFixes, s.reverts)
	}
	if s := statsOf(fix, h); s.laterFixes != 0 || s.reverts != 0 {
		t.Errorf("the fix's later fixes, reverts = %d, %d; want none", s.laterFixes, s.reverts)
	}
	if got := h.corrections[2].String(); got != "revert of #35 → 031-cause (low risk)" {
		t.Errorf("a revert's line = %q", got)
	}
}

// tiered is a merged task's stats with the tier, review rounds and comments.
func tiered(t *testing.T, risk string, rounds, comments int) taskStats {
	return taskStats{task: Task{docWith(t, "---\nrisk: "+risk+"\n---\n")}, rounds: rounds, comments: comments}
}

func TestTierRows(t *testing.T) {
	low, untiered := tiered(t, "low", 1, 2), tiered(t, "", 0, 1)
	merged := byTier([]taskStats{low, tiered(t, "medium", 3, 1), tiered(t, "medium", 1, 0), untiered},
		func(s taskStats) Risk { return s.task.Risk() })
	corrections := []correction{
		{fix: &low.task, cause: &low.task}, {pr: "35", cause: &untiered.task}, {fix: &low.task},
	}
	want := [][]string{
		{"low", "1", "1", "2", "1", "0"},
		{"medium", "2", "2", "0.5", "0", "0"},
		{"high", "0", "-", "-", "0", "0"},
		{"no risk tier", "1", "0", "1", "0", "1"},
	}
	if got := tierRows(merged, corrections); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("tierRows = %q,\nwant %q: an untiered task in the last row, and an untraced fix in none", got, want)
	}
}

func TestLowDrawsMoreComments(t *testing.T) {
	for _, c := range []struct {
		name  string
		tasks []taskStats
		want  bool
	}{
		{"low-risk tasks drew more comments per task", []taskStats{tiered(t, "low", 1, 3), tiered(t, "medium", 1, 1), tiered(t, "medium", 1, 2)}, true},
		{"low-risk tasks drew as many", []taskStats{tiered(t, "low", 1, 2), tiered(t, "medium", 1, 2)}, false},
		{"low-risk tasks drew fewer", []taskStats{tiered(t, "low", 1, 0), tiered(t, "medium", 1, 2)}, false},
		{"no medium-risk task to compare with", []taskStats{tiered(t, "low", 1, 2), tiered(t, "high", 1, 0)}, false},
	} {
		if got := lowDrawsMoreComments(byTier(c.tasks, func(s taskStats) Risk { return s.task.Risk() })); got != c.want {
			t.Errorf("%s: lowDrawsMoreComments = %v, want %v", c.name, got, c.want)
		}
	}
}
