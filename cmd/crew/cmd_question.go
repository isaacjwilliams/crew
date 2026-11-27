package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Questions that wait on someone are question records, which only crew
// commands write: crew ask opens a worker's or a parked one, crew feedback,
// crew ci-fix and crew nudge open crew's own, crew hand-on leaves one for the
// user, and crew answer and crew settle settle them.
//
// crew still decides from the unchecked items under Open questions, so each
// command also writes or ticks the question's item there: its first line ends
// with "(question <id>)", the marker crew finds it by.

// crew ask TASK [--split] "<question>": opens a question, and prints its ID. On
// a draft, it's a lead parking the draft for the user. On any other task, it's
// the worker's, for its goal lead or, outside a goal, for the user; --split
// marks it as a proposal to split the branch into smaller pieces.
func cmdAsk(c *Crew, args []string) error {
	split := len(args) > 0 && args[0] == "--split"
	if split {
		args = args[1:]
	}
	if len(args) != 2 {
		return usage(`crew ask TASK [--split] "<question>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	text := strings.TrimSpace(args[1])
	if text == "" {
		return fail("say what the question is")
	}
	if err := c.lock(); err != nil {
		return err
	}
	q := Question{Kind: QuestionWorker, Asker: "worker", For: "user", Text: text}
	if t.Goal() != "" {
		q.For = "goal-lead"
	}
	switch st := t.Status(); {
	case st.Finished():
		return fail("%s is '%s': nobody is left to answer a question on it", t.ID(), st)
	case st == Draft && split:
		return fail("%s is a draft: only a worker proposes to split its branch", t.ID())
	case st == Draft:
		q = Question{Kind: QuestionParked, Asker: "lead", For: "user", Text: text}
		if t.Goal() != "" {
			q.Asker = "goal-lead"
		}
	case split:
		q.Kind = QuestionSplit
	}
	rs, err := t.ask("crew ask", q)
	if err != nil {
		return fail("%v", err)
	}
	if q.Kind == QuestionParked {
		t.Log("Parked: " + rs[0].question())
	} else {
		t.Log("Asked the " + whom(q.For) + ": " + rs[0].question())
	}
	c.commit("ask " + t.ID())
	fmt.Println(rs[0].ID)
	return nil
}

// crew hand-on TASK QUESTION "<why>": leaves a question for the user, saying why
// its asker's lead won't answer it.
func cmdHandOn(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage(`crew hand-on TASK QUESTION "<why>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	id, why := args[1], oneLine(args[2])
	if why == "" {
		return fail("say why %s is the user's to answer", id)
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, q, err := t.openQuestion(id)
	if err != nil {
		return err
	}
	if q.WaitsOn() == "user" {
		return fail("%s already waits on the user", id)
	}
	r := newRecord("crew hand-on", records)
	r.HandedOn = &HandedOn{Question: id, Why: why}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	// crew stats counts the user's wait on a goal's task from this line.
	t.Log("For the user: " + why)
	c.commit("hand-on " + t.ID())
	fmt.Printf("handed %s on to the user\n", id)
	return nil
}

// crew answer TASK QUESTION "<answer>": settles a question with its answer,
// which may run to several lines.
func cmdAnswer(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage(`crew answer TASK QUESTION "<answer>"`)
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
	records, _, err := t.openQuestion(id)
	if err != nil {
		return err
	}
	r := newRecord("crew answer", records)
	r.Answer = &Answer{Question: id, Text: answer}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	if err := t.tickQuestions(id); err != nil {
		return err
	}
	t.Log(fmt.Sprintf("Answered question %s", id))
	c.commit("answer " + t.ID())
	open := 0
	for _, q := range questionItems(append(records, r)) {
		if q.Open() {
			open++
		}
	}
	fmt.Printf("settled %s; %d question(s) still open\n", id, open)
	return nil
}

// openQuestion is the task's records and its question id, which is still open.
func (t Task) openQuestion(id string) ([]Record, QuestionItem, error) {
	records, err := t.Records()
	if err != nil {
		return nil, QuestionItem{}, fail("%v", err)
	}
	items := questionItems(records)
	i := slices.IndexFunc(items, func(q QuestionItem) bool { return q.ID == id })
	if i < 0 {
		return nil, QuestionItem{}, fail("%s has no question %s (crew show %s lists them)", t.ID(), id, t.ID())
	}
	if !items[i].Open() {
		return nil, QuestionItem{}, fail("%s is already settled, on %s", id, items[i].Answers[0].At.Local().Format(logTime))
	}
	return records, items[i], nil
}

// ask records questions on the task, all written by the command by, and adds
// each to Open questions as an unchecked item. It reads the records first, so
// a ledger crew can't record a question in is left as it was. The caller holds
// the lock and commits.
func (t Task) ask(by string, qs ...Question) ([]Record, error) {
	records, err := t.Records()
	if err != nil {
		return nil, err
	}
	var rs []Record
	var items strings.Builder
	for _, q := range qs {
		r := newRecord(by, append(records, rs...))
		r.Question = &q
		if err := r.check(); err != nil {
			return nil, err
		}
		rs = append(rs, r)
		items.WriteString(r.openQuestionsItem())
	}
	if err := t.AddRecords(rs...); err != nil {
		return nil, err
	}
	return rs, t.AppendToSection("## Open questions", items.String())
}

// question is a question's first line.
func (r Record) question() string {
	first, _, _ := strings.Cut(r.Question.Text, "\n")
	return first
}

// openQuestionsItem is a question as an unchecked item under Open questions,
// its first line ending with its marker and the rest indented under it.
func (r Record) openQuestionsItem() string {
	first, rest, _ := strings.Cut(r.Question.Text, "\n")
	item := "- [ ] " + first + questionMarker(r.ID) + "\n"
	for _, l := range splitLines(rest) {
		item += strings.TrimRight("  "+l, " ") + "\n"
	}
	return item
}

func questionMarker(id string) string { return " (question " + id + ")" }

var questionMarkerEnd = regexp.MustCompile(` \(question [0-9a-f]+\)$`)

// cutQuestionMarker is an item's first line without its question marker, and
// the marker, or "" when it has none.
func cutQuestionMarker(l string) (string, string) {
	m := questionMarkerEnd.FindString(l)
	return strings.TrimSuffix(l, m), m
}

// tickQuestions ticks the items under Open questions that the questions ids
// opened.
func (t Task) tickQuestions(ids ...string) error {
	ls, err := t.read()
	if err != nil {
		return err
	}
	ticked := false
	on := false
	for i, l := range ls {
		if strings.HasPrefix(l, "## ") {
			on = l == "## Open questions"
		}
		if on && strings.HasPrefix(l, "- [ ]") && slices.ContainsFunc(ids, func(id string) bool { return strings.HasSuffix(l, questionMarker(id)) }) {
			ls[i] = "- [x]" + strings.TrimPrefix(l, "- [ ]")
			ticked = true
		}
	}
	if !ticked {
		return nil
	}
	return t.writeLines(ls)
}

// whom is a role as a sentence names it after "the".
func whom(role string) string { return strings.ReplaceAll(role, "-", " ") }

const questionsHeading = "## Open questions"

// withQuestions is the task file's lines with each question from its records in
// place of its item under Open questions, saying who asked it, who it waits
// on and what settled it. A question whose item is gone is shown at the end
// of the section. Other lines stay as they are: until crew decides from the
// records, an item typed into the section still counts.
func withQuestions(t Task, ls []string) []string {
	items, unread := t.Questions()
	if errors.Is(unread, errNotMigrated) || unread == nil && len(items) == 0 {
		return ls
	}
	start := slices.Index(ls, questionsHeading)
	if start < 0 {
		start = slices.Index(ls, "## Log")
		if start < 0 {
			start = len(ls)
		}
		ls = slices.Insert(ls, start, questionsHeading, "")
	}
	end := start + 1 + len(sectionLines(ls[start:], questionsHeading))
	if unread != nil {
		return slices.Concat(ls[:start+1], []string{"crew can't read the questions: " + unread.Error()}, ls[start+1:])
	}
	section := ls[start+1 : end]
	var body []string
	shown := map[string]bool{}
	for i := 0; i < len(section); i++ {
		j := slices.IndexFunc(items, func(q QuestionItem) bool {
			return (strings.HasPrefix(section[i], "- [ ]") || strings.HasPrefix(section[i], "- [x]")) && strings.HasSuffix(section[i], questionMarker(q.ID))
		})
		if j < 0 {
			body = append(body, section[i])
			continue
		}
		body = append(body, questionLines(items[j])...)
		shown[items[j].ID] = true
		for i+1 < len(section) && strings.HasPrefix(section[i+1], "  ") {
			i++
		}
	}
	body = trimBlankEnd(body)
	for _, q := range items {
		if !shown[q.ID] {
			body = append(body, questionLines(q)...)
		}
	}
	return slices.Concat(ls[:start+1], body, []string{""}, ls[end:])
}

// questionLines are a question as Markdown: its item, ticked once it's
// settled, then what kind of question it is, who asked it and when, whether it
// was handed on to the user, and its answers or who it still waits on.
func questionLines(q QuestionItem) []string {
	box := "[x]"
	if q.Open() {
		box = "[ ]"
	}
	out := strings.Split(strings.Replace(q.openQuestionsItem(), "- [ ]", "- "+box, 1), "\n")
	out = append(out[:len(out)-1], fmt.Sprintf("  - %s: asked by %s, %s", questionKindNames[q.Question.Kind], askerName(q.Question.Asker), stamp(q.Record)))
	if q.HandedOn != nil {
		out = append(out, fmt.Sprintf("  - handed on to the user %s: %s", stamp(*q.HandedOn), q.HandedOn.HandedOn.Why))
	}
	if q.Open() {
		return append(out, "  - waits on the "+whom(q.WaitsOn()))
	}
	for _, a := range q.Answers {
		lead := "  - settled " + stamp(a) + ":"
		if lines := strings.Split(a.Answer.Text, "\n"); len(lines) == 1 {
			out = append(out, lead+" "+lines[0])
		} else {
			out = append(out, lead)
			for _, l := range lines {
				out = append(out, strings.TrimRight("    "+l, " "))
			}
		}
	}
	return out
}

var questionKindNames = map[QuestionKind]string{
	QuestionWorker:       "worker's question",
	QuestionSplit:        "split proposal",
	QuestionParked:       "parked draft",
	QuestionCollaborator: "PR comment",
	QuestionNudge:        "stalled worker",
	QuestionRedCI:        "red CI past MAX_CI_FIXES",
}

// askerName is who asked a question, as a sentence names them: a role with
// "the", crew and a collaborator's @login as they are.
func askerName(asker string) string {
	if asker == "crew" || strings.HasPrefix(asker, "@") {
		return asker
	}
	return "the " + whom(asker)
}
