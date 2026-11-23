package main

// Whether the user can merge a pr-open task's PR now. crew status judges it from
// the PR as GitHub answered when last asked, and from what crew knows of the task.

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
// GitHub had it when crew last asked (prs), or as crew sync last recorded it when
// GitHub didn't answer. It's nil for a task that isn't pr-open.
func (s *statusView) mergeCheck(t Task, worker Agent) *mergeCheck {
	if t.Status() != PROpen {
		return nil
	}
	ref := t.PR()
	if ref == "" {
		return &mergeCheck{InTheWay: []string{"no PR"}}
	}
	read, asked := s.prs[ref]
	if !asked {
		return &mergeCheck{InTheWay: []string{"PR not read on GitHub yet"}}
	}
	m := &mergeCheck{}
	pr := read.pr
	if read.err != nil {
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
	branches, known := s.branchesOf(t)
	restack := branches.restack
	retarget := s.branches.offBaseReason(t, pr.Base)
	defaultBranch := s.settings["DEFAULT_BRANCH"]
	// Every reason that holds is listed, even where one leads to another (a
	// merged parent still the base both needs a restack and may conflict).
	for _, c := range []struct {
		holds  bool
		reason string
	}{
		// Clears within seconds: the branches crew status hasn't read yet.
		{!known, "branches not read yet"},
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

// prRead is what GitHub answered when crew asked for a PR: the PR, or the error.
type prRead struct {
	pr  gitHubPR
	err error
}

// prReads are what GitHub answered for each project's PRs: a project's name →
// a PR as its tasks name it → the answer. A PR is named within its project's
// repository, so two projects can name different PRs the same way.
type prReads map[string]map[string]prRead

// readPRs asks GitHub for the PR of every pr-open task in the projects, all at
// once.
func (c *Crew) readPRs(projects []string) prReads {
	prs := prReads{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range projects {
		p, projectErr := c.loadProject(name)
		reads := map[string]prRead{}
		prs[name] = reads
		for _, t := range c.tasks(name) {
			if t.Status() != PROpen {
				continue
			}
			ref := t.PR()
			if ref == "" {
				continue
			}
			keep := func(pr gitHubPR, err error) {
				mu.Lock()
				reads[ref] = prRead{pr, err}
				mu.Unlock()
			}
			// A project crew can't read GitHub for here is as if GitHub didn't
			// answer: its checks go by the last sync.
			if projectErr != nil {
				keep(gitHubPR{}, projectErr)
				continue
			}
			wg.Go(func() { keep(readPR(p.Repo(), ref)) })
		}
	}
	wg.Wait()
	return prs
}
