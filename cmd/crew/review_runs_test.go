package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// runsText is each run as "<time> <step> <raised>/<taken>", or "?" for a run of
// unknown value.
func runsText(runs []ReviewRun) []string {
	var out []string
	for _, r := range runs {
		value := "?"
		if r.Known {
			value = fmt.Sprintf("%d/%d", r.Raised, r.Taken)
		}
		out = append(out, r.At.Format(logTime)+" "+r.Step+" "+value)
	}
	return out
}

func TestReviewRuns(t *testing.T) {
	recorded := Task{docWith(t, reviewedTask+
		// Records in the form the agent files give, a rerun's label included.
		"- 2026-11-09 08:00 — Brief review: 5 raised, 3 taken. (1) a gate sharpened; (4) kept: …\n"+
		"- 2026-11-09 08:30 — Brief review (2nd): 1 raised, 1 taken. …\n"+
		"- 2026-11-10 09:00 — Exhale: 4 raised, 2 taken. Folded …; reverted …\n"+
		"- 2026-11-10 09:10 — test-review: 2 raised, 1 taken. …\n"+
		"- 2026-11-10 09:20 — Pre-PR review: 3 raised, 2 taken. Findings declined: (3) …\n"+
		"- 2026-11-10 09:30 — Security review: 0 raised, 0 taken\n"+
		// Other entries that name a step in an old form don't add a run to it.
		"- 2026-11-10 09:40 — PR #70 opened. Pre-PR review (casual-observer, test-review): declined (3) …\n"+
		"- 2026-11-10 09:50 — status → pr-open\n")}
	want := []string{
		"2026-11-09 08:00 Brief review 5/3",
		"2026-11-09 08:30 Brief review 1/1",
		"2026-11-10 09:00 Exhale 4/2",
		"2026-11-10 09:10 test-review 2/1",
		"2026-11-10 09:20 Pre-PR review 3/2",
		"2026-11-10 09:30 Security review 0/0",
		// reviewedTask's automated reviews: every finding raised, the fixed ones taken.
		"2026-11-10 10:00 Automated review 4/1",
		"2026-11-10 11:00 Automated review 2/1",
		"2026-11-10 12:00 Automated review 4/0",
		"2026-11-11 09:00 Automated review 0/0",
	}
	if got := runsText(recorded.ReviewRuns()); !slices.Equal(got, want) {
		t.Errorf("ReviewRuns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	old := Task{docWith(t, "## Evidence\n### Security review\nNo findings.\n\n## Log\n"+
		// The old forms: each counts as a run, of unknown value.
		"- 2026-11-11 08:00 — Plan review: 3 of 4 taken. Rewritten as objective + gates.\n"+
		"- 2026-11-11 08:01 — Plan review 2: (1) Raise, taken: …\n"+
		"- 2026-11-11 08:02 — Brief review (2nd): 5 findings. Taken: (1) …\n"+
		"- 2026-11-11 08:03 — Exhale: folded a repeated line. Skipped: …\n"+
		"- 2026-11-11 08:04 — Specs pass. test-review: no findings. comment-review: cut one sentence.\n"+
		"- 2026-11-11 08:05 — PR #24 opened (draft). Pre-PR review (casual-observer, test-review x2, comment-review): fixed …\n"+
		"- 2026-11-11 08:06 — Branch pushed. test-review x3 (all findings addressed).\n"+
		// Lines that only mention a step aren't runs of it, and the observer
		// ran once however many entries name it.
		"- 2026-11-11 08:07 — Plan review skipped: a one-condition fix.\n"+
		"- 2026-11-11 08:08 — Plan review 3 settled with the user: (1) …\n"+
		"- 2026-11-11 08:09 — Plan review of 057 changed this brief too: …\n"+
		"- 2026-11-11 08:10 — Pre-PR review findings declined: see the entry above. No re-review: …\n"+
		"- 2026-11-11 08:11 — status → pr-open\n")}
	want = []string{
		"2026-11-11 08:00 Brief review ?",
		"2026-11-11 08:01 Brief review ?",
		"2026-11-11 08:02 Brief review ?",
		"2026-11-11 08:03 Exhale ?",
		"2026-11-11 08:04 test-review ?",
		"2026-11-11 08:04 comment-review ?",
		"2026-11-11 08:05 Pre-PR review ?",
		"2026-11-11 08:05 test-review ?",
		"2026-11-11 08:05 test-review ?",
		"2026-11-11 08:05 comment-review ?",
		"2026-11-11 08:06 test-review ?",
		"2026-11-11 08:06 test-review ?",
		"2026-11-11 08:06 test-review ?",
		// The Evidence's security review, by the hand-over it let through.
		"2026-11-11 08:11 Security review ?",
	}
	if got := runsText(old.ReviewRuns()); !slices.Equal(got, want) {
		t.Errorf("old records: ReviewRuns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if got := (Task{docWith(t, sampleTask)}).ReviewRuns(); got != nil {
		t.Errorf("a task with no review step: %v", runsText(got))
	}
}

// The record each agent file tells its agent to write, as written there, is
// read as a run of its step with the counts it states. Every other record of a
// review step the files ask for would be read as a run of unknown value, so
// there is none.
func TestAgentFilesWriteReviewRecords(t *testing.T) {
	logCommand := regexp.MustCompile("`crew log <task> \"([^\"`]*)\"`")
	for file, steps := range map[string][]string{
		"lead.md":        {stepBriefReview},
		"goal-lead.md":   {stepBriefReview},
		"implementer.md": {stepExhale, stepPrePRReview, stepSecurityReview, "<agent name>"},
	} {
		b, err := os.ReadFile("../../agents/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range logCommand.FindAllStringSubmatch(string(b), -1) {
			msg := strings.NewReplacer("<raised>", "7", "<taken>", "2").Replace(m[1])
			e := logEntry{msg: msg}
			if r, ok := recordedRun(e); ok && r.Raised == 7 && r.Taken == 2 {
				got = append(got, r.Step)
			} else if ok || len(oldRunsIn(e)) > 0 {
				t.Errorf("%s: %q isn't read as 7 raised and 2 taken: %+v", file, m[1], r)
			}
		}
		if !slices.Equal(got, steps) {
			t.Errorf("%s records the steps %q, want %q", file, got, steps)
		}
	}
}
