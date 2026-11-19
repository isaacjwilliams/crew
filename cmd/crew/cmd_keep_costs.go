package main

import (
	"fmt"
	"path/filepath"
)

// crew keep-costs [--all|--project P]
//
// Keeps what each finished task cost, read from this machine's transcripts,
// in its ## Cost section (step_costs.go), in the current project or all of
// them. A task waits until its worktree is cleaned up, when nothing can run in
// it any more, and is kept once. crew tick runs it, so the first tick after an
// upgrade keeps the costs of every task that finished before.
func cmdKeepCosts(c *Crew, args []string) error {
	all, proj := false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		default:
			return usage("crew keep-costs [--all|--project P]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	transcripts := claudeTranscripts()
	var briefs map[string][]string // read when a task first needs them
	kept, notRun, missing := 0, 0, 0
	for _, name := range c.projects(scope) {
		p, err := c.loadProject(name)
		if err != nil {
			warn("%s: %v; its costs wait", name, err)
			continue
		}
		for _, t := range c.tasks(name) {
			if _, done := t.Cost(); done || !t.Status().Finished() || t.Worktree() != "" {
				continue
			}
			if briefs == nil {
				if briefs, err = briefReviews(transcripts); err != nil {
					return err
				}
			}
			tc, err := taskCost(t, transcripts, filepath.Join(p.WorkRoot(), name, t.ID()), briefs[name+"/"+t.ID()])
			if err != nil {
				warn("%s: %v; its cost waits", t.ID(), err)
				continue
			}
			if err := t.AppendToSection("## Cost", tc.String()); err != nil {
				return err
			}
			kept, notRun = kept+1, notRun+len(tc.NotRun)
			if len(tc.Missing) > 0 {
				missing++
			}
		}
	}
	if kept == 0 {
		return nil
	}
	c.commit(fmt.Sprintf("keep the costs of %d task(s)", kept))
	fmt.Printf("kept the costs of %d task(s): %d step(s) not run, %d task(s) missing some\n", kept, notRun, missing)
	return nil
}
