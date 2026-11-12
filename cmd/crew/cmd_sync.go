package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// crew sync [--quiet] [--all]: finds missing PR URLs, records each PR's state and
// when it last changed, marks merged tasks, and reports stacked tasks that need a
// restack, in the current project or all of them.
func cmdSync(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	quiet, all, proj := false, false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--quiet":
			quiet = true
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		default:
			return usage("crew sync [--quiet] [--all|--project P]")
		}
		args = args[1:]
	}
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Printf(format+"\n", a...)
		}
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	projects := c.projects(scope)
	if err := c.lock(); err != nil {
		return err
	}
	changed := false
	for _, name := range projects {
		p, err := c.loadProject(name)
		if err != nil {
			return err
		}
		for _, t := range c.tasks(name) {
			st := t.Status()
			if st == Draft || st == Ready || st.Finished() {
				continue
			}
			pr := t.PR()
			if pr == "" && t.Branch() != "" {
				if pr = prOfBranch(p.Repo(), t.Branch()); pr != "" {
					t.Set("pr", pr)
					changed = true
					say("%s: found PR %s", t.ID(), pr)
				}
			}
			if pr == "" {
				continue
			}
			out, err := ghQuiet(p.Repo(), "pr", "view", pr, "--json", "state,isDraft,reviewDecision,updatedAt,statusCheckRollup")
			var info struct {
				State             string    `json:"state"`
				IsDraft           bool      `json:"isDraft"`
				ReviewDecision    string    `json:"reviewDecision"`
				UpdatedAt         string    `json:"updatedAt"`
				StatusCheckRollup []ciCheck `json:"statusCheckRollup"`
			}
			if err != nil || json.Unmarshal(out, &info) != nil {
				warn("%s: couldn't read %s", t.ID(), pr)
				continue
			}
			// For prMayHaveNewComments in crew tick.
			if t.Get("pr_updated") != info.UpdatedAt {
				t.Set("pr_updated", info.UpdatedAt)
				changed = true
			}
			// For crew tick, which runs crew ci-fix only while CI is failing.
			if ci, _ := rollup(info.StatusCheckRollup); t.Get("ci_state") != string(ci) {
				t.Set("ci_state", string(ci))
				changed = true
			}
			label := "open"
			switch {
			case info.State == "MERGED":
				label = "merged"
			case info.State == "CLOSED":
				label = "closed"
			case info.IsDraft:
				label = "draft"
			}
			if info.State != "MERGED" && info.State != "CLOSED" && info.ReviewDecision != "" {
				label += ", " + strings.ToLower(strings.ReplaceAll(info.ReviewDecision, "_", " "))
			}
			if t.Get("pr_state") != label {
				t.Set("pr_state", label)
				changed = true
			}
			if info.State == "MERGED" && st != Merged {
				t.Set("status", string(Merged))
				t.Touch()
				t.Log("PR merged")
				changed = true
				fmt.Printf("%s: merged (run 'crew cleanup %s')\n", t.ID(), t.ID())
			} else if info.State == "CLOSED" && st != Abandoned {
				say("%s: PR closed without merging (status left as '%s')", t.ID(), st)
			}
		}
		// Stacked tasks: say which need a restack, now that the merges above are known.
		idx, fetched := c.branches(name), false
		for _, t := range c.tasks(name) {
			if !t.Status().InFlight() {
				continue
			}
			parent, ok := idx.parentOf(t)
			if !ok {
				continue
			}
			if !fetched {
				if !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
					warn("%s: git fetch failed; checking stacks against local refs", name)
				}
				fetched = true
			}
			if noteBaseSHA(t, parent, p.Repo()) {
				changed = true
			}
			if reason := idx.restackReason(t, p.Repo()); reason != "" {
				say("%s: needs a restack, %s (run 'crew restack %s')", t.ID(), reason, t.ID())
			}
		}
	}
	if changed {
		c.commit("sync")
	}
	say("sync done")
	return nil
}
