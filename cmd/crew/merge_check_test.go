package main

import (
	"cmp"
	"errors"
	"fmt"
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
	clearPR   = gitHubPR{State: "OPEN", Base: "main", CI: ciPassing, MergeState: "CLEAN"}
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
	// Review 2 of a task whose findings have the outcomes given, after reviewed.
	review2 := func(outcomes ...string) string {
		s := "## Automated reviews\n" + reviewed + "### Automated review 2 — 2026-11-20 11:00\n- Reviewers: Claude reviewer\n"
		for i, o := range outcomes {
			s += fmt.Sprintf("#### %d. [should fix] f%d\n- Agreed severity: should fix\n- Outcome: %s\n", i+1, i+1, o)
		}
		return s + "\n"
	}
	// Every outcome LEDGER.md lists that leaves a finding for the user, and each
	// of them as the user decided it.
	leftForUser := []string{
		"left for the user",
		"left for the user: its fix would cross acceptance criterion 2",
		"left for the user: pre-existing, a day's work; proposal 013-h.md",
		"left for the user: verdict changed since review 1",
		"left for the user: still open from review 1",
		"left for the user: every deploy fails until the migration runs",
		"left for the user: a day's work, all in the parser",
	}
	var decided []string
	for _, o := range leftForUser {
		decided = append(decided, o+"; decided by the user: no change")
	}
	// A fix that found old instances is fixed, and not left for the user.
	fixed := []string{"fixed in abc1234", "fixed in abc1234; left for the user: also at the fork point, a.go:3"}
	// #111's three findings, once the worker recorded the user's decisions on
	// them as implementer.md says.
	decided111 := []string{
		"left for the user: its fix would cross acceptance criterion 6, which names `retros/`, `runs/` and `verify/`; decided by the user: take the second fix, fixed in 58050d9",
		"left for the user; decided by the user: no change, the check can fail",
		"left for the user; decided by the user: no change, the type word stays `memory`",
	}
	decisions := "## Decisions\n- 2026-11-20 — Keep the old flag. (user)\n- 2026-11-20 — Automated review 2, finding 1: no change. (user)\n\n"
	for _, tc := range []struct {
		name       string
		fields     map[string]string // over clearTask's; an empty value removes the key
		body       string            // in place of clearBody
		pr         func(*gitHubPR)   // changes clearPR
		unread     bool              // GitHub didn't answer
		unasked    bool              // GitHub wasn't asked for the PR
		worker     Agent
		unseen     string            // why the workers can't be seen
		unbranch   bool              // the branches haven't been read yet
		settings   map[string]string // over AUTO_REVIEWS=2, DEFAULT_BRANCH=main
		parent     string            // the status of 020-parent, on branch agent/parent
		reparent   string            // 020-parent's status by the time the report is built, after the branches were read
		secured    bool              // a security review is recorded
		unmigrated bool              // the ledger isn't migrated to this crew's records
		want       string
	}{
		{name: "nothing in the way", want: "merge now"},
		{name: "worker busy", worker: Agent{State: "working"}, want: "not yet: worker busy"},
		{name: "review round not sent", fields: map[string]string{"review_rounds": "2", "last_round_sent": "1"}, want: "not yet: round 2 not sent yet"},
		{name: "GitHub still checking for conflicts", pr: func(p *gitHubPR) { p.Mergeable = mergeableUnknown }, want: "not yet: GitHub still checking for conflicts"},
		{name: "CI running", pr: func(p *gitHubPR) { p.CI = ciRunning }, want: "not yet: CI running"},
		{name: "automated review due", body: "## Log\n", want: "not yet: no automated review yet"},
		{name: "automated review off for the task", fields: map[string]string{"auto_review": "no"}, body: "## Log\n", want: "merge now"},
		{name: "automated review off for the project", settings: map[string]string{"AUTO_REVIEWS": "0"}, body: "## Log\n", want: "merge now"},
		{name: "parent merged, still the base", fields: map[string]string{"base": "agent/parent"}, parent: "merged", want: "not yet: needs restack: 020-parent merged"},
		{name: "parent merged since the branches were read", fields: map[string]string{"base": "agent/parent"}, parent: "pr-open", reparent: "merged", want: "not yet: branches not read yet"},
		{name: "PR targets another branch", pr: func(p *gitHubPR) { p.Base = "agent/old" }, want: "not yet: PR targets agent/old, not main"},
		{name: "stacked on an unmerged task", fields: map[string]string{"base": "agent/parent"}, parent: "pr-open", pr: func(p *gitHubPR) { p.Base = "agent/parent" }, want: "not yet: stacked on 020"},
		{name: "based on another branch", fields: map[string]string{"base": "release"}, pr: func(p *gitHubPR) { p.Base = "release" }, want: "not yet: based on release, not main"},
		{name: "conflicts", pr: func(p *gitHubPR) { p.Mergeable = conflicting }, want: "not yet: conflicts with main"},
		{name: "behind its base, which the branch rules want it up to date with", pr: func(p *gitHubPR) { p.MergeState = "BEHIND" }, want: "not yet: behind main"},
		{name: "CI failing", pr: func(p *gitHubPR) { p.CI = ciFailing }, want: "not yet: CI failing"},
		{name: "CI cancelled", pr: func(p *gitHubPR) { p.CI = ciCancelled }, want: "not yet: CI cancelled"},
		{name: "no checks", pr: func(p *gitHubPR) { p.CI = "" }, want: "merge now"},
		{name: "draft", pr: func(p *gitHubPR) { p.Draft = true }, want: "not yet: draft"},
		{name: "changes requested", pr: func(p *gitHubPR) { p.ReviewDecision = "CHANGES_REQUESTED" }, want: "not yet: changes requested"},
		{name: "high risk without its security review", fields: map[string]string{"risk": "high"}, want: "not yet: no security review yet"},
		{name: "high risk with its security review", fields: map[string]string{"risk": "high"}, secured: true, want: "merge now"},
		{name: "high risk with only a security review typed into the file", fields: map[string]string{"risk": "high"}, body: "## Evidence\n### Security review\nNothing found.\n\n## Automated reviews\n" + reviewed + "## Log\n", want: "not yet: no security review yet"},
		{name: "high risk, in a ledger not migrated yet", fields: map[string]string{"risk": "high"}, body: "## Evidence\n### Security review\nNothing found.\n\n## Automated reviews\n" + reviewed + "## Log\n", unmigrated: true,
			want: "not yet: security review unread: p's ledger isn't migrated to this crew's records yet: run crew migrate"},
		{name: "low risk, in a ledger not migrated yet", unmigrated: true, want: "merge now"},
		{name: "open question", body: "## Open questions\n- [ ] Which way?\n\n## Automated reviews\n" + reviewed + "## Log\n", want: "not yet: 1 open question(s)"},
		{name: "review required", pr: func(p *gitHubPR) { p.ReviewDecision, p.MergeState = "REVIEW_REQUIRED", "BLOCKED" }, want: "not yet: review required"},
		{name: "blocked on GitHub for a reason crew can't name", pr: func(p *gitHubPR) { p.MergeState = "BLOCKED" }, want: "not yet: blocked by a branch rule on GitHub"},
		{name: "blocked on GitHub, with a reason crew names", pr: func(p *gitHubPR) { p.CI, p.MergeState = ciRunning, "BLOCKED" }, want: "not yet: CI running"},
		{name: "GitHub unread, changes requested when last synced", unread: true, fields: map[string]string{"pr_state": "open, changes requested"}, want: "not yet: changes requested (couldn't read GitHub; as of the last sync)"},
		{name: "another machine runs the crew", worker: Agent{State: "working"}, unseen: "worker can't be seen from this machine", want: "not yet: worker can't be seen from this machine"},
		{name: "claude agents failed", unseen: "workers couldn't be listed", want: "not yet: workers couldn't be listed"},
		{name: "workers not listed yet", unseen: "workers not listed yet", want: "not yet: workers not listed yet"},
		{name: "branches not read yet", unbranch: true, fields: map[string]string{"base": "agent/parent"}, parent: "pr-open", pr: func(p *gitHubPR) { p.Base = "agent/parent" }, want: "not yet: branches not read yet; stacked on 020"},
		{name: "branches not read yet, with nothing for git to say", unbranch: true, want: "merge now"},
		{name: "several, soonest to clear first", worker: Agent{State: "working"}, pr: func(p *gitHubPR) { p.Draft, p.CI = true, ciFailing }, want: "not yet: worker busy; CI failing; draft"},
		{name: "findings left for the user", body: review2(slices.Concat(leftForUser, fixed)...) + "## Log\n", want: "merge now, 7 finding(s) left for you"},
		{name: "findings left for the user, some decided", body: review2(slices.Concat(leftForUser, decided, fixed)...) + "## Log\n", want: "merge now, 7 finding(s) left for you"},
		{name: "every finding left for the user decided", body: review2(slices.Concat(decided, fixed)...) + "## Log\n", want: "merge now"},
		{name: "#111's findings, all decided", body: review2(decided111...) + "## Log\n", want: "merge now"},
		{name: "a finding decided under Decisions, whose outcome doesn't say so yet", body: decisions + review2(leftForUser[:2]...) + "## Log\n", want: "merge now, 2 finding(s) left for you"},
		{name: "the user's decisions about other things and another finding", body: decisions + review2(decided[0], leftForUser[1]) + "## Log\n", want: "merge now, 1 finding(s) left for you"},
		{name: "findings left for the user, and in the way", body: review2(leftForUser...) + "## Log\n", pr: func(p *gitHubPR) { p.Draft = true }, want: "not yet: draft"},
		{name: "merged on GitHub", pr: func(p *gitHubPR) { p.State, p.Draft = "MERGED", true }, want: "merged on GitHub"},
		{name: "closed on GitHub", pr: func(p *gitHubPR) { p.State = "CLOSED" }, want: "closed on GitHub"},
		{name: "GitHub unread, clear when last synced", unread: true, want: "merge now (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, mergeable unknown when last synced", unread: true, fields: map[string]string{"mergeable": "unknown"}, want: "not yet: GitHub still checking for conflicts (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, draft and failing when last synced", unread: true, fields: map[string]string{"pr_state": "draft, approved", "ci_state": "failing"}, want: "not yet: CI failing; draft (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, behind when last synced", unread: true, fields: map[string]string{"merge_state": "behind"}, want: "not yet: behind main (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, blocked when last synced", unread: true, fields: map[string]string{"merge_state": "blocked"}, want: "not yet: blocked by a branch rule on GitHub (couldn't read GitHub; as of the last sync)"},
		{name: "GitHub unread, never synced", unread: true, fields: map[string]string{"pr_state": ""}, want: "not yet: couldn't read the PR on GitHub, and crew sync hasn't recorded it yet"},
		{name: "GitHub not asked for the PR yet", unasked: true, want: "not yet: PR not read on GitHub yet"},
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
			if !tc.unmigrated {
				if err := writeLedgerFormat(filepath.Join(dir, "projects", "p")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.secured {
				add(t, task, Record{SecurityReview: &SecurityReview{"Nothing found."}})
			}
			if tc.parent != "" {
				mergeTask(t, dir, "020-parent", map[string]string{"status": tc.parent, "base": "main", "branch": "agent/parent"}, "## Log\n")
			}
			settings := Settings{"AUTO_REVIEWS": "2", "DEFAULT_BRANCH": "main"}
			maps.Copy(settings, tc.settings)
			pr := clearPR
			if tc.pr != nil {
				tc.pr(&pr)
			}
			c := &Crew{Home: dir}
			writeFile(t, c, "REPO="+repo+"\n", "projects", "p", "project.env")
			reads := statusReads{workers: &workerList{}, branches: c.readBranches([]string{"p"})}
			if tc.reparent != "" {
				mergeTask(t, dir, "020-parent", map[string]string{"status": tc.reparent, "base": "main", "branch": "agent/parent"}, "## Log\n")
			}
			if tc.unbranch {
				reads.branches = nil
			}
			s := &statusView{Crew: c, workersUnseen: tc.unseen, reads: reads, settings: settings, prs: map[string]prRead{}}
			s.branches = s.Crew.branches("p")
			switch {
			case tc.unread:
				s.prs[task.PR()] = prRead{err: errors.New("gh: no answer")}
			case !tc.unasked:
				s.prs[task.PR()] = prRead{pr: pr}
			}
			got := ""
			if m := s.mergeCheck(task, tc.worker); m != nil {
				got = m.wording().text
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
		{State: "OPEN", ReviewDecision: "APPROVED", UpdatedAt: "2026-11-20T10:00:00Z", Base: "main", CI: ciPassing, MergeState: "CLEAN"},
		{State: "OPEN", Draft: true, ReviewDecision: "CHANGES_REQUESTED", Base: "agent/parent", CI: ciRunning, Mergeable: mergeableUnknown, MergeState: "BLOCKED"},
		{State: "OPEN", CI: ciFailing, Mergeable: conflicting, MergeState: "DIRTY"},
		{State: "OPEN", CI: ciPassing, MergeState: "BEHIND"},
		{State: "MERGED"},
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
