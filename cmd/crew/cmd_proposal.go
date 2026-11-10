package main

// Proposals: crew propose, crew proposals, crew promote. A proposal is an idea
// worth keeping that isn't a task yet, from the gardener or anyone else.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// crew propose "<title>" [--why TEXT] [--revisit-when TEXT] [--source S] [--project P]
func cmdPropose(c *Crew, args []string) error {
	const use = `crew propose "<title>" [--why TEXT] [--revisit-when TEXT] [--source S] [--project P]`
	var title, proj, why, revisitWhen string
	source := "lead"
	for len(args) > 0 {
		flag := map[string]*string{"--project": &proj, "--why": &why, "--revisit-when": &revisitWhen, "--source": &source}[args[0]]
		if flag != nil && len(args) > 1 {
			*flag, args = args[1], args[2:]
			continue
		}
		if title == "" {
			title = args[0]
		} else {
			title += " " + args[0]
		}
		args = args[1:]
	}
	if title == "" {
		return usage(use)
	}
	// 33 characters, so the ID with its date fits crew proposals' column.
	slug := strings.TrimSuffix(cut(slugify(title), 33), "-")
	if slug == "" {
		return fail("the title needs a letter or a digit, to name the proposal's file")
	}
	proj, err := c.oneProject(proj)
	if err != nil {
		return err
	}
	p, err := c.loadProject(proj)
	if err != nil {
		return err
	}
	if source, err = c.ledgerSource(source, proj); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}

	id := time.Now().Format("2006-01-02") + "-" + slug
	pr := Proposal{Doc{filepath.Join(p.Dir, "proposals", id+".md")}}
	if isFile(pr.Path) {
		return fail("there's already a proposal %s; add to it, or give this one another title", id)
	}
	if err := c.fromTemplate("proposal.md", pr.Path, map[string]string{
		"ID": id, "TITLE": strings.ReplaceAll(title, `"`, "'"), "PROJECT": proj, "SOURCE": source, "NOW": now(),
	}); err != nil {
		return err
	}
	if revisitWhen != "" {
		pr.Set("revisit_when", revisitWhen)
	}
	if why != "" {
		pr.ReplaceSection("## Why it matters", why+"\n")
	}
	pr.Log("proposed by " + pr.Author())
	c.commit("propose " + proj + "/" + id)
	fmt.Println(pr.Path)
	return nil
}

// ledgerSource checks the --source of what an agent suggests to the ledger: user,
// lead, gardener, or worker:<task>, which it returns with the task's full ID.
func (c *Crew) ledgerSource(s, proj string) (string, error) {
	switch s {
	case "gardener", "user", "lead":
		return s, nil
	}
	task, ok := strings.CutPrefix(s, "worker:")
	if !ok {
		return "", fail("--source '%s': use user, lead, gardener or worker:<task>", s)
	}
	f, matches := resolveIn(task, c.files("tasks", proj))
	if f == "" {
		return "", ambiguousFail(task, matches, "--source '%s' doesn't match exactly one task in %s", s, proj)
	}
	return "worker:" + (Doc{f}).ID(), nil
}

// crew proposals [--any-status] [--all|--project P]
func cmdProposals(c *Crew, args []string) error {
	anyStatus, all, p := false, false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--any-status":
			anyStatus, args = true, args[1:]
		case args[0] == "--all":
			all, args = true, args[1:]
		case args[0] == "--project" && len(args) > 1:
			p, args = args[1], args[2:]
		default:
			return usage("crew proposals [--any-status] [--all|--project P]")
		}
	}
	p, err := c.scope(p, all)
	if err != nil {
		return err
	}
	n := 0
	for _, f := range c.files("proposals", p) {
		d := Doc{f}
		st := d.Get("status")
		if !anyStatus && st != "open" {
			continue
		}
		fmt.Printf("%-44s %-9s %-3s %-11s %s\n", d.ID(), st, d.Get("size"), cut(d.Get("source"), 11), d.Get("title"))
		if r := d.Get("revisit_when"); r != "" {
			fmt.Printf("%45srevisit when: %s\n", "", r)
		}
		n++
	}
	if n == 0 {
		if anyStatus {
			fmt.Fprintln(os.Stderr, "No proposals.")
		} else {
			fmt.Fprintln(os.Stderr, "No open proposals.")
		}
	}
	return nil
}

// crew promote PROPOSAL [--branch NAME]: a proposal becomes a draft task.
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
	if err := c.lock(); err != nil {
		return err
	}
	t.ReplaceSection("## Context", pr.TaskContext())
	pr.Set("status", "accepted")
	pr.Set("task", t.ID())
	pr.Touch()
	pr.Log("promoted to task " + t.ID())
	c.commit("promote " + pr.ID() + " → " + t.ID())
	fmt.Println(t.Path)
	return nil
}
