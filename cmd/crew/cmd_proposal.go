package main

// Proposals: crew proposals, crew promote.

import (
	"fmt"
	"os"
	"path/filepath"
)

// crew proposals [--all]
func cmdProposals(c *Crew, args []string) error {
	all := len(args) > 0 && args[0] == "--all"
	p, _ := c.projectFromCwd()
	n := 0
	for _, f := range c.files("proposals", p) {
		d := Doc{f}
		st := d.Get("status")
		if !all && st != "open" {
			continue
		}
		fmt.Printf("%-44s %-9s %-3s %s\n", cut(d.ID(), 44), st, d.Get("size"), d.Get("title"))
		n++
	}
	if n == 0 {
		if all {
			fmt.Fprintln(os.Stderr, "No proposals.")
		} else {
			fmt.Fprintln(os.Stderr, "No open proposals.")
		}
	}
	return nil
}

// crew promote PROPOSAL [--branch NAME]: a gardener proposal becomes a draft task.
func cmdPromote(c *Crew, args []string) error {
	const use = "crew promote PROPOSAL [--branch NAME]"
	if len(args) == 0 {
		return usage(use)
	}
	f, err := c.resolve("proposals", args[0])
	if err != nil {
		return err
	}
	pr := Proposal{Doc{f}}
	var branch []string
	for args = args[1:]; len(args) > 0; args = args[2:] {
		if args[0] != "--branch" || len(args) < 2 {
			return usage(use)
		}
		branch = []string{"--branch", args[1]}
	}
	if st := pr.Get("status"); st != "open" && st != "deferred" {
		return fail("proposal is '%s' (task: %s); only open or deferred proposals can be promoted", st, pr.Get("task"))
	}
	proj := pr.Get("project")
	if proj == "" || !isDir(c.path("projects", proj)) {
		proj = filepath.Base(filepath.Dir(filepath.Dir(f)))
	}
	t, err := c.newTask(append([]string{pr.Get("title"), "--project", proj}, branch...), false)
	if err != nil {
		return err
	}
	context := fmt.Sprintf("From gardener proposal `%s` (%s). Run normal intake: confirm the plan with the user.\n\n%s\nFirst step suggested by the gardener:\n%s",
		filepath.Base(f), f, withNewline(pr.Section("## Proposal")), withNewline(pr.Section("## First step")))
	if err := c.lock(); err != nil {
		return err
	}
	t.ReplaceSection("## Context", context)
	pr.Set("status", "accepted")
	pr.Set("task", t.ID())
	pr.Touch()
	pr.Log("promoted to task " + t.ID())
	c.commit("promote " + pr.ID() + " → " + t.ID())
	fmt.Println(t.Path)
	return nil
}

// withNewline is text as a command prints it: each line, the last one too, ends
// in a newline. Text without lines prints nothing.
func withNewline(text string) string {
	if text == "" {
		return ""
	}
	return text + "\n"
}
