package main

// Project memory: crew remember, crew keep, crew decline, crew memory. A
// project's memory is the facts about it that every crew agent should know,
// approved by the user, in projects/<p>/memory.md. Agents suggest facts with
// crew remember, as records beside memory-suggestions.md. The lead moves the
// ones the user approves into memory.md and settles each with crew keep or crew
// decline.

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// memoryRule is how an agent weighs project memory. It's printed with the
// memory, so no agent profile needs to say it again.
const memoryRule = "Where it disagrees with the code or the ledger, they win: suggest a correction with `crew remember`. " +
	"Claude's auto memory ranks below this memory, and below the code and the ledger. " +
	"To add a fact, suggest it with `crew remember`; only the lead edits this file, when the user approves."

// hookOutputCap is the most of a hook's output that Claude Code adds to a
// session's context, in characters. Past it, the session gets a file path and a
// short preview, and isn't told to read the file.
const hookOutputCap = 10000

// crew remember "<fact>" --why TEXT [--source S] [--project P]: suggests a fact
// for the project's memory.md, as a record, and prints the suggestion's ID. It
// waits on the user until crew keep or crew decline settles it.
func cmdRemember(c *Crew, args []string) error {
	const use = `crew remember "<fact>" --why TEXT [--source S] [--project P]`
	var proj, why string
	source := "lead"
	fact, err := parseText(args, map[string]*string{"--project": &proj, "--why": &why, "--source": &source}, use)
	if err != nil {
		return err
	}
	fact, why = oneLine(fact), oneLine(why)
	if fact == "" || why == "" {
		return usage(use)
	}
	if proj, err = c.oneProject(proj); err != nil {
		return err
	}
	if source, err = c.ledgerSource(source, proj); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	d := c.suggestionsDoc(proj)
	records, err := d.Records()
	if err != nil {
		return fail("%v", err)
	}
	if !isFile(d.Path) {
		if err := os.WriteFile(d.Path, []byte(suggestionsHead(proj)), 0o644); err != nil {
			return err
		}
	}
	r := newRecord("crew remember", records)
	r.Suggested = &Suggested{Fact: fact, Why: why, Source: source}
	if err := d.AddRecords(r); err != nil {
		return err
	}
	c.commit("remember " + proj)
	fmt.Println(r.ID)
	return nil
}

// crew keep ID [--project P]: settles a suggestion the user kept. The lead
// writes it into memory.md itself, in the words the user approved.
func cmdKeep(c *Crew, args []string) error {
	var proj string
	id, err := parseText(args, map[string]*string{"--project": &proj}, "crew keep ID [--project P]")
	if err != nil {
		return err
	}
	return c.settleSuggestion(proj, id, "keep", "kept", func(r *Record) { r.Kept = &Kept{Suggestion: id} })
}

// crew decline ID --why TEXT [--project P]: settles a suggestion the user
// turned down, with why, which stops it being suggested again.
func cmdDecline(c *Crew, args []string) error {
	const use = `crew decline ID --why TEXT [--project P]`
	var proj, why string
	id, err := parseText(args, map[string]*string{"--project": &proj, "--why": &why}, use)
	if err != nil {
		return err
	}
	if why = oneLine(why); why == "" {
		return fail("say why the user declined it: %s", use)
	}
	return c.settleSuggestion(proj, id, "decline", "declined", func(r *Record) { r.Declined = &Declined{Suggestion: id, Reason: why} })
}

// settleSuggestion adds the record crew <verb> writes, with the payload settle
// sets, for the project's open suggestion id, refusing an ID that isn't one. It
// says the suggestion was done.
func (c *Crew) settleSuggestion(proj, id, verb, done string, settle func(*Record)) error {
	if id == "" || strings.Contains(id, " ") {
		return fail("name one suggestion to %s by its ID (crew show memory-suggestions lists them)", verb)
	}
	proj, err := c.oneProject(proj)
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	d := c.suggestionsDoc(proj)
	records, err := d.Records()
	if err != nil {
		return fail("%v", err)
	}
	all := suggestions(records)
	i := slices.IndexFunc(all, func(s Item) bool { return s.ID == id })
	if i < 0 {
		return fail("%s has no memory suggestion %s (crew show memory-suggestions --project %s lists them)", proj, id, proj)
	}
	if !all[i].Open() {
		return fail("%s is already settled: %s", id, settledLine(all[i].Settled[0]))
	}
	r := newRecord("crew "+verb, records)
	settle(&r)
	if err := d.AddRecords(r); err != nil {
		return err
	}
	c.commit(verb + " " + proj)
	fmt.Printf("%s %s; %d suggestion(s) still wait on the user\n", done, id, openItems(all)-1)
	return nil
}

// suggestionsFile is a project's file of memory suggestions. Its records file
// holds the suggestions; the file itself is lines for people that crew never
// reads, and names the commands that do.
const suggestionsFile = "memory-suggestions.md"

