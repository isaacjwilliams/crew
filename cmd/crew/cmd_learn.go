package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// crew to-learn TASK "<item>": adds something the draft's objective or gates
// rest on that nobody has checked yet, as a record, and prints the item's ID.
// crew ready refuses the draft until crew learned settles it.
//
// Neither this nor crew learned touches the task file: two clones that add and
// settle items on one task then merge without a conflict.
func cmdToLearn(c *Crew, args []string) error {
	if len(args) != 2 {
		return usage(`crew to-learn TASK "<item>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	text := oneLine(args[1])
	if text == "" {
		return fail("say what there is to learn")
	}
	if err := c.lock(); err != nil {
		return err
	}
	if st := t.Status(); st != Draft {
		return fail("%s is '%s': only a draft has things still to learn", t.ID(), st)
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	r := newRecord("crew to-learn", records)
	r.ToLearn = &ToLearn{Text: text}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("to-learn " + t.ID())
	fmt.Println(r.ID)
	return nil
}

// crew learned TASK ITEM "<answer>": settles a still-to-learn item with its
// answer, which may run to several lines. Unlike crew to-learn, it takes a task
// in any status: crew migrate may have moved an open item from one past draft.
func cmdLearned(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage(`crew learned TASK ITEM "<answer>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	id, answer := args[1], strings.TrimSpace(args[2])
	if answer == "" {
		return fail("give the answer that settles %s", id)
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	items := learnItems(records)
	i := slices.IndexFunc(items, func(i LearnItem) bool { return i.ID == id })
	if i < 0 {
		return fail("%s has no item %s still to learn (crew show %s lists them)", t.ID(), id, t.ID())
	}
	if !items[i].Open() {
		return fail("%s is already settled, on %s", id, items[i].Answers[0].At.Local().Format(logTime))
	}
	r := newRecord("crew learned", records)
	r.Learned = &Learned{Item: id, Answer: answer}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("learned " + t.ID())
	fmt.Printf("settled %s; %d item(s) still open\n", id, countOpen(items)-1)
	return nil
}

const learnHeading = "## Still to learn"

// shownTask is a task as crew show prints it: its file, with what its records
// say put in the sections they belong to.
func shownTask(path string) (string, error) {
	t := Task{Doc{path}}
	ls, err := t.read()
	if err != nil {
		return "", err
	}
	return strings.Join(withQuestions(t, withLearning(t, ls)), "\n") + "\n", nil
}

// withLearning is the task file's lines with its still-to-learn items from its
// records under Still to learn. Anything typed into the file's section stays
// below them, marked as not being items crew counts. When crew can't read the
// items, the section says why. In a project not yet migrated, what's typed in
// it is the items, so it's shown as it is.
func withLearning(t Task, ls []string) []string {
	var items []string
	learning, unread := t.StillToLearn()
	if unread != nil {
		items = []string{"crew can't read the items still to learn: " + unread.Error()}
	} else {
		items = learnLines(learning)
	}
	if !slices.Contains(ls, learnHeading) && len(items) == 0 {
		return ls
	}
	ls, start, end := placeSection(ls, learnHeading)
	// uncommented blanks out comment lines rather than dropping them, so its
	// lines line up with the section's.
	var kept, typed []string // the section's comments and blank lines, and the rest
	for i, l := range uncommented(ls[start+1 : end]) {
		if strings.Trim(l, " \t") == "" {
			kept = append(kept, ls[start+1+i])
		} else {
			typed = append(typed, ls[start+1+i])
		}
	}
	body := append(trimBlankEnd(kept), items...)
	switch {
	case errors.Is(unread, errNotMigrated):
		body = append(body, typed...)
	case len(typed) > 0:
		body = append(append(body, "", "Typed into the file, so not items crew counts (crew to-learn adds one):"), typed...)
	}
	return slices.Concat(ls[:start+1], body, []string{""}, ls[end:])
}

// learnLines are the items as Markdown: a checkbox, ticked once the item is
// settled, with its ID and when it was added, and under it each answer, with
// when it was given. Two clones may each settle an item, so it can have more
// than one answer.
func learnLines(items []LearnItem) []string {
	var out []string
	for _, i := range items {
		box := "[x]"
		if i.Open() {
			box = "[ ]"
		}
		out = append(out, fmt.Sprintf("- %s %s (%s, added %s)", box, i.ToLearn.Text, i.ID, stamp(i.Record)))
		for _, a := range i.Answers {
			out = append(out, answerLines(a, a.Learned.Answer)...)
		}
	}
	return out
}

// answerLines are an answer under its item: when it was given and by which
// command, then its text, on the same line when it's one line long.
func answerLines(a Record, text string) []string {
	lead := "  - settled " + stamp(a) + ":"
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		return []string{lead + " " + lines[0]}
	}
	out := []string{lead}
	for _, l := range lines {
		out = append(out, strings.TrimRight("    "+l, " "))
	}
	return out
}

// placeSection is the lines with the section heading in them, added before
// the Log when it's missing, and where the heading and the section's end are.
func placeSection(ls []string, heading string) ([]string, int, int) {
	start := slices.Index(ls, heading)
	if start < 0 {
		start = slices.Index(ls, "## Log")
		if start < 0 {
			start = len(ls)
		}
		ls = slices.Insert(ls, start, heading, "")
	}
	return ls, start, start + 1 + len(sectionLines(ls[start:], heading))
}

// stamp says when a record was written, and by which command.
func stamp(r Record) string { return r.At.Local().Format(logTime) + " by " + r.By }
