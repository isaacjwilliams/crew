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
	"fmt"
	"regexp"
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
// saying what settled them (by): an answered record for each, and its item
// ticked. It reads the records first, so a ledger crew can't record the
// settling in is left as it was.
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
	if err := t.AddRecords(answers...); err != nil {
		return err
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
		if in && isOpenNudgeQuestion(l) {
			// The marker stays last, to tie the item to its record.
			item := strings.TrimPrefix(l, "- [ ]")
			marker := questionMarkerEnd.FindString(item)
			ls[i] = "- [x]" + strings.TrimSuffix(item, marker) + " Settled: " + by + "." + marker
		}
	}
	return t.writeLines(ls)
}
