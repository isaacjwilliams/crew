package main

// Red CI on an open PR: crew sync records the state of the PR's checks, crew
// ci-fix sends a red build to the task's worker as a review round, and crew
// ci-summary posts the worker's comment saying why CI was red and what it did.
// CI counts only once the worker has handed its PR over (pr-open): before
// that, it's the worker's own business.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ciState is the state of the checks on a PR's head commit, as crew sync records
// it in ci_state: running while any check hasn't finished, then failing if one
// failed, else passing. It's empty when the head has no checks.
type ciState string

const (
	ciRunning ciState = "running"
	ciFailing ciState = "failing"
	ciPassing ciState = "passing"
)

// A check on a PR's head commit, as `gh pr view --json statusCheckRollup` lists
// it: a check run (GitHub Actions, or another GitHub App), or a status context
// (the older commit status API, which some CI services still use).
type ciCheck struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`       // a check run's
	Status     string `json:"status"`     // a check run's: QUEUED, IN_PROGRESS, COMPLETED, …
	Conclusion string `json:"conclusion"` // a check run's, once it's COMPLETED
	Context    string `json:"context"`    // a status context's name
	State      string `json:"state"`      // a status context's: PENDING, SUCCESS, FAILURE, …
}

func (ch ciCheck) isStatusContext() bool { return ch.Typename == "StatusContext" }

func (ch ciCheck) finished() bool {
	if ch.isStatusContext() {
		return ch.State != "PENDING" && ch.State != "EXPECTED"
	}
	return ch.Status == "COMPLETED"
}

// failed: the check finished red. A cancelled, skipped or neutral one didn't.
func (ch ciCheck) failed() bool {
	if ch.isStatusContext() {
		return ch.State == "FAILURE" || ch.State == "ERROR"
	}
	return slices.Contains([]string{"FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED"}, ch.Conclusion)
}

func (ch ciCheck) name() string {
	if ch.isStatusContext() {
		return ch.Context
	}
	return ch.Name
}

// rollup is the state of a head commit's checks, and once they've all finished,
// the names of those that failed.
func rollup(checks []ciCheck) (ciState, []string) {
	if len(checks) == 0 {
		return "", nil
	}
	var failed []string
	for _, ch := range checks {
		if !ch.finished() {
			return ciRunning, nil
		}
		if ch.failed() {
			failed = append(failed, ch.name())
		}
	}
	if len(failed) > 0 {
		return ciFailing, failed
	}
	return ciPassing, nil
}

// shortSHA is a commit's abbreviated hash, as the task file's CI lines name it.
func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// redCI is a head commit whose checks failed, for a review round to report.
type redCI struct {
	head   string // short SHA
	checks []string
}

// crew ci-fix TASK: when CI went red on the head of a task's open PR after its
// worker handed the PR over, sends the worker the failure as a review round,
// once for each head commit. After MAX_CI_FIXES such rounds, it asks the user
// under Open questions instead. Exits 3 when there's nothing to do.
func cmdCIFix(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	if len(args) != 1 {
		return usage("crew ci-fix TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	max, err := intSetting(p.Settings, "MAX_CI_FIXES")
	if err != nil {
		return err
	}
	if t.PR() == "" {
		return fail("%s has no PR", t.ID())
	}
	if max == 0 {
		fmt.Println("nothing to do: MAX_CI_FIXES is 0, so crew leaves CI alone")
		return exitQuietly(3)
	}
	if st := t.Status(); st != PROpen {
		fmt.Printf("nothing to do: %s is %s, and its CI is its worker's own business until the PR is handed over (pr-open)\n", t.ID(), st)
		return exitQuietly(3)
	}
	// Read the PR's head and checks afresh: ci_state is only crew tick's filter.
	out, err := gh(p.Repo(), "pr", "view", t.PR(), "--json", "headRefOid,statusCheckRollup")
	if err != nil {
		return quietly(err)
	}
	var pr struct {
		HeadRefOid        string    `json:"headRefOid"`
		StatusCheckRollup []ciCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return fail("can't read the checks of %s: %v", t.PR(), err)
	}
	head := shortSHA(pr.HeadRefOid)
	state, failed := rollup(pr.StatusCheckRollup)
	if state != ciFailing {
		fmt.Printf("nothing to do: CI on %s is %s\n", head, cmp.Or(string(state), "not set up (no checks)"))
		return exitQuietly(3)
	}
	if err := c.lock(); err != nil {
		return err
	}
	if t.CIReported(head) {
		fmt.Printf("nothing to do: the red CI on %s is reported already\n", head)
		return exitQuietly(3)
	}
	checks := strings.Join(failed, ", ")
	if fixes := len(t.CIFailures()); fixes < max {
		n, _ := reviewRound{redCI: &redCI{head, failed}}.add(t, t.PRNumber())
		t.Log(fmt.Sprintf("review round %d recorded: CI failed on %s", n, head))
		fmt.Printf("round %d recorded: CI failed on %s (%s); crew tick sends it to the worker\n", n, head, checks)
	} else {
		t.AppendToSection("## Open questions", fmt.Sprintf("- [ ] %s%s after %d fix attempts (MAX_CI_FIXES=%d): %s. Look at it yourself, or tell the worker to try again.\n", ciStillRedOn, head, fixes, max, checks))
		t.Log(fmt.Sprintf("CI still red on %s after %d fix attempts; recorded under Open questions for the user", head, fixes))
		fmt.Printf("CI is still red on %s after %d fix attempts (MAX_CI_FIXES=%d); recorded under Open questions\n", head, fixes, max)
	}
	t.Touch()
	c.commit("ci-fix " + p.Name + "/" + t.ID())
	return nil
}

// ciSummaryMarker tags a worker's comment on why CI was red on head, so crew
// ci-summary can find it again.
func ciSummaryMarker(head string) string {
	return fmt.Sprintf("<!-- crew ci-summary: %s -->", head)
}

// crew ci-summary TASK FILE: posts FILE on the task's PR as the worker's account
// of the newest red CI a review round reported: why it was red, and what was
// done. Each head commit reported gets a comment of its own: when this GitHub
// account's comment for that head is there already, it is edited to FILE.
func cmdCISummary(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	if len(args) != 2 {
		return usage("crew ci-summary TASK FILE")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	text, err := commentFile(args[1])
	if err != nil {
		return err
	}
	if t.PR() == "" {
		return fail("task has no PR")
	}
	failures := t.CIFailures()
	if len(failures) == 0 {
		return fail("%s records no red CI to summarize (no '%s' line under '## Review rounds')", t.ID(), strings.TrimSpace(ciFailedOn))
	}
	head := failures[len(failures)-1]
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	me, err := c.ghLogin()
	if err != nil {
		return err
	}
	num := t.PRNumber()
	edited, err := postOrEditComment(p.Repo(), num, me, ciSummaryMarker(head), text)
	if err != nil {
		return err
	}
	if edited {
		fmt.Printf("updated the CI summary for %s on PR #%s\n", head, num)
	} else {
		fmt.Printf("posted the CI summary for %s on PR #%s\n", head, num)
	}
	return nil
}
