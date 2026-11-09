package main

// Project memory: crew remember, crew memory. A project's memory is the facts
// about it that every crew agent should know, approved by the user, in
// projects/<p>/memory.md. Agents suggest facts into memory-suggestions.md, and
// the lead moves the ones the user approves into memory.md.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// memoryRule is how an agent weighs project memory. It's printed with the
// memory, so no agent profile needs to say it again.
const memoryRule = "Where it disagrees with the code or the ledger, they win: suggest a correction with `crew remember`. " +
	"Claude's auto memory ranks below this memory, and below the code and the ledger. " +
	"To add a fact, suggest it with `crew remember`; only the lead edits this file, when the user approves."

// crew remember "<fact>" --why TEXT [--source S] [--project P]
func cmdRemember(c *Crew, args []string) error {
	const use = `crew remember "<fact>" --why TEXT [--source S] [--project P]`
	var fact, proj, why string
	source := "lead"
	for len(args) > 0 {
		flag := map[string]*string{"--project": &proj, "--why": &why, "--source": &source}[args[0]]
		if flag != nil && len(args) > 1 {
			*flag, args = args[1], args[2:]
			continue
		}
		fact += " " + args[0]
		args = args[1:]
	}
	fact, why = oneLine(fact), oneLine(why)
	if fact == "" || why == "" {
		return usage(use)
	}
	proj, err := c.oneProject(proj)
	if err != nil {
		return err
	}
	if source, err = c.ledgerSource(source, proj); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	f := c.path("projects", proj, "memory-suggestions.md")
	if !isFile(f) {
		head := "# Memory suggestions: " + proj + "\n\n" +
			"<!-- Facts suggested for memory.md by crew remember. \"- [ ]\" waits on the user; the lead ticks each one \"→ kept\" or \"→ declined: <reason>\". -->\n\n"
		if err := os.WriteFile(f, []byte(head), 0o644); err != nil {
			return err
		}
	}
	if err := (Doc{f}).appendRaw(memorySuggestion(fact, why, source, time.Now()) + "\n"); err != nil {
		return err
	}
	c.commit("remember " + proj)
	fmt.Println(f)
	return nil
}

// memorySuggestion is a suggestion's line: an unchecked item holding the entry,
// source and date included, exactly as it goes into memory.md, so approving it
// moves the text unchanged.
func memorySuggestion(fact, why, source string, day time.Time) string {
	return fmt.Sprintf("- [ ] **%s** %s. (%s, %s)", fact, strings.TrimSuffix(why, "."), source, day.Format("2006-01-02"))
}

// oneLine is s with its runs of spaces and newlines made single spaces, so it
// fits on one line of a list.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// crew memory [--project P] | crew memory --hook
func cmdMemory(c *Crew, args []string) error {
	if len(args) == 1 && args[0] == "--hook" {
		// A SessionStart hook: Claude Code adds what it prints to the session's context.
		fmt.Print(c.sessionMemory(os.Getenv("CREW_PROJECT")))
		return nil
	}
	var proj string
	for len(args) > 0 {
		if args[0] != "--project" || len(args) < 2 {
			return usage("crew memory [--project P] | crew memory --hook")
		}
		proj, args = args[1], args[2:]
	}
	proj, err := c.oneProject(proj)
	if err != nil {
		return err
	}
	m := c.memory(proj)
	if m == "" {
		fmt.Fprintf(os.Stderr, "No project memory for %s yet.\n", proj)
		return nil
	}
	fmt.Print(m)
	return nil
}

// memory is what crew memory prints for a project: a line naming its memory.md
// and the rule for weighing it, then the file. It's empty when there's no file.
func (c *Crew) memory(proj string) string {
	f := c.path("projects", proj, "memory.md")
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("Project memory for %s (%s): facts the user approved.\n%s\n\n%s", proj, f, memoryRule, b)
}

// sessionMemory is what the SessionStart hook prints for a session's
// CREW_PROJECT: its project's memory, or nothing for a session on every project
// or on none.
func (c *Crew) sessionMemory(crewProject string) string {
	if crewProject == "" || crewProject == allProjects {
		return ""
	}
	return c.memory(crewProject)
}

// memorySuggestions counts a project's suggestions still waiting on the user.
func (c *Crew) memorySuggestions(proj string) int {
	b, _ := os.ReadFile(c.path("projects", proj, "memory-suggestions.md"))
	return unchecked(string(b))
}
