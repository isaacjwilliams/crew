package main

// Task worktrees are made and removed with plain `git worktree`, or with Worktrunk
// (`wt`) when the project sets WORKTREE_TOOL=worktrunk, so the repo's wt hooks run
// (per-worktree databases, services, dependency installs). crew never passes
// --yes: hooks the user hasn't approved make wt fail rather than run. Hook output
// goes to a log under logs/ instead of the caller's terminal. These are the only
// places task worktrees are made or removed.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func unknownTool(tool string) error {
	return fail("unknown WORKTREE_TOOL '%s' (use git or worktrunk)", tool)
}

// worktreeCreate makes a worktree at wt on branch: a new branch from startRef, or
// the existing branch when startRef is empty.
func (p *Project) worktreeCreate(wt, branch, startRef, logf string) error {
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return err
	}
	p.pruneWorktrees()
	switch tool := p.Settings["WORKTREE_TOOL"]; tool {
	case "git":
		if startRef != "" {
			return gitDo(p.Repo(), "worktree", "add", "--quiet", "--no-track", "-b", branch, wt, startRef)
		}
		return gitDo(p.Repo(), "worktree", "add", "--quiet", wt, branch)
	case "worktrunk":
		if err := need("wt"); err != nil {
			return err
		}
		args := []string{"switch", "--no-cd", "--format", "json", "--config-set", "worktree-path=" + jsonString(wt)}
		if startRef != "" {
			args = append(args, "--create", "--base", startRef)
		}
		log, err := openLog(logf)
		if err != nil {
			return err
		}
		defer log.Close()
		fmt.Fprintf(log, "== %s wt %s %s\n", now(), strings.Join(args, " "), branch)
		fmt.Printf("Creating %s with Worktrunk (hook output: %s)\n", wt, logf)
		cmd := exec.Command("wt", append(append([]string{"-C", p.Repo()}, args...), branch)...)
		cmd.Stderr = log
		out, err := cmd.Output()
		if err != nil {
			tailTo(os.Stderr, logf, 15)
			return fail("wt couldn't create the worktree (full output in %s). If its hooks need approval, run 'wt config approvals add' in %s.", logf, p.Repo())
		}
		var res struct {
			Path string `json:"path"`
		}
		json.Unmarshal(out, &res)
		got, err1 := canonical(res.Path)
		want, err2 := canonical(wt)
		if res.Path == "" || err1 != nil || err2 != nil || got != want {
			path := res.Path
			if path == "" {
				path = "unknown"
			}
			return fail("wt left %s at '%s' instead of %s; does the branch already have a worktree? (wt list)", branch, path, wt)
		}
		return nil
	default:
		return unknownTool(tool)
	}
}

