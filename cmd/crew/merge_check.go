package main

// Whether the user can merge a pr-open task's PR now. crew status judges it from
// the PR as GitHub answered when last asked, and from what crew knows of the task.

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

// mergeCheck is whether a pr-open task's PR can be merged now.
type mergeCheck struct {
	// Closed is "merged" or "closed" once the PR isn't open on GitHub, so there's
	// nothing left to merge.
	Closed string
	// InTheWay is what stands between the PR and a merge, what's likely to clear
	// soonest first. It's empty when the PR can merge now.
	InTheWay []wording
	// AwaitingUser counts the findings the newest automated review left for the
	// user that they haven't decided yet. They never stand in the way: the user
	// can merge without deciding them.
	AwaitingUser int
	// LastSync: GitHub couldn't be read, so the check went by what crew sync
	// last recorded.
	LastSync bool
}

// wording words the check as a note: "merge now", "not yet: CI running; draft"
// or "merged on GitHub", saying when it went by the last sync. The live view
// shows what stands in the way as its notes show them, and a PR that can merge
// now as merge, in green, with the findings left for the user. A merged or
// closed PR's mark says it all.
func (m mergeCheck) wording() wording {
	var w wording
	switch {
	case m.Closed != "":
		w = stripOnly(m.Closed + " on GitHub")
	case len(m.InTheWay) > 0:
		w = wording{"not yet: " + strings.Join(texts(m.InTheWay), "; "), joinNotes(m.InTheWay).live}
	default:
		w = wording{"merge now", usersWords.Foreground(lipgloss.Green).Render("merge")}
		if m.AwaitingUser > 0 {
			w.text += fmt.Sprintf(", %d finding(s) left for you", m.AwaitingUser)
			w.live += " " + usersWords.Render(plural(m.AwaitingUser, "finding"))
		}
	}
	if m.LastSync {
		w.text += " (couldn't read GitHub; as of the last sync)"
	}
	return w
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
		return &mergeCheck{InTheWay: []wording{forUser("no PR", "no PR")}}
	}
	if _, asked := s.prs[ref]; !asked {
		return &mergeCheck{InTheWay: []wording{stripOnly("PR not read on GitHub yet")}}
	}
	pr, answered := s.prOf(t)
	m := &mergeCheck{LastSync: !answered}
	switch pr.State {
	case "MERGED", "CLOSED":
		m.Closed = strings.ToLower(pr.State)
		return m
	case "":
		return &mergeCheck{InTheWay: []wording{stripOnly("couldn't read the PR on GitHub, and crew sync hasn't recorded it yet")}}
	}
	base := s.branches.effectiveBase(t)
	parent, onParent := s.branches.liveParent(t)
	_, stacked := s.branches.parentOf(t)
	branches, known := s.branchesOf(t)
	restack := branches.restack
	retarget := s.branches.offBaseReason(t, pr.Base)
	defaultBranch := s.settings["DEFAULT_BRANCH"]
	// Every reason that holds is listed, even where one leads to another (a
	// merged parent still the base both needs a restack and may conflict). The
	// live view leaves to the strip what the PR's marks show.
	for _, c := range []struct {
		holds  bool
		reason wording
	}{
		// Clears within seconds: the branches crew status hasn't read yet.
		{!known, stripOnly("branches not read yet")},
		// Clears by itself, within minutes: the worker, crew tick, GitHub or CI.
		{s.workersUnseen == "" && worker.busy(), standing("worker busy", underWaySign)},
		{atoiOr0(t.Get("review_rounds")) > atoiOr0(t.Get("last_round_sent")), standing(unsentRounds(t)+" not sent yet", underWaySign)},
		{pr.Mergeable == mergeableUnknown, stripOnly("GitHub still checking for conflicts")},
		{pr.CI == ciRunning, stripOnly("CI running")},
		{atoiOr0(t.AutoReviews(s.settings["AUTO_REVIEWS"])) > 0 && t.NewestReview() == 0, standing("no automated review yet", underWaySign)},
		// Clears once the branch is where it belongs: a restack or retarget crew
		// sends the worker, or the parent's PR merging.
		{restack != "", standing("needs restack: "+restack, mustMoveSign)},
		{retarget != "", standing(retarget, mustMoveSign)},
		{onParent, standing("stacked on "+taskNumber(parent), waitingSign)},
		{!stacked && defaultBranch != "" && t.Base() != defaultBranch, standing(fmt.Sprintf("based on %s, not %s", t.Base(), defaultBranch), mustMoveSign)},
		// Needs a fix from the worker.
		{pr.Mergeable == conflicting, stripOnly("conflicts with " + base)},
		{pr.CI == ciFailing, stripOnly("CI failing")},
		{pr.Draft, stripOnly("draft")},
		{t.AwaitsSecurityReview(), standing("no security review yet", noSecurityReviewSign)},
		// Needs someone on GitHub: an update of the branch, a rerun of the
		// checks, or a reviewer's approval.
		{pr.MergeState == "BEHIND", stripOnly("behind " + base)},
		{pr.CI == ciCancelled, stripOnly("CI cancelled")},
		{pr.ReviewDecision == "CHANGES_REQUESTED", stripOnly("changes requested")},
		{pr.ReviewDecision == "REVIEW_REQUIRED", stripOnly("review required")},
		// Needs the user.
		{t.OpenQuestions() > 0, questionsNote(t.OpenQuestions())},
		{s.workersUnseen != "", stripOnly(s.workersUnseen)},
	} {
		if c.holds {
			m.InTheWay = append(m.InTheWay, c.reason)
		}
	}
	// GitHub has the last word: a PR it blocks for a reason not named above, such
	// as a required check that hasn't reported, isn't merge now either.
	if len(m.InTheWay) == 0 && pr.MergeState == "BLOCKED" {
		m.InTheWay = []wording{stripOnly("blocked by a branch rule on GitHub")}
	}
	if reviews := t.AutomatedReviews(); len(reviews) > 0 {
		m.AwaitingUser = reviews[len(reviews)-1].AwaitingUser()
	}
	return m
}

// prOf is t's PR as GitHub answered when crew status last asked, or else as
// crew sync last recorded it: answered is false then. crew status asks GitHub
// only for pr-open tasks' PRs.
func (s *statusView) prOf(t Task) (pr gitHubPR, answered bool) {
	if read, asked := s.prs[t.PR()]; asked && read.err == nil {
		return read.pr, true
	}
	return syncedPR(t), false
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
