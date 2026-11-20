package main

// Whether the user can merge a pr-open task's PR now. crew status judges it from
// the PR as GitHub has it when status runs, and from what crew knows of the task.

import (
	"fmt"
	"strings"
	"sync"
)

// mergeCheck is whether a pr-open task's PR can be merged now.
type mergeCheck struct {
	// Closed is "merged" or "closed" once the PR isn't open on GitHub, so there's
	// nothing left to merge.
	Closed string
	// InTheWay is what stands between the PR and a merge, what's likely to clear
	// soonest first. It's empty when the PR can merge now.
	InTheWay []string
	// LeftForUser counts the findings the newest automated review left for the
	// user. They never stand in the way: nothing records that the user read them.
	LeftForUser int
	// LastSync: GitHub couldn't be read, so the check went by what crew sync
	// last recorded.
	LastSync bool
}

// String words the check: "merge now", "not yet: CI running; draft" or "merged
// on GitHub", and says when it went by the last sync.
func (m mergeCheck) String() string {
	s := "merge now"
	switch {
	case m.Closed != "":
		s = m.Closed + " on GitHub"
	case len(m.InTheWay) > 0:
		s = "not yet: " + strings.Join(m.InTheWay, "; ")
	case m.LeftForUser > 0:
		s += fmt.Sprintf(", %d finding(s) left for you", m.LeftForUser)
	}
	if m.LastSync {
		s += " (couldn't read GitHub; as of the last sync)"
	}
	return s
}

// mergeCheck works out whether a pr-open task's PR can merge now, from the PR as
// GitHub has it now (prs), or as crew sync last recorded it when GitHub couldn't
// be read. It's nil for a task that isn't pr-open.
func (s *statusView) mergeCheck(t Task, worker Agent) *mergeCheck {
	if t.Status() != PROpen {
		return nil
	}
	if t.PR() == "" {
		return &mergeCheck{InTheWay: []string{"no PR"}}
	}
	m := &mergeCheck{}
	pr, live := s.prs[t.Path]
	if !live {
		pr, m.LastSync = syncedPR(t), true
	}
	switch pr.State {
	case "MERGED", "CLOSED":
		m.Closed = strings.ToLower(pr.State)
		return m
	case "":
		return &mergeCheck{InTheWay: []string{"couldn't read the PR on GitHub, and crew sync hasn't recorded it yet"}}
	}
	base := s.branches.effectiveBase(t)
	parent, onParent := s.branches.liveParent(t)
	_, stacked := s.branches.parentOf(t)
	restack := ""
	if s.repo != "" {
		restack = s.branches.restackReason(t, s.repo)
	}
	retarget := s.branches.offBaseReason(t, pr.Base)
	defaultBranch := s.settings["DEFAULT_BRANCH"]
	// Every reason that holds is listed, even where one leads to another (a
	// merged parent still the base both needs a restack and may conflict).
	for _, c := range []struct {
		holds  bool
		reason string
	}{
		// Clears by itself, within minutes: the worker, crew tick, GitHub or CI.
		{s.workersUnseen == "" && worker.busy(), "worker busy"},
		{atoiOr0(t.Get("review_rounds")) > atoiOr0(t.Get("last_round_sent")), unsentRounds(t) + " not sent yet"},
		{pr.Mergeable == mergeableUnknown, "GitHub still checking for conflicts"},
		{pr.CI == ciRunning, "CI running"},
		{atoiOr0(t.AutoReviews(s.settings["AUTO_REVIEWS"])) > 0 && t.NewestReview() == 0, "no automated review yet"},
		// Clears once the branch is where it belongs: a restack or retarget crew
		// sends the worker, or the parent's PR merging.
		{restack != "", "needs restack: " + restack},
		{retarget != "", retarget},
		{onParent, "stacked on " + taskNumber(parent)},
		{!stacked && defaultBranch != "" && t.Base() != defaultBranch, fmt.Sprintf("based on %s, not %s", t.Base(), defaultBranch)},
		// Needs a fix from the worker.
		{pr.Mergeable == conflicting, "conflicts with " + base},
		{pr.CI == ciFailing, "CI failing"},
		{pr.Draft, "draft"},
		{t.AwaitsSecurityReview(), "no security review yet"},
		// Needs someone on GitHub: an update of the branch, a rerun of the
		// checks, or a reviewer's approval.
		{pr.MergeState == "BEHIND", "behind " + base},
		{pr.CI == ciCancelled, "CI cancelled"},
		{pr.ReviewDecision == "CHANGES_REQUESTED", "changes requested"},
		{pr.ReviewDecision == "REVIEW_REQUIRED", "review required"},
		// Needs the user.
		{t.OpenQuestions() > 0, fmt.Sprintf("%d open question(s)", t.OpenQuestions())},
		{s.workersUnseen != "", s.workersUnseen},
	} {
		if c.holds {
			m.InTheWay = append(m.InTheWay, c.reason)
		}
	}
	// GitHub has the last word: a PR it blocks for a reason not named above, such
	// as a required check that hasn't reported, isn't merge now either.
	if len(m.InTheWay) == 0 && pr.MergeState == "BLOCKED" {
		m.InTheWay = []string{"blocked by a branch rule on GitHub"}
	}
	if reviews := t.AutomatedReviews(); len(reviews) > 0 {
		m.LeftForUser = reviews[len(reviews)-1].LeftForUser()
	}
	return m
}

// readPRs asks GitHub for the PR of every pr-open task in the projects, all at
// once, and keys them by task file. A PR GitHub didn't answer for is left out.
func (s *statusView) readPRs(projects []string) map[string]gitHubPR {
	prs := map[string]gitHubPR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range projects {
		p, err := s.loadProject(name)
		if err != nil {
			continue
		}
		for _, t := range s.tasks(name) {
			if t.Status() != PROpen || t.PR() == "" {
				continue
			}
			wg.Go(func() {
				if pr, err := readPR(p.Repo(), t.PR()); err == nil {
					mu.Lock()
					prs[t.Path] = pr
					mu.Unlock()
				}
			})
		}
	}
	wg.Wait()
	return prs
}
