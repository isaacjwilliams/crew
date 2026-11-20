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
	defaultBranch := s.settings["DEFAULT_BRANCH"]
	for _, c := range []struct {
		holds  bool
		reason string
	}{
		{!s.readOnly && worker.busy(), "worker busy"},
		{atoiOr0(t.Get("review_rounds")) > atoiOr0(t.Get("last_round_sent")), "review round not sent yet"},
		{pr.Mergeable == "UNKNOWN", "GitHub still checking for conflicts"},
		{pr.CI == ciRunning, "CI running"},
		{atoiOr0(t.AutoReviews(s.settings["AUTO_REVIEWS"])) > 0 && t.NewestReview() == 0, "no automated review yet"},
		{restack != "", "needs restack: " + restack},
		{pr.Base != "" && pr.Base != base, fmt.Sprintf("PR targets %s, not %s", pr.Base, base)},
		{onParent, "stacked on " + taskNumber(parent)},
		{!stacked && defaultBranch != "" && t.Base() != defaultBranch, fmt.Sprintf("based on %s, not %s", t.Base(), defaultBranch)},
		{pr.Mergeable == "CONFLICTING", "conflicts with " + base},
		{pr.CI == ciFailing, "CI failing"},
		{pr.Draft, "draft"},
		{t.AwaitsSecurityReview(), "no security review yet"},
		{t.OpenQuestions() > 0, fmt.Sprintf("%d open question(s)", t.OpenQuestions())},
		{s.readOnly, "worker can't be seen from this machine"},
	} {
		if c.holds {
			m.InTheWay = append(m.InTheWay, c.reason)
		}
	}
	if reviews := t.AutomatedReviews(); len(reviews) > 0 {
		for _, f := range reviews[len(reviews)-1].Findings {
			if f.Outcome() == OutcomeLeftForUser {
				m.LeftForUser++
			}
		}
	}
	return m
}

// readPRs asks GitHub for the PR of each pr-open task, all at once, and keys
// them by task file. A PR GitHub didn't answer for is left out.
func readPRs(repo string, tasks []Task) map[string]gitHubPR {
	prs := map[string]gitHubPR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range tasks {
		if t.Status() != PROpen || t.PR() == "" {
			continue
		}
		wg.Go(func() {
			if pr, err := readPR(repo, t.PR()); err == nil {
				mu.Lock()
				prs[t.Path] = pr
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return prs
}
