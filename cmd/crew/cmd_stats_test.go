package main

import (
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
			"- 2026-11-01 09:00 — status → pr-open\n- 2026-11-01 10:00 — followup (resume): worker a, carrying on after a pause\n" +
			"- 2026-11-01 15:00 — status → merged\n",
			at("2026-11-01 15:00"), 6 * time.Hour, 0},
		{"a wait the merge didn't end isn't counted", "---\ngoal:\n---\n## Log\n- 2026-11-01 10:00 — status → needs-input\n",
			time.Time{}, 0, 0},
	} {
		s := statsOf(Task{docWith(t, c.task)})
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
	s := statsOf(Task{docWith(t, reviews+round)})
	got := []int{s.rounds, s.comments, s.reviews, s.cleanAfter, s.fixed, s.setAside, s.leftForUser}
	if want := []int{1, 2, 5, 3, 2, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("rounds, comments, reviews, clean after, fixed, set aside, left for the user = %v, want %v", got, want)
	}

	// Only the first review, which needed fixes: it never came back clean.
	first, _, _ := strings.Cut(reviewedTask, "### Automated review 2")
	if s := statsOf(Task{docWith(t, first+"## Log\n")}); s.reviews != 1 || s.cleanAfter != 0 {
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
