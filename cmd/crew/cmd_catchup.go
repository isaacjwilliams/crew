package main

// A PR that stops merging cleanly into its base, usually because another PR
// merged first: crew sync records it in mergeable, and crew catch-up sends it to
// the task's worker as a review round, once for each commit of the base, so the
// worker merges the base in. Like red CI, it counts only once the worker has
// handed its PR over (pr-open). A stacked task's conflicts are crew restack's.
//
// crew catch-up has crew ci-fix's shape: resolve the task, read the PR again,
// check it's open, then decide under the lock. That preamble isn't shared: for
// two commands, a helper isn't worth it yet. A third would make it worth it.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// crew catch-up TASK: when the open PR of a task its worker handed over
// conflicts with the task's base, and the task isn't stacked, sends the worker
// the conflict as a review round, once for each commit of the base, and
// records it. Exits 3 when there's nothing to do.
func cmdCatchUp(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	if len(args) != 1 {
		return usage("crew catch-up TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if t.PR() == "" {
		return fail("%s has no PR", t.ID())
	}
	if parent, stacked := c.branches(p.Name).parentOf(t); stacked {
		return nothingToDo("%s is stacked on %s, so its conflicts are a restack's (crew restack)", t.ID(), parent.ID())
	}
	// Read the PR afresh: the mergeable crew sync recorded may be stale by now.
	out, err := gh(p.Repo(), "pr", "view", t.PR(), "--json", "state,mergeable,baseRefName")
	if err != nil {
		return quietly(err)
	}
	var pr struct {
		State       string `json:"state"`
		Mergeable   string `json:"mergeable"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return fail("can't read whether %s conflicts with its base: %v", t.PR(), err)
	}
	num, base := t.PRNumber(), t.Base()
	switch {
	case pr.State != "OPEN":
		return nothingToDo("PR #%s is %s", num, strings.ToLower(pr.State))
	case pr.Mergeable == "UNKNOWN":
		return nothingToDo("GitHub hasn't worked out yet whether PR #%s conflicts with %s", num, pr.BaseRefName)
	case pr.Mergeable != "CONFLICTING":
		return nothingToDo("PR #%s merges cleanly into %s", num, pr.BaseRefName)
	case pr.BaseRefName != base:
		// GitHub worked the conflict out against the PR's base, and only the
		// task's counts: a PR still on another branch is retargeted first.
		return nothingToDo("PR #%s targets %s, not the task's base %s", num, pr.BaseRefName, base)
	}
	// The base's commit comes from origin. GitHub's baseRefOid won't do: it stays
	// at an older commit of the base, even after the head is pushed.
	ref, err := git(p.Repo(), "ls-remote", "origin", "refs/heads/"+base)
	if err != nil {
		return fail("can't read %s on origin: %v", base, err)
	}
	sha, _, _ := strings.Cut(ref, "\t")
	if sha == "" {
		return fail("origin has no branch %s", base)
	}
	// The task's status is read under the lock, so one abandoned while gh and git
	// ran isn't sent anything, and two runs can't both report the same commit.
	if err := c.lock(); err != nil {
		return err
	}
	switch st := t.Status(); {
	case st.Finished():
		return nothingToDo("%s is %s", t.ID(), st)
	case st != PROpen:
		return nothingToDo("%s is %s, and its conflicts are its worker's own business until the PR is handed over (pr-open)", t.ID(), st)
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	conflict := &BaseConflict{Base: base, At: shortSHA(sha)}
	if roundReports(records).ConflictAt(conflict.At) {
		return nothingToDo("the conflict with %s at %s is reported already", conflict.Base, conflict.At)
	}
	r := newRecord("crew catch-up", records)
	r.BaseConflict = conflict
	n, err := reviewRound{conflict: conflict}.addRecorded(t, num, r)
	if err != nil {
		return err
	}
	t.Log(fmt.Sprintf("review round %d recorded: conflicts with %s at %s", n, conflict.Base, conflict.At))
	t.Touch()
	c.commit("catch-up " + p.Name + "/" + t.ID())
	fmt.Printf("round %d recorded: conflicts with %s at %s; crew tick sends it to the worker\n", n, conflict.Base, conflict.At)
	return nil
}