// worktreeRemove removes a worktree, and its changes too when force is set. It
// reports whether it did; the error is for a worktree tool it can't run at all.
// crew deletes merged branches itself, the same way for both tools.
func (p *Project) worktreeRemove(wt string, force bool, logf string) (bool, error) {
	var flags []string
	if force {
		flags = []string{"--force"}
	}
	switch tool := p.Settings["WORKTREE_TOOL"]; tool {
	case "git":
		return gitDo(p.Repo(), append(append([]string{"worktree", "remove"}, flags...), wt)...) == nil, nil
	case "worktrunk":
		if err := need("wt"); err != nil {
			return false, err
		}
		log, err := openLog(logf)
		if err != nil {
			return false, err
		}
		defer log.Close()
		fmt.Fprintf(log, "== %s wt remove %s\n", now(), wt)
		cmd := exec.Command("wt", append(append([]string{"-C", p.Repo(), "remove", "--no-delete-branch", "--foreground"}, flags...), wt)...)
		cmd.Stdout, cmd.Stderr = log, log
		if cmd.Run() != nil {
			tailTo(os.Stderr, logf, 15)
			warn("full output in %s", logf)
			return false, nil
		}
		return true, nil
	default:
		return false, unknownTool(tool)
	}
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// tailTo copies the last n lines of a file to w.
func tailTo(w *os.File, path string, n int) {
	b, _ := os.ReadFile(path)
	lines := splitLines(string(b))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// How setupWorktree made a missing worktree.
type worktreeFrom string

const (
	fromLocalBranch  worktreeFrom = "local-branch"  // the task's branch here
	fromOriginBranch worktreeFrom = "origin-branch" // the task's branch on origin, pushed from another machine
	fromBase         worktreeFrom = "base"          // a new branch from its base
)

// setupWorktree makes a task's worktree ready for a worker: it creates it unless
// it exists, and copies the WORKTREE_COPY files from the main checkout. It says
// how it made a missing one, and for a new branch, the commit it starts at.
//
// An existing worktree whose path the task file doesn't record yet (dispatch
// records it only after this succeeds) is left from a dispatch that failed while
// creating it, where Worktrunk's setup hooks may not have finished, so it's
// refused. Run it without the ledger lock: Worktrunk hooks can take minutes.
func (p *Project) setupWorktree(t Task, wt, branch, base string) (from worktreeFrom, startSHA string, err error) {
	repo := p.Repo()
	if isDir(wt) {
		if !gitOK(wt, "rev-parse", "--is-inside-work-tree") {
			return "", "", fail("%s exists but isn't a git worktree", wt)
		}
		if p.Settings["WORKTREE_TOOL"] == "worktrunk" && t.Worktree() == "" {
			return "", "", fail("%s is left over from a dispatch whose Worktrunk setup failed; remove it with 'wt -C %s remove %s' and dispatch again", wt, repo, wt)
		}
	} else {
		startRef := ""
		switch {
		case gitOK(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch):
			from = fromLocalBranch
		case gitOK(repo, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch):
			from, startRef = fromOriginBranch, "origin/"+branch
		default:
			from, startRef = fromBase, "origin/"+base
			if !gitOK(repo, "rev-parse", "--verify", "--quiet", startRef) {
				startRef = base
			}
			startSHA, _ = git(repo, "rev-parse", "--verify", "--quiet", startRef+"^{commit}")
		}
		if err := p.worktreeCreate(wt, branch, startRef, p.worktreeLog(t)); err != nil {
			return "", "", err
		}
	}
	for _, f := range strings.Fields(p.Settings["WORKTREE_COPY"]) {
		if exists(filepath.Join(repo, f)) && !exists(filepath.Join(wt, f)) {
			os.MkdirAll(filepath.Dir(filepath.Join(wt, f)), 0o755)
			if out, err := exec.Command("cp", "-a", filepath.Join(repo, f), filepath.Join(wt, f)).CombinedOutput(); err != nil {
				return "", "", fail("couldn't copy %s into %s: %s", f, wt, strings.TrimSpace(string(out)))
			}
		}
	}
	return from, startSHA, nil
}

func (p *Project) worktreeLog(t Task) string {
	return filepath.Join(p.Home, "logs", "worktree-"+p.Name+"-"+t.ID()+".log")
}

// ensureWorktree makes sure a dispatched task's worktree is on this machine, at
// $WORK_ROOT/<p>/<id>. After a failover the task file names another machine's
// path, and the task's branch may be on origin only, or nowhere, if that machine
// never pushed it: then the branch starts over from its base. It records the
// path when it changes, and logs how a missing worktree came back. Run it without
// the ledger lock.
func (c *Crew) ensureWorktree(p *Project, t Task) error {
	wt := filepath.Join(p.WorkRoot(), p.Name, t.ID())
	base := t.Base()
	if base == "" {
		base = p.DefaultBranch()
	}
	if !isDir(wt) && !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
		warn("git fetch origin failed; using local refs")
	}
	from, startSHA, err := p.setupWorktree(t, wt, t.Branch(), base)
	if err != nil {
		return err
	}
	msg := map[worktreeFrom]string{
		fromLocalBranch:  "recreated worktree on the local branch " + t.Branch(),
		fromOriginBranch: "recreated worktree from origin/" + t.Branch(),
	}[from]
	if from == fromBase {
		msg = "started over: " + t.Branch() + " was never pushed from " + c.previousHolder()
	}
	if t.Worktree() == wt && msg == "" {
		return nil
	}
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	t.Set("worktree", wt)
	if startSHA != "" {
		t.Set("base_sha", startSHA)
	}
	if msg != "" {
		t.Log(msg)
	}
	t.Touch()
	c.commit("worktree " + p.Name + "/" + t.ID())
	return nil
}
