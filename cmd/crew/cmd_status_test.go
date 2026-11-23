package main

import (
	"strings"
	"testing"
)

// fullStatusLedger is statusLedger with what's left to show: an open proposal
// and a memory suggestion in p, and a third project, r, with a task that asks.
func fullStatusLedger(t *testing.T) *Crew {
	t.Helper()
	c := statusLedger(t)
	writeFile(t, c, "---\nstatus: open\n---\n# An idea\n", "projects", "p", "proposals", "an-idea.md")
	writeFile(t, c, "# Memory suggestions: p\n\n- [ ] **B** b. (lead, 2026-11-09)\n", "projects", "p", "memory-suggestions.md")
	writeFile(t, c, "", "projects", "r", "project.env")
	reportTask(t, c, "r", "001-asks", true, "status: ready")
	return c
}

// plainText is what printReport prints for r.
func plainText(r statusReport) string {
	var b strings.Builder
	printReport(&b, r)
	return b.String()
}

func TestPrintReport(t *testing.T) {
	r := buildReport(fullStatusLedger(t), []string{"p"}, false)
	r.ReadOnlyOn = "laptop"
	want := `read-only: crew runs on laptop
workers running: 1/3

p
  goal 001-launch                         running                                         0 of 9 task(s) merged, 1/2 in flight
    in flight
      001-in-flight                       working              worker: working            -      
    next, in dispatch order
      1  002-next                         ready                                           -      dispatch by hand
      2  003-next                         ready                                           -      dispatch by hand
      3  004-next                         ready                                           -      dispatch by hand
      4  005-next                         ready                                           -      dispatch by hand
      5  006-next                         ready                                           -      dispatch by hand
         … (2 more)
    parked
         009-parked                       draft                                           -      1 open question(s)
  010-parent                              working                                         -      
  └ 011-child                             draft                                           -      1 open question(s)
  013-risky                               pr-open                                         #9     not yet: no security review yet (couldn't read GitHub; as of the last sync)  high
  014-later                               pr-open                                         #12    not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet  low: approve from the evidence
  015-earlier                             pr-open                                         #5     not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet  low: approve from the evidence
  1 draft(s)  (crew list --project p --status draft)
  open proposals: 1  (crew proposals --project p)
  memory suggestions: 1  (projects/p/memory-suggestions.md)
  to review: #5 low, #12 low, #9 high (no security review yet)

waiting on you in other projects  (crew status --all)
  q: 1 task(s) with open questions, 1 PR(s) to review, 1 memory suggestion(s)
  r: 1 task(s) with open questions
`
	if got := plainText(r); got != want {
		t.Errorf("plain text:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintReportWithoutProjects(t *testing.T) {
	got := plainText(statusReport{ReadOnlyOn: "no machine"})
	if want := "read-only: crew runs on no machine\nNo projects registered. Run 'crew project add' in a repo.\n"; got != want {
		t.Errorf("plain text = %q, want %q", got, want)
	}
}