func (c *Crew) suggestionsDoc(proj string) Doc {
	return Doc{c.path("projects", proj, suggestionsFile)}
}

// suggestionsHead is memory-suggestions.md as crew writes it.
func suggestionsHead(proj string) string {
	return "# Memory suggestions: " + proj + "\n\n" +
		"<!-- crew keeps this project's memory suggestions as records beside this file, and decides from them alone: " +
		"crew remember suggests a fact, crew keep and crew decline settle it, and crew show memory-suggestions prints them all. " +
		"Anything typed here is for people only. -->\n"
}

// suggestions are the memory suggestions records add, in the order they were
// made, each with what settled it.
func suggestions(records []Record) []Item {
	return items(records, memorySuggestion)
}

// memorySuggestions are a project's memory suggestions, from its records alone.
func (c *Crew) memorySuggestions(proj string) ([]Item, error) {
	records, err := c.suggestionsDoc(proj).Records()
	if err != nil {
		return nil, err
	}
	return suggestions(records), nil
}

// entry is a suggestion in the shape of an entry in memory.md, source and date
// included, so keeping it can move the text unchanged.
func (s Suggested) entry(day time.Time) string {
	return fmt.Sprintf("**%s** %s. (%s, %s)", s.Fact, strings.TrimSuffix(s.Why, "."), s.Source, day.Local().Format("2006-01-02"))
}

// settledLine says how a kept or declined record settled its suggestion, and
// when: "kept 2026-11-27 13:42 by crew keep".
func settledLine(r Record) string {
	if r.Declined != nil {
		return "declined " + stamp(r) + ": " + r.Declined.Reason
	}
	return "kept " + stamp(r)
}

// suggestionLines are the suggestions as Markdown: a checkbox, ticked once the
// suggestion is settled, its ID and its entry, and under it each record that
// settled it.
func suggestionLines(all []Item) []string {
	var out []string
	for _, s := range all {
		box := "[x]"
		if s.Open() {
			box = "[ ]"
		}
		out = append(out, fmt.Sprintf("- %s %s %s", box, s.ID, s.Suggested.entry(s.At)))
		for _, r := range s.Settled {
			out = append(out, "  - "+settledLine(r))
		}
	}
	return out
}

// shownSuggestions is memory-suggestions.md as crew show prints it: its
// heading and comments, the suggestions from its records, and anything typed
// into it, marked as not being suggestions crew counts. When crew can't read the
// records, it says why in their place. In a project not yet migrated, what's
// typed in the file is the suggestions, so it's shown as it is.
func shownSuggestions(path string) (string, error) {
	d := Doc{path}
	ls, err := d.read()
	if err != nil {
		return "", err
	}
	records, unread := d.Records()
	var shown []string
	if unread != nil {
		shown = []string{"crew can't read the memory suggestions: " + unread.Error()}
	} else {
		shown = suggestionLines(suggestions(records))
	}
	if errors.Is(unread, errNotMigrated) {
		return strings.Join(slices.Concat(trimBlankEnd(ls), []string{""}, shown), "\n") + "\n", nil
	}
	// uncommented blanks out comment lines rather than dropping them, so its
	// lines line up with the file's.
	var kept, typed []string // the file's heading, comments and blank lines, and the rest
	for i, l := range uncommented(ls) {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "# ") {
			kept = append(kept, ls[i])
		} else {
			typed = append(typed, ls[i])
		}
	}
	body := trimBlankEnd(kept)
	if len(shown) > 0 {
		body = append(append(body, ""), shown...)
	}
	if len(typed) > 0 {
		body = append(append(body, "", "Typed into the file, so not suggestions crew counts (crew remember adds one):"), typed...)
	}
	return strings.Join(body, "\n") + "\n", nil
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

// memory is what crew memory prints for a project: its header, then its
// memory.md. It's empty when there's no file.
func (c *Crew) memory(proj string) string {
	f := c.path("projects", proj, "memory.md")
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return memoryHeader(proj, f) + string(b)
}

// memoryHeader is a line naming a project's memory.md, then the rule for
// weighing it.
func memoryHeader(proj, file string) string {
	return fmt.Sprintf("Project memory for %s (%s): facts the user approved.\n%s\n\n", proj, file, memoryRule)
}

// sessionMemory is what the SessionStart hook prints for a session's
// CREW_PROJECT: its project's memory, or nothing for a session on every project
// or on none. A memory too long for Claude Code to pass on whole gets its
// header and an instruction to read the file instead.
func (c *Crew) sessionMemory(crewProject string) string {
	if crewProject == "" || crewProject == allProjects {
		return ""
	}
	m := c.memory(crewProject)
	if utf8.RuneCountInString(m) <= hookOutputCap {
		return m
	}
	f := c.path("projects", crewProject, "memory.md")
	return memoryHeader(crewProject, f) + "This memory is too long to load at session start. Read " + f + " in full now, before anything else.\n"
}
