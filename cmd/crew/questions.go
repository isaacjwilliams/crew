package main

// Questions crew opens itself are question records: crew feedback opens a
// collaborator's PR comment for the user, crew ci-fix red CI past MAX_CI_FIXES,
// and crew nudge a stalled worker for the lead. crew settle settles a nudge
// question once the worker moves on.
//
// crew still decides from the unchecked items under Open questions, so each
// question is also an item there, written with it: its first line ends with
// "(question <id>)", which ties it to its record.

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const questionsHeading = "## Open questions"

// Questions are the questions opened on the task, from its records alone, in
// the order they were opened, each with the records that settled it.
func (t Task) Questions() ([]Item, error) {
	records, err := t.Records()
	if err != nil {
		return nil, err
	}
	return items(records, kindQuestion), nil
}

// ask opens questions on the task, all by the command by: a record for each,
// and its item under Open questions. Nothing is written when crew can't read
// the task's records (before crew migrate, say), so the caller, which holds the
// lock and commits, can refuse with nothing lost.
func (t Task) ask(by string, qs ...Question) ([]Record, error) {
	records, err := t.Records()
	if err != nil {
		return nil, fmt.Errorf("can't open a question on %s: %w", t.ID(), err)
	}
	var rs []Record
	var lines []string
	for _, q := range qs {
		r := newRecord(by, append(records, rs...))
		r.Question = &q
		rs = append(rs, r)
		lines = append(lines, r.questionItem()...)
	}
	if err := t.AddRecords(rs...); err != nil {
		return nil, err
	}
	return rs, t.AppendToSection(questionsHeading, strings.Join(lines, "\n")+"\n")
}

// questionItem is a question as an unchecked item under Open questions: its
// first line ends with the question's marker, and the rest is indented under it.
func (r Record) questionItem() []string {
	first, rest, _ := strings.Cut(r.Question.Text, "\n")
	out := []string{"- [ ] " + first + questionMarker(r.ID)}
	for _, l := range splitLines(rest) {
		out = append(out, "  "+l)
	}
	return out
}

func questionMarker(id string) string { return " (question " + id + ")" }

var questionMarkerEnd = regexp.MustCompile(` \(question ([0-9a-f]+)\)$`)

// itemQuestion is the ID of the question an item under Open questions was
// written for, or "" when the line isn't one.
func itemQuestion(l string) string {
	if _, ok := checklistItem(l); !ok {
		return ""
	}
	if m := questionMarkerEnd.FindStringSubmatch(l); m != nil {
		return m[1]
	}
	return ""
}

// SettleNudgeQuestions settles the open questions crew nudge asked the lead,
// saying what settled them (by). It records the settling, as an answered
// record for each open nudge question, and ticks each open nudge item under
// Open questions, which is what crew still counts. It reads the records first,
// so a ledger crew can't record the settling in is left as it was.
func (t Task) SettleNudgeQuestions(by string) error {
	records, err := t.Records()
	if err != nil {
		return fmt.Errorf("can't settle the nudge question on %s: %w", t.ID(), err)
	}
	var answers []Record
	for _, q := range items(records, kindQuestion) {
		if q.Question.Kind == QuestionNudge && q.Open() {
			r := newRecord("crew settle", append(records, answers...))
			r.Answered = &Answered{Question: q.ID, Text: "The worker moved on: " + by + "."}
			answers = append(answers, r)
		}
	}
	if len(answers) > 0 {
		if err := t.AddRecords(answers...); err != nil {
			return err
		}
	}
	ls, err := t.read()
	if err != nil {
		return err
	}
	in := false
	for i, l := range ls {
		if strings.HasPrefix(l, "## ") {
			in = l == questionsHeading
		}
		// By its words, not its record, so a nudge question asked before crew
		// kept records is settled too.
		if in && isOpenNudgeQuestion(l) {
			// The marker stays last, to tie the item to its record.
			item := strings.TrimPrefix(l, "- [ ]")
			marker := questionMarkerEnd.FindString(item)
			ls[i] = "- [x]" + strings.TrimSuffix(item, marker) + " Settled: " + by + "." + marker
		}
	}
	return t.writeLines(ls)
}

// withQuestions is the task file's lines with what the records say of each
// question under its item in Open questions: what it's about, who asked it and
// when, and who it waits on or what settled it. A question whose item is gone
// is shown at the end of the section. Every other line stays as it is.
//
// Until crew decides from the records, it counts only the unticked items, so a
// question whose item was ticked or removed by hand is shown settled, though no
// record says so.
func withQuestions(t Task, ls []string) []string {
	questions, unread := t.Questions()
	if errors.Is(unread, errNotMigrated) || unread == nil && len(questions) == 0 {
		return ls
	}
	ls, start, end := placeSection(ls, questionsHeading)
	if unread != nil {
		return slices.Concat(ls[:start+1], []string{"crew can't read the questions: " + unread.Error()}, ls[start+1:])
	}
	section := ls[start+1 : end]
	var body []string
	shown := map[string]bool{}
	for i := 0; i < len(section); i++ {
		body = append(body, section[i])
		id := itemQuestion(section[i])
		at := slices.IndexFunc(questions, func(q Item) bool { return q.ID == id })
		if at < 0 {
			continue
		}
		byHand := ""
		if strings.HasPrefix(section[i], "- [x]") {
			byHand = "ticked"
		}
		for i+1 < len(section) && strings.HasPrefix(section[i+1], "  ") {
			i++
			body = append(body, section[i])
		}
		body = append(body, questionLines(questions[at], byHand)...)
		shown[id] = true
	}
	body = trimBlankEnd(body)
	for _, q := range questions {
		if !shown[q.ID] {
			item := q.questionItem()
			item[0] = "- [x]" + strings.TrimPrefix(item[0], "- [ ]")
			body = append(append(body, item...), questionLines(q, "removed")...)
		}
	}
	return slices.Concat(ls[:start+1], body, []string{""}, ls[end:])
}

// questionLines say under a question's item what it's about, who asked it and
// when, and then who it waits on or what settled it. byHand is how its item
// was settled by hand under Open questions, "ticked" or "removed", or "".
func questionLines(q Item, byHand string) []string {
	line := fmt.Sprintf("  - %s from %s, opened %s", q.Question.about(), q.Question.Asker, stamp(q.Record))
	switch {
	case !q.Open():
	case byHand != "":
		line += "; settled: its item was " + byHand + " by hand"
	default:
		line += "; waits on the " + q.Question.For
	}
	out := []string{line}
	for _, a := range q.Settled {
		out = append(out, settledLines(a, a.Answered.Text)...)
	}
	return out
}

// about names what opened the question.
func (q Question) about() string {
	switch q.Kind {
	case QuestionCollaborator:
		return "PR comment"
	case QuestionNudge:
		return "stalled worker"
	case QuestionRedCI:
		return "red CI on " + q.Head
	}
	return string(q.Kind)
}
