package main

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A pr-open task with nothing in its PR's way: synced, reviewed, on main, with a
// clean PR whose checks pass.
var (
	clearTask = map[string]string{
		"status": "pr-open", "risk": "low", "base": "main", "branch": "agent/task",
		"pr": "https://github.com/o/r/pull/7", "pr_state": "open",
	}
	clearBody = "## Open questions\n\n## Evidence\n\n## Automated reviews\n" + reviewed + "## Log\n"
	reviewed  = "### Automated review 1 — 2026-11-20 10:00\n- Reviewers: Claude reviewer\nNo findings.\n\n"
	clearPR   = gitHubPR{State: "OPEN", Base: "main", CI: ciPassing, Mergeable: "MERGEABLE"}
)

// mergeTask writes a task file in project p of a ledger at dir, from its front
// matter and its body.
func mergeTask(t *testing.T, dir, name string, fields map[string]string, body string) Task {
	t.Helper()
	front := "id: " + name + "\n"
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		front += k + ": " + fields[k] + "\n"
	}
	p := filepath.Join(dir, "projects", "p", "tasks", name+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\n"+front+"---\n# "+name+"\n\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

func TestMergeCheck(t *testing.T) {
	repo := stackRepo(t, "agent/task", "agent/parent")
	leftForUser := "### Automated review 2 — 2026-11-20 11:00\n- Reviewers: Claude reviewer\n" +
		"#### [should fix] a\n- Agreed severity: should fix\n- Outcome: left for the user: its fix would cross a bound\n" +
		"#### [fix later] b\n- Agreed severity: fix later\n- Outcome: left for the user: a day's work\n" +
		"#### [nitpick] c\n- Agreed severity: nitpick\n- Outcome: fixed in abc1234\n\n"
	for _, tc := range []struct {
		name     string
		fields   map[string]string // over clearTask's; an empty value removes the key
		body     string            // in place of clearBody
		pr       func(*gitHubPR)   // changes clearPR
		unread   bool              // GitHub couldn't be read
		worker   Agent
		readOnly bool
		settings map[string]string // over AUTO_REVIEWS=2, DEFAULT_BRANCH=main
		parent   string            // the status of 020-parent, on branch agent/parent
		want     string
	}{
		{name: "nothing in the way", want: "merge now"},
		{name: "worker busy", worker: Agent{State: "working"}, want: "not yet: worker busy"},
		{name: "review round not sent", fields: map[string]string{"review_rounds": "2", "last_round_sent": "1"}, want: "not yet: review round not sent yet"},
		{name: "GitHub still checking for conflicts", pr: func(p *gitHubPR) { p.Mergeable = "UNKNOWN" }, want: "not yet: GitHub still checking for conflicts"},
		{name: "CI running", pr: func(p *gitHubPR) { p.CI = ciRunning }, want: "not yet: CI running"},
		{name: "automated review due", body: "## Log\n", want: "not yet: no automated review yet"},
		{name: "automated review off for the task", fields: map[string]string{"auto_review": "no"}, body: "## Log\n", want: "merge now"},
		{name: "automated review off for the project", settings: map[string]string{"AUTO_REVIEWS": "0"}, body: "## Log\n", want: "merge now"},
		{name: "parent merged, still the base", fields: map[string]string{"base": "agent/parent"}, parent: "merged", want: "not yet: needs restack: 020-parent merged"},
		{name: "PR targets another branch", pr: func(p *gitHubPR) { p.Base = "agent/old" }, want: "not yet: PR targets agent/old, not main"},
		{name: "stacked on an unmerged task", fields: map[string]string{"base": "agent/parent"}, parent: "pr-open", pr: func(p *gitHubPR) { p.Base = "agent/parent" }, want: "not yet: stacked on 020"},
		{name: "based on another branch", fields: map[string]string{"base": "release"}, pr: func(p *gitHubPR) { p.Base = "release" }, want: "not yet: based on release, not main"},
		{name: "conflicts", pr: func(p *gitHubPR) { p.Mergeable = "CONFLICTING" }, want: "not yet: conflicts with main"},
		{name: "CI failing", pr: func(p *gitHubPR) { p.CI = ciFailing }, want: "not yet: CI failing"},
		{name: "no checks", pr: func(p *gitHubPR) { p.CI = "" }, want: "merge now"},
		{name: "draft", pr: func(p *gitHubPR) { p.Draft = true }, want: "not yet: draft"},
		{name: "high risk without its security review", fields: map[string]string{"risk": "high"}, want: "not yet: no security review yet"},
		{name: "high risk with its security review", fields: map[string]string{"risk": "high"}, body: "## Evidence\n### Security review\nNothing found.\n\n## Automated reviews\n" + reviewed + "## Log\n", want: "merge now"},
		{name: "open question", body: "## Open questions\n- [ ] Which way?\n\n## Automated reviews\n" + reviewed + "## Log\n", want: "not yet: 1 open question(s)"},
		{name: "another machine runs the crew", worker: Agent{State: "working"}, readOnly: true, want: "not yet: worker can't be seen from this machine"},
		{name: "several, soonest to clear first", worker: Agent{State: "working"}, pr: func(p *gitHubPR) { p.Draft, p.CI = true, ciFailing }, want: "not yet: worker busy; CI failing; draft"},
		{name: "findings left for the user", body: "## Automated reviews\n" + reviewed + leftForUser + "## Log\n", want: "merge now, 2 finding(s) left for you"},
		{name: "findings left for the user, and in the way", body: "## Automated reviews\n" + reviewed + leftForUser + "## Log\n", pr: func(p *gitHubPR) { p.Draft = true }, want: "not yet: draft"},
		{name: "merged on GitHub", pr: func(p *gitHubPR) { p.State, p.Draft = "MERGED", true }, want: "merged on GitHub"},
		{name: "closed on GitHub", pr: func(p *gitHubPR) { p.State = "CLOSED" }, want: "closed on GitHub"},
		{name: "GitHub unread, clear when last synced", unread: true, want: "merge now (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, mergeable unknown when last synced", unread: true, fields: map[string]string{"mergeable": "unknown"}, want: "not yet: GitHub still checking for conflicts (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, draft and failing when last synced", unread: true, fields: map[string]string{"pr_state": "draft, approved", "ci_state": "failing"}, want: "not yet: CI failing; draft (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, never synced", unread: true, fields: map[string]string{"pr_state": ""}, want: "not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet"},
		{name: "no PR", fields: map[string]string{"pr": ""}, want: "not yet: no PR"},
		{name: "not pr-open", fields: map[string]string{"status": "working"}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fields := maps.Clone(clearTask)
			for k, v := range tc.fields {
				if v == "" {
					delete(fields, k)
				} else {
					fields[k] = v
				}
			}
			body := cmp.Or(tc.body, clearBody)
			task := mergeTask(t, dir, "021-task", fields, body)
			if tc.parent != "" {
				mergeTask(t, dir, "020-parent", map[string]string{"status": tc.parent, "base": "main", "branch": "agent/parent"}, "## Log\n")
			}
			settings := Settings{"AUTO_REVIEWS": "2", "DEFAULT_BRANCH": "main"}
			maps.Copy(settings, tc.settings)
			pr := clearPR
			if tc.pr != nil {
				tc.pr(&pr)
			}
			s := &statusView{Crew: &Crew{Home: dir}, readOnly: tc.readOnly, repo: repo, settings: settings, prs: map[string]gitHubPR{}}
			s.branches = s.Crew.branches("p")
			if !tc.unread {
				s.prs[task.Path] = pr
			}
			got := ""
			if m := s.mergeCheck(task, tc.worker); m != nil {
				got = m.String()
			}
			if got != tc.want {
				t.Errorf("merge check = %q, want %q", got, tc.want)
			}
		})
	}
}

// syncedPR reads back what record wrote.
func TestSyncedPRReadsBackWhatSyncRecorded(t *testing.T) {
	for _, pr := range []gitHubPR{
		{State: "OPEN", ReviewDecision: "APPROVED", UpdatedAt: "2026-11-20T10:00:00Z", Base: "main", CI: ciPassing, Mergeable: "MERGEABLE"},
		{State: "OPEN", Draft: true, ReviewDecision: "CHANGES_REQUESTED", Base: "agent/parent", CI: ciRunning, Mergeable: "UNKNOWN"},
		{State: "OPEN", CI: ciFailing, Mergeable: "CONFLICTING"},
		{State: "MERGED", Mergeable: "MERGEABLE"},
	} {
		task := mergeTask(t, t.TempDir(), "001-task", map[string]string{"status": "pr-open"}, "## Log\n")
		pr.record(task)
		if got := syncedPR(task); got != pr {
			t.Errorf("recorded %+v, read back %+v", pr, got)
		}
	}
	if got := syncedPR(mergeTask(t, t.TempDir(), "001-task", nil, "## Log\n")); got.State != "" {
		t.Errorf("a task never synced reads back as %+v, want no state", got)
	}
}
