package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reportTask writes a task file in a project of the ledger c is on, from front
// matter lines, with an open question when asks is true.
func reportTask(t *testing.T, c *Crew, project, name string, asks bool, fields ...string) Task {
	t.Helper()
	questions := ""
	if asks {
		questions = "- [ ] Which way?\n"
	}
	p := c.path("projects", project, "tasks", name+".md")
	content := "---\nid: " + name + "\n" + strings.Join(fields, "\n") + "\n---\n# " + name +
		"\n\n## Open questions\n" + questions + "\n## Log\n"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerFormat(c.path("projects", project)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

// writeFile writes a file in the home, making its directory.
func writeFile(t *testing.T, c *Crew, content string, path ...string) {
	t.Helper()
	p := c.path(path...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingOf(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	for _, tc := range []struct {
		name string
		task Task
		want waitingOnUser
	}{
		{"a task with an open question", reportTask(t, c, "p", "001-asks", true, "status: draft"), waitingOnUser{Questions: 1}},
		{"a pr-open task", reportTask(t, c, "p", "002-pr", false, "status: pr-open"), waitingOnUser{PRs: 1}},
		{"a finished task with an open question", reportTask(t, c, "p", "003-merged", true, "status: merged"), waitingOnUser{}},
		{"a working task", reportTask(t, c, "p", "004-working", false, "status: working"), waitingOnUser{}},
	} {
		if got := waitingOf(tc.task); got != tc.want {
			t.Errorf("%s: waitingOf = %+v, want %+v", tc.name, got, tc.want)
		}
		if got, want := waitsOnUser(tc.task), tc.want != (waitingOnUser{}); got != want {
			t.Errorf("%s: waitsOnUser = %v, want %v", tc.name, got, want)
		}
	}
}

func TestWaitingOnUserWording(t *testing.T) {
	for _, tc := range []struct {
		w    waitingOnUser
		want string
	}{
		{waitingOnUser{Questions: 5, PRs: 2, MemorySuggestions: 2}, "5 task(s) with open questions, 2 PR(s) to review, 2 memory suggestion(s)"},
		{waitingOnUser{PRs: 1}, "1 PR(s) to review"},
		{waitingOnUser{}, ""},
	} {
		if got := tc.w.String(); got != tc.want {
			t.Errorf("%+v words as %q, want %q", tc.w, got, tc.want)
		}
	}
}

// statusLedger is a home with two projects. p has a running goal with a task in
// flight, seven ready to go and one parked; outside the goal, a stack, a draft
// that folds and three open PRs, which GitHub can't be read for. q has a task
// with an open question, an open PR, a finished task with an open question and a
// memory suggestion.
func statusLedger(t *testing.T) *Crew {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	c := &Crew{Home: t.TempDir(), Host: "here", Settings: Settings{"MAX_WORKERS": "3", "GOAL_MAX_IN_FLIGHT": "2"}}
	writeFile(t, c, "here\n", "host")
	writeFile(t, c, `#!/bin/sh
echo '[{"id":"w1","state":"working","pid":1}]'
`, "bin", "claude")
	writeFile(t, c, "#!/bin/sh\nexit 1\n", "bin", "gh") // GitHub can't be read
	t.Setenv("PATH", c.path("bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, p := range []string{"p", "q"} {
		writeFile(t, c, "", "projects", p, "project.env")
	}
	writeFile(t, c, "---\nid: 001-launch\nstatus: running\n---\n# launch\n\n## Log\n", "projects", "p", "goals", "001-launch.md")
	goal := []string{"goal: 001-launch", "base: main"}
	reportTask(t, c, "p", "001-in-flight", false, slices.Concat(goal, []string{"status: working", "worker_job: w1"})...)
	for i := 2; i <= 8; i++ {
		reportTask(t, c, "p", fmt.Sprintf("%03d-next", i), false, slices.Concat(goal, []string{"status: ready"})...)
	}
	reportTask(t, c, "p", "009-parked", true, slices.Concat(goal, []string{"status: draft"})...)
	reportTask(t, c, "p", "010-parent", false, "status: working", "base: main", "branch: agent/parent")
	reportTask(t, c, "p", "011-child", true, "status: draft", "base: agent/parent", "branch: agent/child")
	reportTask(t, c, "p", "012-idea", false, "status: draft", "base: main")
	reportTask(t, c, "p", "013-risky", false, "status: pr-open", "risk: high", "pr: https://github.com/o/r/pull/9", "pr_state: open")
	reportTask(t, c, "p", "014-later", false, "status: pr-open", "risk: low", "pr: https://github.com/o/r/pull/12")
	reportTask(t, c, "p", "015-earlier", false, "status: pr-open", "risk: low", "pr: https://github.com/o/r/pull/5")

	reportTask(t, c, "q", "001-asks", true, "status: draft")
	reportTask(t, c, "q", "002-pr", false, "status: pr-open", "pr: https://github.com/o/q/pull/3")
	reportTask(t, c, "q", "003-merged", true, "status: merged")
	writeFile(t, c, "# Memory suggestions: q\n\n- [ ] **A** a. (lead, 2026-11-09)\n", "projects", "q", "memory-suggestions.md")
	return c
}

// readReport is the report crew status builds for the projects in scope, after
// asking GitHub for their PRs, listing the workers and reading the branches.
func readReport(c *Crew, inScope ...string) statusReport {
	return buildReport(c, inScope, false, statusReads{prs: c.readPRs(inScope), workers: c.listWorkers(), branches: c.readBranches(inScope)})
}

func rowIDs(rows []taskRow) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.Task.ID())
	}
	return ids
}

func TestBuildReportGroupsAGoalsTasks(t *testing.T) {
	r := readReport(statusLedger(t), "p")
	p := r.Projects[0]
	if p.Name != "p" || !p.InScope || len(p.Goals) != 1 {
		t.Fatalf("project p = %+v", p)
	}
	g := p.Goals[0]
	var kinds []groupKind
	for _, group := range g.Groups {
		kinds = append(kinds, group.Kind)
	}
	if want := []groupKind{groupInFlight, groupNext, groupParked}; !slices.Equal(kinds, want) {
		t.Fatalf("goal groups = %v, want %v", kinds, want)
	}
	inFlight, next := g.Groups[0].Rows, g.Groups[1].Rows
	if len(inFlight) != 1 || inFlight[0].Worker.ID != "w1" || inFlight[0].Number != 0 {
		t.Errorf("in flight = %+v, want 001-in-flight with its worker and no number", inFlight)
	}
	// The plain text lists only the first nextShown; the report has every one.
	if len(next) != 7 || next[5].Task.ID() != "007-next" || next[5].Number != 6 {
		t.Errorf("next = %v, want all seven, the sixth numbered 6", rowIDs(next))
	}
	if g.Total != 9 || g.Merged != 0 || g.InFlight != (slots{used: 1, limit: 2}) {
		t.Errorf("goal counts: %d of %d merged, %+v in flight", g.Merged, g.Total, g.InFlight)
	}
}

func TestBuildReportCountsOnlyTasksThatCanStillMerge(t *testing.T) {
	c := statusLedger(t)
	before := readReport(c, "p").Projects[0].Goals[0]
	goal := []string{"goal: 001-launch", "base: main"}
	reportTask(t, c, "p", "016-merged", false, slices.Concat(goal, []string{"status: merged"})...)
	reportTask(t, c, "p", "017-abandoned", false, slices.Concat(goal, []string{"status: abandoned"})...)
	reportTask(t, c, "p", "018-spike", false, slices.Concat(goal, []string{"status: done", "kind: spike"})...)
	g := readReport(c, "p").Projects[0].Goals[0]
	// Only the merged task adds to the counts, not the abandoned task or the spike.
	if g.Total != before.Total+1 || g.Merged != before.Merged+1 {
		t.Errorf("goal counts: %d of %d merged, want %d of %d", g.Merged, g.Total, before.Merged+1, before.Total+1)
	}
}

func TestBuildReportStacksAndFoldsTasksOutsideAGoal(t *testing.T) {
	p := readReport(statusLedger(t), "p").Projects[0]
	if got, want := rowIDs(p.Rows), []string{"010-parent", "012-idea", "013-risky", "014-later", "015-earlier"}; !slices.Equal(got, want) {
		t.Fatalf("rows outside the goal = %v, want %v", got, want)
	}
	parent, idea := p.Rows[0], p.Rows[1]
	if got := rowIDs(parent.Kids); !slices.Equal(got, []string{"011-child"}) {
		t.Errorf("010-parent's kids = %v, want 011-child", got)
	} else if notes := parent.Kids[0].Notes; !slices.Contains(notes, "1 open question(s)") {
		t.Errorf("011-child's notes = %q, want its open question", notes)
	}
	if parent.Folded || !idea.Folded {
		t.Errorf("folded: 010-parent %v, 012-idea %v; want only the draft folded", parent.Folded, idea.Folded)
	}
	if risky := p.Rows[2]; !slices.Equal(risky.Notes, []string{"high"}) || risky.Merge == nil || !slices.Contains(risky.Merge.InTheWay, "no security review yet") {
		t.Errorf("013-risky's notes = %q and merge check %+v, want its tier, and its security review in the way", risky.Notes, risky.Merge)
	}
	var review []string
	for _, task := range p.ToReview {
		review = append(review, task.ID())
	}
	if want := []string{"015-earlier", "014-later", "013-risky"}; !slices.Equal(review, want) {
		t.Errorf("to review = %v, want cheapest look first, then by PR number: %v", review, want)
	}
}

func TestBuildReportCountsOnlyWhatWaitsInOtherProjects(t *testing.T) {
	r := readReport(statusLedger(t), "p")
	if len(r.Projects) != 2 || r.Projects[1].Name != "q" {
		t.Fatalf("projects = %+v, want p then q", r.Projects)
	}
	q := r.Projects[1]
	if q.InScope || q.Rows != nil || q.Goals != nil || q.ToReview != nil {
		t.Errorf("q, out of scope = %+v", q)
	}
	if want := (waitingOnUser{Questions: 1, PRs: 1, MemorySuggestions: 1}); q.Waiting != want {
		t.Errorf("q waiting = %+v, want %+v", q.Waiting, want)
	}
	if r.ReadOnlyOn != "" || r.Workers != (slots{used: 1, limit: 3}) || r.NoneDispatch {
		t.Errorf("crew-wide: read-only on %q, workers %+v, none dispatch %v", r.ReadOnlyOn, r.Workers, r.NoneDispatch)
	}
}

// A report goes by the reads it's given: building it runs no gh, claude or git.
func TestBuildReportGoesByOneRoundOfReads(t *testing.T) {
	c := statusLedger(t)
	repo := stackRepo(t, "agent/parent", "agent/child")
	writeFile(t, c, "REPO="+repo+"\n", "projects", "p", "project.env") // so gh and git could be run for p's tasks
	reportTask(t, c, "p", "011-child", false, "status: working", "base: agent/parent", "branch: agent/child")
	workers, branches := c.listWorkers(), c.readBranches([]string{"p"})
	calls := c.path("calls")
	for _, cmd := range []string{"gh", "claude", "git"} {
		writeFile(t, c, "#!/bin/sh\necho "+cmd+" \"$@\" >>"+calls+"\nexit 1\n", "bin", cmd)
	}
	// GitHub didn't answer for 013-risky's PR, answered for 014-later's, and
	// wasn't asked for 015-earlier's.
	prs := prReads{"p": {
		"https://github.com/o/r/pull/9":  {err: errors.New("gh: no answer")},
		"https://github.com/o/r/pull/12": {pr: gitHubPR{State: "OPEN", Base: "main", CI: ciPassing, MergeState: "CLEAN"}},
	}}
	for build := 1; build <= 2; build++ {
		checks := map[string]string{}
		for _, row := range buildReport(c, []string{"p"}, false, statusReads{prs: prs, workers: workers, branches: branches}).Projects[0].Rows {
			if row.Merge != nil {
				checks[row.Task.ID()] = row.Merge.String()
			}
		}
		if got := checks["013-risky"]; !strings.HasSuffix(got, "(couldn't read GitHub; as of the last sync)") {
			t.Errorf("build %d: 013-risky's merge check = %q, want it to go by the last sync", build, got)
		}
		if got := checks["014-later"]; got == "" || strings.Contains(got, "GitHub") {
			t.Errorf("build %d: 014-later's merge check = %q, want it to go by GitHub's answer", build, got)
		}
		if got := checks["015-earlier"]; got != "not yet: PR not read on GitHub yet" {
			t.Errorf("build %d: 015-earlier's merge check = %q, want it not read yet", build, got)
		}
	}
	if calls, err := os.ReadFile(calls); err == nil {
		t.Errorf("building the report ran:\n%s", calls)
	}
}

func TestReadPRsKeepsEachProjectsPRsApart(t *testing.T) {
	c := statusLedger(t)
	// p's and q's tasks both name PR 12, each in its own repository: open in
	// p's, merged in q's.
	for project, state := range map[string]string{"p": "OPEN", "q": "MERGED"} {
		repo := t.TempDir()
		writeFile(t, c, "REPO="+repo+"\n", "projects", project, "project.env")
		writeFile(t, c, `{"state":"`+state+`","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}`, filepath.Base(repo)+".json")
		reportTask(t, c, project, "020-twelve", false, "status: pr-open", "base: main", "pr: 12")
	}
	writeFile(t, c, "#!/bin/sh\ncat "+c.Home+"/$(basename \"$PWD\").json\n", "bin", "gh")
	r := readReport(c, "p", "q")
	for i, wantClosed := range []string{"", "merged"} {
		for _, row := range r.Projects[i].Rows {
			if row.Task.ID() == "020-twelve" && row.Merge.Closed != wantClosed {
				t.Errorf("%s's PR 12: closed = %q, want %q", r.Projects[i].Name, row.Merge.Closed, wantClosed)
			}
		}
	}
}

// goalLedger is a home with one project, p, and a running goal, 002-boards, for
// tests of how status lists a goal's tasks. Each task is "NNN status [key:
// value…]", in goal 002-boards and based on main unless it says otherwise.
func goalLedger(t *testing.T, tasks ...string) *Crew {
	t.Helper()
	c := &Crew{Home: t.TempDir(), Host: "here", Settings: Settings{"MAX_WORKERS": "3", "GOAL_MAX_IN_FLIGHT": "3"}}
	writeFile(t, c, "here\n", "host")
	writeFile(t, c, "", "projects", "p", "project.env")
	writeFile(t, c, "---\nid: 002-boards\nstatus: running\n---\n# boards\n\n## Log\n", "projects", "p", "goals", "002-boards.md")
	for _, task := range tasks {
		id, rest, _ := strings.Cut(task, " ")
		status, keys, _ := strings.Cut(rest, " ")
		fields := []string{"project: p", "goal: 002-boards", "status: " + status}
		for _, kv := range strings.Split(keys, ";") {
			if kv = strings.TrimSpace(kv); kv != "" {
				fields = append(fields, kv)
			}
		}
		if !strings.Contains(keys, "base: ") {
			fields = append(fields, "base: main")
		}
		reportTask(t, c, "p", id, false, fields...)
	}
	return c
}

// boardsGoal is shaped like a real goal whose 12 tasks wait on each other in a
// single line, 160 → 259 → 195 → [235 → 165 → 236 → 170] → 242 → 166 → [148 →
// 243 → 244], brackets being stacks. Some tasks also name a task further down
// the line in depends_on, as 166 names 170.
var boardsGoal = []string{
	"160 pr-open branch: b160",
	"259 working depends_on: 160; priority: 3",
	"195 ready depends_on: 259; priority: 2",
	"235 ready base: b160; branch: b235; depends_on: 160, 195",
	"165 ready base: b235; branch: b165; depends_on: 235, 160; priority: 1",
	"236 ready base: b165; branch: b236; depends_on: 165, 160",
	"170 ready base: b236; branch: b170; depends_on: 236; priority: 1",
	"242 ready depends_on: 170",
	"166 ready depends_on: 170, 242",
	"148 ready branch: b148; depends_on: 166",
	"243 ready base: b148; branch: b243; depends_on: 148, 166",
	"244 ready base: b243; branch: b244; depends_on: 243, 166",
}

// goalReportOf is the report of p's one goal, built without reading GitHub,
// the workers or the branches.
func goalReportOf(c *Crew) goalReport {
	return buildReport(c, []string{"p"}, false, statusReads{}).Projects[0].Goals[0]
}

// groupOf is the goal's group of that kind, or nil.
func groupOf(g goalReport, k groupKind) []taskRow {
	for _, group := range g.Groups {
		if group.Kind == k {
			return group.Rows
		}
	}
	return nil
}

// rowOf is the row of the task with that ID, wherever it is in the goal.
func rowOf(t *testing.T, g goalReport, id string) taskRow {
	t.Helper()
	var find func(rows []taskRow) (taskRow, bool)
	find = func(rows []taskRow) (taskRow, bool) {
		for _, r := range rows {
			if r.Task.ID() == id {
				return r, true
			}
			if k, ok := find(r.Kids); ok {
				return k, true
			}
		}
		return taskRow{}, false
	}
	for _, group := range g.Groups {
		if r, ok := find(group.Rows); ok {
			return r
		}
	}
	t.Fatalf("no row for %s", id)
	return taskRow{}
}

// afterNote is the "after" note on a task's row, or "".
func afterNote(t *testing.T, g goalReport, id string) string {
	t.Helper()
	for _, n := range rowOf(t, g, id).Notes {
		if strings.HasPrefix(n, "after ") {
			return n
		}
	}
	return ""
}

// waitingInText are the tasks plain crew status lists under the goal's
// "waiting on other tasks", in order, stacked ones included.
func waitingInText(t *testing.T, c *Crew) []string {
	t.Helper()
	text := plainText(buildReport(c, []string{"p"}, false, statusReads{}))
	_, waiting, ok := strings.Cut(text, "    waiting on other tasks\n")
	if !ok {
		t.Fatalf("no waiting group in:\n%s", text)
	}
	var ids []string
	for _, l := range strings.Split(waiting, "\n") {
		if !strings.HasPrefix(l, "      ") {
			break
		}
		ids = append(ids, strings.Fields(strings.TrimLeft(l, " └"))[0])
	}
	return ids
}

func TestGoalListsWaitingTasksInRunOrder(t *testing.T) {
	c := goalLedger(t, boardsGoal...)
	if got, want := rowIDs(groupOf(goalReportOf(c), groupWaiting)), []string{"195", "235", "242", "166", "148"}; !slices.Equal(got, want) {
		t.Errorf("waiting rows = %v, want %v", got, want)
	}
	want := []string{"195", "235", "165", "236", "170", "242", "166", "148", "243", "244"}
	if got := waitingInText(t, c); !slices.Equal(got, want) {
		t.Errorf("plain text lists the waiting tasks as %v, want %v", got, want)
	}
}

func TestGoalListsWaitingTasksByDependencyThenPriority(t *testing.T) {
	g := goalReportOf(goalLedger(t,
		"w working",
		"a ready depends_on: w",
		"b ready depends_on: a; priority: 5",
		"c ready depends_on: w; priority: 3",
	))
	if got, want := rowIDs(groupOf(g, groupWaiting)), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Errorf("waiting = %v, want %v", got, want)
	}
}

func TestGoalListsFirstOfTwoRowsTheOneWhoseTopTaskTheOtherWaitsOn(t *testing.T) {
	// b waits on a, and a2, stacked on a, waits on b: the rows wait on each
	// other, but only a2 makes a's row wait.
	g := goalReportOf(goalLedger(t,
		"w working",
		"a ready depends_on: w; branch: ba",
		"b ready depends_on: a; priority: 5",
		"a2 ready base: ba; depends_on: b",
	))
	if got, want := rowIDs(groupOf(g, groupWaiting)), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("waiting = %v, want %v", got, want)
	}
}

func TestGoalListsWaitingTasksInALoopOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks []string
		want  []string
	}{
		{"tasks that wait for each other", []string{
			"w working",
			"a ready depends_on: b",
			"b ready depends_on: a",
			"c ready depends_on: a",
		}, []string{"a", "b", "c"}},
		// p is in flight, so k1 and k2, stacked on it, are listed on their own,
		// each with its own stack; k1's stack waits for k2, and k2's for k1.
		{"two branches of a forked stack", []string{
			"w working",
			"p working branch: bp",
			"k1 ready base: bp; branch: b1; depends_on: w",
			"k2 ready base: bp; branch: b2; depends_on: w",
			"k1a draft base: b1; branch: b1a; depends_on: k2",
			"k2a draft base: b2; branch: b2a; depends_on: k1",
			"x ready depends_on: k2a",
		}, []string{"k1", "k1a", "k2", "k2a", "x"}},
	} {
		c := goalLedger(t, tc.tasks...)
		if got := waitingInText(t, c); !slices.Equal(got, tc.want) {
			t.Errorf("%s: waiting = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGoalAfterNotesNameOnlyWhatEachTaskWaitsOnNext(t *testing.T) {
	g := goalReportOf(goalLedger(t, boardsGoal...))
	for id, want := range map[string]string{
		"195": "after 259",
		"235": "after 195",
		// 235 can be dispatched once 160's branch is pushed, but 165 waits for 160
		// to merge.
		"165": "after 160",
		// 165, the stack parent, can't be dispatched until 160 has merged.
		"236": "",
		"170": "",
		"242": "after 170",
		// 242 can't be dispatched until 170 has merged.
		"166": "after 242",
		"148": "after 166",
		// 148, then 243, the stack parents, can't be dispatched until 166 has
		// merged.
		"243": "",
		"244": "",
	} {
		if got := afterNote(t, g, id); got != want {
			t.Errorf("%s's note = %q, want %q", id, got, want)
		}
	}
}

func TestGoalAfterNoteNamesWhatsStillToMergeOnceAParentIsInFlight(t *testing.T) {
	tasks := slices.Clone(boardsGoal)
	tasks[1] = "259 merged depends_on: 160"
	tasks[2] = "195 merged depends_on: 259"
	tasks[3] = "235 working base: b160; branch: b235; depends_on: 160, 195"
	if got := afterNote(t, goalReportOf(goalLedger(t, tasks...)), "165"); got != "after 160" {
		t.Errorf("165's note = %q, want %q", got, "after 160")
	}
}

func TestGoalAfterNoteAlwaysNamesAnAbandonedTask(t *testing.T) {
	tasks := slices.Clone(boardsGoal)
	tasks[7] = "242 abandoned depends_on: 170"
	if got, want := afterNote(t, goalReportOf(goalLedger(t, tasks...)), "166"), "after 170, 242 (abandoned)"; got != want {
		t.Errorf("166's note = %q, want %q", got, want)
	}
}

func TestGoalAfterNoteLeavesOutWhatItWaitsOnThroughATaskItDoesntName(t *testing.T) {
	g := goalReportOf(goalLedger(t,
		"w working",
		"a ready depends_on: w",
		"x ready depends_on: a",
		"b ready depends_on: x",
		"c ready depends_on: a, b",
	))
	if got, want := afterNote(t, g, "c"), "after b"; got != want {
		t.Errorf("c's note = %q, want %q", got, want)
	}
}

func TestGoalAfterNoteNamesTasksThatWaitOnEachOther(t *testing.T) {
	g := goalReportOf(goalLedger(t,
		"a ready depends_on: b",
		"b ready depends_on: a",
		"c ready depends_on: a, b",
	))
	if got, want := afterNote(t, g, "c"), "after a, b"; got != want {
		t.Errorf("c's note = %q, want %q", got, want)
	}
}
