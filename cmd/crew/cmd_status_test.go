package main

import (
	"slices"
	"strings"
	"testing"
)

// fullStatusLedger is statusLedger with what's left to show: a goal lead, a
// task's model and effort, an open proposal and a memory suggestion in p, and a
// third project, r, with a task that asks.
func fullStatusLedger(t *testing.T) *Crew {
	t.Helper()
	c := statusLedger(t)
	writeFile(t, c, `#!/bin/sh
echo '[{"id":"w1","state":"working","pid":1},{"id":"g1","state":"idle","pid":2}]'
`, "bin", "claude")
	writeFile(t, c, "---\nid: 001-launch\nstatus: running\nlead_job: g1\n---\n# launch\n\n## Log\n", "projects", "p", "goals", "001-launch.md")
	reportTask(t, c, "p", "010-parent", false, "status: working", "base: main", "branch: agent/parent", "model: sonnet", "effort: high")
	writeFile(t, c, "---\nstatus: open\n---\n# An idea\n", "projects", "p", "proposals", "an-idea.md")
	suggestFact(t, c, "p", "B")
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
	r := readReport(fullStatusLedger(t), "p")
	r.ReadOnlyOn = "laptop"
	want := `read-only: crew runs on laptop
workers running: 1/3

p
  task               status   worker   PR   model   effort  notes
  goal 001-launch    running  idle                          0 of 9 task(s) merged, 1/2 in flight
    in flight
      001-in-flight  working  working
    next, in dispatch order
      1  002-next    ready                                  dispatch by hand
      2  003-next    ready                                  dispatch by hand
      3  004-next    ready                                  dispatch by hand
      4  005-next    ready                                  dispatch by hand
      5  006-next    ready                                  dispatch by hand
      … (2 more)
    parked
         009-parked  draft                                  1 open question(s)
  010-parent         working                sonnet  high
    └ 011-child      draft                                  1 open question(s)
  013-risky          pr-open           #9                   not yet: no security review yet (couldn't read GitHub; as of the last sync)  high
  014-later          pr-open           #12                  not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet  low: approve from the evidence
  015-earlier        pr-open           #5                   not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet  low: approve from the evidence
  1 draft(s)  (crew list --project p --status draft)
  open proposals: 1  (crew proposals --project p)
  memory suggestions: 1  (crew show memory-suggestions --project p)
  to review: #5 low, #12 low, #9 high (no security review yet)

waiting on you in other projects  (crew status --all)
  q: 1 task(s) with open questions, 1 PR(s) to review, 1 memory suggestion(s)
  r: 1 task(s) with open questions
`
	if got := plainText(r); got != want {
		t.Errorf("plain text:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintReportLeavesFoldedRowsOutOfItsColumns(t *testing.T) {
	c := fullStatusLedger(t)
	reportTask(t, c, "p", "016-an-idea-with-a-longer-name-than-any-row-printed", false, "status: draft", "base: main")
	writeFile(t, c, "", "projects", "s", "project.env")
	reportTask(t, c, "s", "001-idea", false, "status: draft")
	var b strings.Builder
	printReport(&b, readReport(c, "p"))
	if want := "\n  task               status   worker"; !strings.Contains(b.String(), want) {
		t.Errorf("a folded draft widens the columns: want %q in\n%s", want, b.String())
	}
	b.Reset()
	printReport(&b, readReport(c, "s"))
	if strings.Contains(b.String(), "  task  ") {
		t.Errorf("headings over a project whose rows are all folded:\n%s", b.String())
	}
}

func TestPrintReportWithoutProjects(t *testing.T) {
	got := plainText(statusReport{ReadOnlyOn: "no machine"})
	if want := "read-only: crew runs on no machine\nNo projects registered. Run 'crew project add' in a repo.\n"; got != want {
		t.Errorf("plain text = %q, want %q", got, want)
	}
}

// treeOf maps each line of the tree to the line it's under ("" at the top) by
// their heads, and lists every line's key in order.
func treeOf(lines []statusLine) (parents map[string]string, keys []string) {
	parents = map[string]string{}
	var walk func(lines []statusLine, parent string)
	walk = func(lines []statusLine, parent string) {
		for _, l := range lines {
			parents[l.head] = parent
			keys = append(keys, l.key)
			walk(l.kids, l.head)
		}
	}
	walk(lines, "")
	return parents, keys
}

func TestReportLines(t *testing.T) {
	c := fullStatusLedger(t)
	parents, keys := treeOf(reportLines(readReport(c, "p")))
	drafts := "1 draft(s)  (crew list --project p --status draft)"
	for text, under := range map[string]string{
		"workers running: 1/3":    "",
		"p":                       "",
		"goal 001-launch":         "p",
		"in flight":               "goal 001-launch",
		"001-in-flight":           "in flight",
		"next, in dispatch order": "goal 001-launch",
		"1  002-next":             "next, in dispatch order",
		"… (2 more)":              "next, in dispatch order",
		"6  007-next":             "… (2 more)", // counted, not listed
		"7  008-next":             "… (2 more)",
		"parked":                  "goal 001-launch",
		"   009-parked":           "parked",
		"010-parent":              "p",
		"└ 011-child":             "010-parent",
		drafts:                    "p",
		"012-idea":                drafts, // counted, not listed
		"open proposals: 1  (crew proposals --project p)": "p",
		elsewhereHeading:                   "",
		"r: 1 task(s) with open questions": elsewhereHeading,
	} {
		if got, ok := parents[text]; !ok {
			t.Errorf("no line %q in the tree", text)
		} else if got != under {
			t.Errorf("%q is under %q, want %q", text, got, under)
		}
	}

	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("two lines have the key %q", k)
		}
		seen[k] = true
	}
	if _, again := treeOf(reportLines(readReport(c, "p"))); !slices.Equal(again, keys) {
		t.Errorf("building the report again changed the keys:\n%q\nwant:\n%q", again, keys)
	}
}
