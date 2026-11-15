package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// crew sync [--quiet] [--all]: finds missing PR URLs, records each PR's state and
// when it last changed, marks merged tasks, raises the risk of tasks that touch a
// sensitive path, and reports stacked tasks that need a restack, in the current
// project or all of them.
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
			// A kind that makes no PR has none to find: one under its branch's
			// name is an old one, from a branch since deleted.
			if rules, err := t.rules(); err == nil && !rules.pr {
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
			out, err := ghQuiet(p.Repo(), "pr", "view", pr, "--json", "state,isDraft,reviewDecision,updatedAt,statusCheckRollup,mergeable")
			var info struct {
				State             string    `json:"state"`
				IsDraft           bool      `json:"isDraft"`
				ReviewDecision    string    `json:"reviewDecision"`
				UpdatedAt         string    `json:"updatedAt"`
				StatusCheckRollup []ciCheck `json:"statusCheckRollup"`
				Mergeable         string    `json:"mergeable"`
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
			// For crew tick, which runs crew catch-up only while the PR conflicts with its base.
			mergeable := ""
			if info.Mergeable == "CONFLICTING" {
				mergeable = conflicting
			}
			if t.Get("mergeable") != mergeable {
				t.Set("mergeable", mergeable)
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
				t.SetStatus(Merged)
				t.Touch()
				t.Log("PR merged")
				changed = true
				fmt.Printf("%s: merged (run 'crew cleanup %s')\n", t.ID(), t.ID())
			} else if info.State == "CLOSED" && st != Abandoned {
				say("%s: PR closed without merging (status left as '%s')", t.ID(), st)
			}
		}
		// Tasks in flight, now that the merges above are known: raise the risk of
		// those whose branch touches a sensitive path, and say which stacked tasks
		// need a restack.
		// Both read origin's branches, so the project's clone is fetched first, once.
		idx, fetched := c.branches(name), false
		fetch := func() {
			if !fetched {
				if !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
					warn("%s: git fetch failed; using the refs as last fetched", name)
				}
				fetched = true
			}
		}
		sensitive := strings.TrimSpace(p.Settings["SENSITIVE_PATHS"]) != ""
		for _, t := range c.tasks(name) {
			if !t.Status().InFlight() {
				continue
			}
			// A kind that makes no PR, a spike say, has no risk tier to raise.
			if rules, err := t.rules(); err == nil && rules.pr {
				if sensitive {
					fetch()
				}
				if raised, err := raiseRisk(t, p); err != nil {
					warn("%s: %v", t.ID(), err)
				} else if raised != "" {
					changed = true
					fmt.Printf("%s: %s\n", t.ID(), raised)
				}
			}
			parent, ok := idx.parentOf(t)
			if !ok {
				continue
			}
			fetch()
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
