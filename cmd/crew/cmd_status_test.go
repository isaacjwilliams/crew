package main

import (
	"cmp"
	"errors"
	"regexp"
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
	reportTask(t, c, "p", "010-parent", false, "status: working", "branch: agent/parent", "model: sonnet", "effort: high")
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

func TestPrintReportLeavesFoldedRowsOutOfItsColumns(t *testing.T) {
	c := fullStatusLedger(t)
	reportTask(t, c, "p", "016-an-idea-with-a-longer-name-than-any-row-printed", false, "status: draft")
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

// everyNoteLedger is a home whose project p has a row for every note crew
// status can show, each under a task named for it: a pr-open task for each
// reason its merge check gives, a task of another status for each note about
// its PR, branches or questions, ready tasks for each note on what they wait
// for, drafts that are still learning, and goals that are full or whose cap
// isn't a number. Its reads are GitHub's answer for the pr-open PRs and the
// workers as listed, with two stacked tasks' branches not read yet.
func everyNoteLedger(t *testing.T) (*Crew, statusReads) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repo := stackRepo(t, "agent/merged-parent", "agent/open-parent", "agent/108", "agent/207", "agent/206", "release", "agent/old")
	c := &Crew{Home: t.TempDir(), Host: "here", Settings: Settings{"MAX_WORKERS": "9", "GOAL_MAX_IN_FLIGHT": "3", "AUTO_REVIEWS": "2", "DEFAULT_BRANCH": "main"}}
	writeFile(t, c, "here\n", "host")
	writeFile(t, c, "REPO="+repo+"\n", "projects", "p", "project.env")
	if err := writeLedgerFormat(c.path("projects", "p")); err != nil {
		t.Fatal(err)
	}
	// task writes a task in p, based on main, from front matter lines that
	// add to those or replace them; one with no value takes its key out.
	task := func(name, body string, fields ...string) {
		t.Helper()
		front := []string{"id: " + name, "project: p", "base: main"}
		for _, f := range fields {
			key, value, _ := strings.Cut(f, ":")
			front = slices.DeleteFunc(front, func(g string) bool { return strings.HasPrefix(g, key+":") })
			if strings.TrimSpace(value) != "" {
				front = append(front, f)
			}
		}
		writeFile(t, c, "---\n"+strings.Join(front, "\n")+"\n---\n# "+name+"\n\n"+body+"## Log\n", "projects", "p", "tasks", name+".md")
	}
	reviewed := "## Automated reviews\n### Automated review 1 — 2026-11-20 10:00\n- Reviewers: Claude reviewer\nNo findings.\n\n"
	leftForUser := "## Automated reviews\n### Automated review 1 — 2026-11-20 10:00\n- Reviewers: Claude reviewer\n" +
		"#### 1. [should fix] f1\n- Agreed severity: should fix\n- Outcome: left for the user\n" +
		"#### 2. [should fix] f2\n- Agreed severity: should fix\n- Outcome: left for the user\n\n"
	asks := "## Open questions\n- [ ] Which way?\n\n"
	secured := "## Evidence\n### Security review\nNothing found.\n\n"
	prs := map[string]prRead{}
	// prURL is the URL of the PR numbered as the task is.
	prURL := func(name string) string { return "https://github.com/o/r/pull/" + name[:3] }
	// prOpen writes a pr-open task, reviewed unless body says otherwise, whose
	// PR GitHub answers as clean and passing, changed by pr.
	prOpen := func(name, body string, pr func(*gitHubPR), fields ...string) {
		t.Helper()
		url := prURL(name)
		read := gitHubPR{State: "OPEN", Base: "main", CI: ciPassing, MergeState: "CLEAN"}
		if pr != nil {
			pr(&read)
		}
		prs[url] = prRead{pr: read}
		task(name, cmp.Or(body, reviewed), slices.Concat([]string{"status: pr-open", "risk: low", "branch: agent/" + name[:3], "pr: " + url, "pr_state: open"}, fields)...)
	}
	// unread writes a pr-open task whose PR GitHub didn't answer for.
	unread := func(name string, fields ...string) {
		t.Helper()
		prOpen(name, "", nil, fields...)
		prs[prURL(name)] = prRead{err: errors.New("gh: no answer")}
	}

	// The tasks others wait on, stack on or name.
	task("090-merged-parent", "", "status: merged", "branch: agent/merged-parent")
	task("091-open-parent", reviewed, "status: pr-open", "risk: low", "branch: agent/open-parent", "pr: https://github.com/o/r/pull/91", "pr_state: open")
	prs["https://github.com/o/r/pull/91"] = prRead{pr: gitHubPR{State: "OPEN", Base: "main", CI: ciPassing, MergeState: "CLEAN"}}
	task("092-abandoned", "", "status: abandoned")

	// A pr-open task for each thing its merge check says.
	prOpen("101-merge-now", "", nil)
	prOpen("102-findings-left", leftForUser, nil, "risk: medium")
	prOpen("103-worker-busy", "", nil, "worker_job: busy")
	prOpen("104-round-not-sent", "", nil, "review_rounds: 2", "last_round_sent: 1")
	prOpen("105-checking-conflicts", "", func(p *gitHubPR) { p.Mergeable = mergeableUnknown })
	prOpen("106-ci-running", "", func(p *gitHubPR) { p.CI = ciRunning })
	prOpen("107-not-reviewed", "\n", nil)
	prOpen("108-restack", "", func(p *gitHubPR) { p.Base = "agent/merged-parent" }, "base: agent/merged-parent")
	prOpen("109-retarget", "", func(p *gitHubPR) { p.Base = "agent/old" })
	prOpen("110-stacked", "", func(p *gitHubPR) { p.Base = "agent/open-parent" }, "base: agent/open-parent")
	prOpen("111-based-elsewhere", "", func(p *gitHubPR) { p.Base = "release" }, "base: release")
	prOpen("112-conflicts", "", func(p *gitHubPR) { p.Mergeable = conflicting })
	prOpen("113-ci-failing", "", func(p *gitHubPR) { p.CI = ciFailing })
	prOpen("114-draft", "", func(p *gitHubPR) { p.Draft = true })
	prOpen("115-no-security-review", "", nil, "risk: high")
	prOpen("116-behind", "", func(p *gitHubPR) { p.MergeState = "BEHIND" })
	prOpen("117-ci-cancelled", "", func(p *gitHubPR) { p.CI = ciCancelled })
	prOpen("118-changes-requested", "", func(p *gitHubPR) { p.ReviewDecision = "CHANGES_REQUESTED" })
	prOpen("119-review-required", "", func(p *gitHubPR) { p.ReviewDecision, p.MergeState = "REVIEW_REQUIRED", "BLOCKED" })
	prOpen("120-asks", asks+reviewed, nil)
	prOpen("121-blocked", "", func(p *gitHubPR) { p.MergeState = "BLOCKED" })
	prOpen("122-merged", "", func(p *gitHubPR) { p.State = "MERGED" })
	prOpen("123-closed", "", func(p *gitHubPR) { p.State = "CLOSED" })
	unread("124-last-sync", "pr_state: open, changes requested", "ci_state: failing")
	unread("125-never-synced", "pr_state:")
	task("126-not-asked", reviewed, "status: pr-open", "risk: low", "pr: https://github.com/o/r/pull/126", "pr_state: open")
	task("127-no-pr", reviewed, "status: pr-open", "risk: low")
	prOpen("128-branches-unread", "", func(p *gitHubPR) { p.Base = "agent/open-parent" }, "base: agent/open-parent")
	prOpen("129-no-risk-tier", "", nil, "risk:")
	prOpen("130-secured", secured+reviewed, nil, "risk: high")

	// Tasks of other statuses, with a PR crew sync recorded, a branch to
	// move, or a question.
	task("201-changes-requested", "", "status: working", "pr: https://github.com/o/r/pull/201", "pr_state: open, changes requested", "ci_state: failing")
	task("202-draft", "", "status: working", "pr: https://github.com/o/r/pull/202", "pr_state: draft", "ci_state: running", "mergeable: conflicting", "merge_state: behind")
	task("203-closed", "", "status: needs-input", "pr: https://github.com/o/r/pull/203", "pr_state: closed")
	task("204-approved", "", "status: addressing-feedback", "pr: https://github.com/o/r/pull/204", "pr_state: open, approved", "ci_state: passing", "merge_state: blocked")
	task("205-review-required", "", "status: addressing-feedback", "pr: https://github.com/o/r/pull/205", "pr_state: open, review required", "ci_state: cancelled", "mergeable: unknown")
	task("206-no-pr-yet", "", "status: working", "branch: agent/206")
	task("207-restack", "", "status: working", "base: agent/merged-parent", "branch: agent/207")
	task("208-retarget", "", "status: working", "pr: https://github.com/o/r/pull/208", "pr_state: open", "pr_base: agent/old")
	task("209-branches-unread", "", "status: working", "base: agent/open-parent", "branch: agent/209")
	task("210-asks", asks, "status: needs-input")
	task("211-interrupted", "", "status: working", "interrupted: 2026-11-27T10:00:00Z")
	task("212-review-off", "", "status: working", "auto_review: no")
	task("213-merged-pr", "", "status: addressing-feedback", "pr: https://github.com/o/r/pull/213", "pr_state: merged")

	// Drafts that are still learning, and the spikes they learn from.
	task("220-learning", "", "status: draft")
	learning := Task{Doc{c.path("projects", "p", "tasks", "220-learning.md")}}
	add(t, learning, Record{ToLearn: &ToLearn{"Does it page?"}})
	add(t, learning, Record{ToLearn: &ToLearn{"How fast?"}})
	task("221-spike", "", "status: working", "kind: spike", "informs: 220-learning")
	task("222-spike-done", "", "status: done", "kind: spike", "informs: 220-learning")

	// Ready tasks outside a goal, and what they wait for.
	task("230-by-hand", "", "status: ready")
	task("231-bad-go-ahead", "", "status: ready", "go_ahead: nothing")
	task("232-go-ahead-lost", "", "status: ready", "go_ahead: 092-abandoned merged")
	task("233-go-ahead-met", "", "status: ready", "go_ahead: 090-merged-parent merged", "priority: 2")
	task("234-go-ahead-once", "", "status: ready", "go_ahead: 206-no-pr-yet pr-open", "priority: x")
	task("235-bad-dependency", "", "status: ready", "depends_on: 999")
	task("236-after", "", "status: ready", "depends_on: 206, 211")
	task("237-after-abandoned", "", "status: ready", "depends_on: 092")
	task("238-after-many", "", "status: ready", "depends_on: 092, 201, 202, 206, 211")
	task("239-on-parent", "", "status: ready", "base: agent/206")

	// Goals: one full, one whose cap isn't a number, and two finished ones
	// whose caps still hold their ready tasks.
	goal := func(name, status, maxInFlight string) {
		writeFile(t, c, "---\nid: "+name+"\nstatus: "+status+"\nmax_in_flight: "+maxInFlight+"\n---\n# "+name+"\n\n## Log\n", "projects", "p", "goals", name+".md")
	}
	goal("001-full", "running", "1")
	task("301-in-flight", "", "status: working", "goal: 001-full")
	task("302-next", "", "status: ready", "goal: 001-full", "priority: 1")
	goal("002-bad-cap", "running", "x")
	task("303-next", "", "status: ready", "goal: 002-bad-cap")
	goal("003-done-full", "done", "1")
	task("311-in-flight", "", "status: working", "goal: 003-done-full")
	task("312-goal-full", "", "status: ready", "goal: 003-done-full")
	goal("004-done-bad-cap", "done", "y")
	task("313-goal-bad-cap", "", "status: ready", "goal: 004-done-bad-cap")

	branches := c.readBranches([]string{"p"})
	for _, name := range []string{"128-branches-unread", "209-branches-unread"} {
		delete(branches, c.path("projects", "p", "tasks", name+".md"))
	}
	workers := &workerList{agents: Agents{{ID: "busy", State: "working", PID: true}}}
	return c, statusReads{prs: prReads{"p": prs}, workers: workers, branches: branches}
}

// everyNoteText is what crew status prints for everyNoteLedger.
const everyNoteText = `workers running: 1/9

p
  task                     status               worker   PR    model  effort  notes
  goal 001-full            running                                            0 of 2 task(s) merged, 1/1 in flight: full
    in flight
      301-in-flight        working
    next, in dispatch order
      1  302-next          ready                                              priority 1
  goal 002-bad-cap         running                                            0 of 1 task(s) merged, 0/? in flight  goal 002-bad-cap: max_in_flight must be a whole number, not 'x' (set it with 'crew goal cap')
    next, in dispatch order
      1  303-next          ready
  091-open-parent          pr-open                       #91                  merge now  low: approve from the evidence
    └ 110-stacked          pr-open                       #110                 not yet: stacked on 091  low: approve from the evidence
    └ 128-branches-unread  pr-open                       #128                 not yet: branches not read yet; stacked on 091  low: approve from the evidence
    └ 209-branches-unread  working                                            branches not read yet
  101-merge-now            pr-open                       #101                 merge now  low: approve from the evidence
  102-findings-left        pr-open                       #102                 merge now, 2 finding(s) left for you  medium: read the diff
  103-worker-busy          pr-open              working  #103                 not yet: worker busy  low: approve from the evidence
  104-round-not-sent       pr-open                       #104                 not yet: round 2 not sent yet  low: approve from the evidence
  105-checking-conflicts   pr-open                       #105                 not yet: GitHub still checking for conflicts  low: approve from the evidence
  106-ci-running           pr-open                       #106                 not yet: CI running  low: approve from the evidence
  107-not-reviewed         pr-open                       #107                 not yet: no automated review yet  low: approve from the evidence
  108-restack              pr-open                       #108                 not yet: needs restack: 090-merged-parent merged; PR targets agent/merged-parent, not main  low: approve from the evidence
  109-retarget             pr-open                       #109                 not yet: PR targets agent/old, not main  low: approve from the evidence
  111-based-elsewhere      pr-open                       #111                 not yet: based on release, not main  low: approve from the evidence
  112-conflicts            pr-open                       #112                 not yet: conflicts with main  low: approve from the evidence
  113-ci-failing           pr-open                       #113                 not yet: CI failing  low: approve from the evidence
  114-draft                pr-open                       #114                 not yet: draft  low: approve from the evidence
  115-no-security-review   pr-open                       #115                 not yet: no security review yet  high
  116-behind               pr-open                       #116                 not yet: behind main  low: approve from the evidence
  117-ci-cancelled         pr-open                       #117                 not yet: CI cancelled  low: approve from the evidence
  118-changes-requested    pr-open                       #118                 not yet: changes requested  low: approve from the evidence
  119-review-required      pr-open                       #119                 not yet: review required  low: approve from the evidence
  120-asks                 pr-open                       #120                 not yet: 1 open question(s)  low: approve from the evidence
  121-blocked              pr-open                       #121                 not yet: blocked by a branch rule on GitHub  low: approve from the evidence
  122-merged               pr-open                       #122                 merged on GitHub  low: approve from the evidence
  123-closed               pr-open                       #123                 closed on GitHub  low: approve from the evidence
  124-last-sync            pr-open                       #124                 not yet: CI failing; changes requested (couldn't read GitHub; as of the last sync)  low: approve from the evidence
  125-never-synced         pr-open                       #125                 not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet  low: approve from the evidence
  126-not-asked            pr-open                       #126                 not yet: PR not read on GitHub yet  low: approve from the evidence
  127-no-pr                pr-open                                            not yet: no PR  low: approve from the evidence
  129-no-risk-tier         pr-open                       #129                 merge now  no risk tier: read the diff
  130-secured              pr-open                       #130                 merge now  high: read the diff; security review ran
  201-changes-requested    working                       #201                 (open, changes requested)
  202-draft                working                       #202                 (draft)
  203-closed               needs-input                   #203                 (closed)
  204-approved             addressing-feedback           #204                 (open, approved)
  205-review-required      addressing-feedback           #205                 (open, review required)
  206-no-pr-yet            working
  207-restack              working                                            needs restack: 090-merged-parent merged
  208-retarget             working                       #208                 (open)  PR targets agent/old, not main
  210-asks                 needs-input                                        1 open question(s)
  211-interrupted          working                                            interrupted by a pause
  212-review-off           working                                            automated review off
  213-merged-pr            addressing-feedback           #213                 (merged)
  220-learning             draft                                              2 to learn; 221 working, 222 done
  221-spike                working                                            informs 220
  230-by-hand              ready                                              dispatch by hand
  231-bad-go-ahead         ready                                              go_ahead 'nothing' isn't a task in this project and a status  dispatch by hand
  232-go-ahead-lost        ready                                              go-ahead never met: 092 abandoned  dispatch by hand
  233-go-ahead-met         ready                                              priority 2  go-ahead met: 090 reached merged
  234-go-ahead-once        ready                                              priority ?  go-ahead once 206 is pr-open
  235-bad-dependency       ready                                              235-bad-dependency depends on '999', which doesn't match exactly one task in p  dispatch by hand
  236-after                ready                                              after 206, 211  dispatch by hand
  237-after-abandoned      ready                                              after 092 (abandoned)  dispatch by hand
  238-after-many           ready                                              after 5 tasks, 092 abandoned  dispatch by hand
  239-on-parent            ready                                              on 206  dispatch by hand
  311-in-flight            working
  312-goal-full            ready                                              goal 003-done-full full  dispatch by hand
  313-goal-bad-cap         ready                                              goal 004-done-bad-cap: max_in_flight must be a whole number, not 'y' (set it with 'crew goal cap')  dispatch by hand
  to review: 127 low, #91 low, #101 low, #103 low, #104 low, #105 low, #106 low, #107 low, #108 low, #109 low, #110 low, #111 low, #112 low, #113 low, #114 low, #116 low, #117 low, #118 low, #119 low, #120 low, #121 low, #122 low, #123 low, #124 low, #125 low, #126 low, #128 low, #102 medium, #115 high (no security review yet), #130 high, #129 no risk tier
`

func TestPrintReportWordsEveryNote(t *testing.T) {
	c, reads := everyNoteLedger(t)
	if got := plainText(buildReport(c, []string{"p"}, false, reads)); got != everyNoteText {
		t.Errorf("plain text:\n%s\nwant:\n%s", got, everyNoteText)
	}
	// Why the workers can't be seen is the same for every row of a report, so
	// a report for each says it on 101's row.
	readOnly := *c
	readOnly.Host = "laptop"
	for _, tc := range []struct {
		c       *Crew
		workers *workerList
		want    string
	}{
		{c, nil, "not yet: workers not listed yet"},
		{c, &workerList{err: errors.New("claude: no answer")}, "not yet: workers couldn't be listed"},
		{&readOnly, reads.workers, "not yet: worker can't be seen from this machine"},
	} {
		unseen := reads
		unseen.workers = tc.workers
		want := "  101-merge-now  pr-open  #101  " + tc.want + "  low: approve from the evidence"
		if got := rowText(t, buildReport(tc.c, []string{"p"}, false, unseen), "101-merge-now"); got != want {
			t.Errorf("101's row = %q, want %q", got, want)
		}
	}
}

// rowText is the task's row as the plain text prints it, with its runs of
// spaces made two.
func rowText(t *testing.T, r statusReport, id string) string {
	t.Helper()
	for _, line := range strings.Split(plainText(r), "\n") {
		if strings.Contains(line, " "+id+" ") {
			return regexp.MustCompile(`   +`).ReplaceAllString(line, "  ")
		}
	}
	t.Fatalf("no row for %s", id)
	return ""
}
