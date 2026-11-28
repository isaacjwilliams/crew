package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// crew decide TASK WHO "<decision>": records a decision about the task, and
// whose call it was: the user's, a lead's or the worker's. Like crew to-learn,
// it doesn't touch the task file: crew show lists the decisions under
// Decisions.
func cmdDecide(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage(`crew decide TASK user|lead|worker "<decision>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	call, text := args[1], strings.TrimSpace(args[2])
	if !slices.Contains(calls, call) {
		return fail("whose call was it: %s? Not '%s'", orList(calls), call)
	}
	if text == "" {
		return fail("say what was decided")
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	r := newRecord("crew decide", records)
	r.Decided = &Decided{Text: text, Call: call}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("decide " + t.ID())
	fmt.Println(strings.Join(decisionLines([]Record{r}), "\n"))
	return nil
}

// crew user-check TASK N "<why>": records the user's agreement that the task's
// criterion N is theirs to check, a live smoke test say, so its worker may
// leave it to them. It's the user's call alone, at brief time or later.
func cmdUserCheck(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage(`crew user-check TASK N "<why>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	why := strings.TrimSpace(args[2])
	if why == "" {
		return fail("say why only the user can check it")
	}
	if err := c.lock(); err != nil {
		return err
	}
	criteria := t.AcceptanceCriteria()
	n, err := strconv.Atoi(args[1])
	if err != nil || n < 1 || n > len(criteria) {
		return fail("%s has no criterion %s: its Acceptance criteria has %d '- ' item(s)", t.ID(), args[1], len(criteria))
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	r := newRecord("crew user-check", records)
	r.UserCheck = &UserCheck{Criterion: criteria[n-1], Why: why}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("user-check " + t.ID())
	fmt.Printf("criterion %d is a user check\n", n)
	return nil
}

// crew user-decisions TASK: the decisions recorded as the user's call, as crew
// show lists them, oldest first. The pr-review skill briefs its reviewers with
// them.
func cmdUserDecisions(c *Crew, args []string) error {
	if len(args) != 1 {
		return usage("crew user-decisions TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	decisions, err := t.UserDecisions()
	if err != nil {
		return fail("%v", err)
	}
	for _, l := range decisionLines(decisions) {
		fmt.Println(l)
	}
	return nil
}

const decisionsHeading = "## Decisions"

// decisionLines are the decisions among records, as Markdown: one item each,
// "- <date> — <text> (<whose call>)", with any further lines of its text
// indented under it.
func decisionLines(records []Record) []string {
	var out []string
	for _, r := range records {
		if r.Decided == nil {
			continue
		}
		call := "(" + r.Decided.Call + ")"
		if r.Decided.Call == "" {
			call = "(whose call wasn't recorded)"
		}
		out = append(out, listItem("- "+r.At.Local().Format("2006-01-02")+" — ", r.Decided.Text+" "+call)...)
	}
	return out
}

// listItem is text as a list item's lines: its first line after lead, which
// starts the item, and the rest indented under it.
func listItem(lead, text string) []string {
	indent := strings.Repeat(" ", len(lead)-len(strings.TrimLeft(lead, " "))+2)
	lines := strings.Split(text, "\n")
	out := []string{lead + lines[0]}
	for _, l := range lines[1:] {
		out = append(out, strings.TrimRight(indent+l, " "))
	}
	return out
}

const criteriaHeading = "## Acceptance criteria"

// showUserChecks marks each criterion the user checks themselves, under its
// item, with when the user check was recorded and why. Below the criteria, it
// lists the user checks that cover none, their criterion gone from the brief.
func showUserChecks(ls []string, records []Record) []string {
	checks := slices.DeleteFunc(slices.Clone(records), func(r Record) bool { return r.UserCheck == nil })
	start := slices.Index(ls, criteriaHeading)
	if start < 0 || len(checks) == 0 {
		return ls
	}
	end := start + 1 + len(sectionLines(ls[start:], criteriaHeading))
	// uncommented keeps a line for each line, so plain lines up with ls.
	plain := uncommented(ls[start+1 : end])
	out := slices.Clone(ls[:start+1])
	covered := map[string]bool{}
	for i := 0; i < len(plain); i++ {
		out = append(out, ls[start+1+i])
		criterion, ok := strings.CutPrefix(plain[i], "- ")
		if !ok {
			continue
		}
		for i+1 < len(plain) && strings.TrimSpace(plain[i+1]) != "" && strings.TrimLeft(plain[i+1], " \t") != plain[i+1] {
			i++
			out = append(out, ls[start+1+i])
		}
		for _, r := range checks {
			if r.UserCheck.Criterion == criterion {
				out = append(out, listItem("  - User check, recorded "+stamp(r)+": ", r.UserCheck.Why)...)
				covered[criterion] = true
			}
		}
	}
	var gone []string
	for _, r := range checks {
		if !covered[r.UserCheck.Criterion] {
			gone = append(gone, fmt.Sprintf("The user check recorded %s covers no criterion: the brief no longer has \"%s\".", stamp(r), r.UserCheck.Criterion))
		}
	}
	if len(gone) > 0 {
		out = slices.Concat(trimBlankEnd(out), []string{""}, gone, []string{""})
	}
	return append(out, ls[end:]...)
}
