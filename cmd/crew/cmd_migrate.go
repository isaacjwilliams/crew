package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// crew migrate brings each project's ledger to the format this crew reads
// (ledgerFormat), once, after an upgrade that changes it. Until then crew
// refuses to decide from the project's records.
//
// Format 1 moves still-to-learn items from checkboxes in the task file into
// records: each item becomes a to_learn record and, once ticked, a learned
// record with its answer. The section keeps any line that wasn't part of an
// item, under the template's comment.
//
// Format 2 records each review round the task file holds: its number and time,
// whether it holds the user's feedback, and their comments in it. The rounds'
// text stays as it is: it's what the worker was sent.
func cmdMigrate(c *Crew, args []string) error {
	if len(args) != 0 {
		return usage("crew migrate")
	}
	if err := c.lock(); err != nil {
		return err
	}
	// Committed however the run ends, so what it moved before a project stopped
	// it, or before an earlier run was cut short, isn't left out of the ledger.
	// crew upgrade, which runs it, needs a clean home to run again. Each write
	// leaves its file whole, so a second run carries on from where this one
	// stopped.
	defer c.commit("migrate the ledger to format " + fmt.Sprint(ledgerFormat))
	template, err := Task{Doc{c.path("templates", "task.md")}}.read()
	if err != nil {
		return err
	}
	comment := sectionLines(template, learnHeading)
	migrated := 0
	for _, p := range c.projects("") {
		pdir := c.path("projects", p)
		have, err := projectLedgerFormat(pdir)
		if err != nil {
			return fail("%v", err)
		}
		if have > ledgerFormat {
			return fail("%v", checkLedgerFormat(pdir))
		}
		if have == ledgerFormat {
			continue
		}
		items, rounds := 0, 0
		for _, t := range c.tasks(p) {
			if have < 1 {
				n, err := migrateStillToLearn(t, comment)
				if err != nil {
					return fail("%s: %v", t.ID(), err)
				}
				items += n
			}
			n, err := migrateRounds(t)
			if err != nil {
				return fail("%s: %v", t.ID(), err)
			}
			rounds += n
		}
		if err := writeLedgerFormat(pdir); err != nil {
			return err
		}
		if have < 1 {
			fmt.Printf("%s: moved %d item(s) still to learn into records\n", p, items)
		}
		fmt.Printf("%s: recorded %d review round(s)\n", p, rounds)
		migrated++
	}
	if migrated == 0 {
		fmt.Println("Every project's ledger is already migrated.")
	}
	return nil
}

// migrateStillToLearn moves the checkboxes under the task's Still to learn into
// records, and gives the section the template's comment, keeping any line that
// wasn't part of an item. It says how many items it moved.
//
// It writes the task's records in one go, and only then changes its file. A run
// stopped in between leaves both, so a task that has records crew migrate wrote
// has had its items moved already, and a second run only changes its file.
func migrateStillToLearn(t Task, comment []string) (int, error) {
	ls, err := t.read()
	if err != nil || !slices.Contains(ls, learnHeading) {
		return 0, err
	}
	items, rest := checkboxes(sectionLines(ls, learnHeading))
	var records []Record
	if len(items) > 0 {
		if records, err = t.readRecords(); err != nil {
			return 0, err
		}
	}
	if slices.ContainsFunc(records, func(r Record) bool { return r.By == "crew migrate" && (r.ToLearn != nil || r.Learned != nil) }) {
		items = nil
	}
	var moved []Record
	for _, i := range items {
		r := newRecord("crew migrate", records)
		r.ToLearn = &ToLearn{Text: i.text}
		records, moved = append(records, r), append(moved, r)
		if !i.ticked {
			continue
		}
		a := newRecord("crew migrate", records)
		a.Learned = &Learned{Item: r.ID, Answer: i.answer}
		records, moved = append(records, a), append(moved, a)
	}
	if len(moved) > 0 {
		if err := t.AddRecords(moved...); err != nil {
			return 0, err
		}
	}
	body := trimBlankEnd(comment)
	if len(rest) > 0 {
		body = slices.Concat(body, []string{""}, rest)
	}
	return len(items), t.ReplaceSection(learnHeading, strings.Join(body, "\n"))
}

// A checkbox is a still-to-learn item as a task file kept it before records.
type checkbox struct {
	text, answer string
	ticked       bool
}

// noAnswer settles a ticked item that had no answer written down.
const noAnswer = "Ticked in the task file, with no answer written."

// checkboxes splits a Still to learn section, as task files kept it before
// records, into its items and the lines that weren't part of one, without
// their comments. An item is a "- [ ]" or "- [x]" line. A ticked item's answer
// is the lines indented under it or, with none, what follows its first "? ":
// the lead wrote some answers on the item's own line, "Does it page? Yes: …".
// An open item has no answer, so any lines under it are kept.
func checkboxes(section []string) (items []checkbox, rest []string) {
	plain := uncommented(section)
	for i := 0; i < len(plain); i++ {
		line := strings.TrimRight(plain[i], " \t")
		text, ok := checklistItem(line)
		if !ok || strings.TrimSpace(text) == "" {
			if strings.TrimSpace(line) != "" {
				rest = append(rest, line)
			}
			continue
		}
		item := checkbox{text: strings.TrimSpace(text), ticked: !strings.HasPrefix(line, "- [ ]")}
		if !item.ticked {
			items = append(items, item)
			continue
		}
		var under []string
		for i+1 < len(plain) && (strings.TrimSpace(plain[i+1]) == "" || strings.HasPrefix(plain[i+1], " ") || strings.HasPrefix(plain[i+1], "\t")) {
			i++
			under = append(under, strings.TrimRight(plain[i], " \t"))
		}
		item.answer = answerFrom(trimBlankEnd(under))
		if item.answer == "" {
			question, answer, ok := strings.Cut(item.text, "? ")
			if ok {
				item.text, item.answer = question+"?", strings.TrimSpace(answer)
			} else {
				item.answer = noAnswer
			}
		}
		items = append(items, item)
	}
	return items, rest
}

// answerFrom is the answer the lines under a ticked item give: the lines
// without the indent they share, and a lone list item without its "- ".
func answerFrom(under []string) string {
	indent := -1
	for _, l := range under {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	var out []string
	for _, l := range under {
		if len(l) >= indent && indent >= 0 {
			l = l[indent:]
		}
		out = append(out, l)
	}
	if len(out) == 1 {
		return strings.TrimPrefix(out[0], "- ")
	}
	return strings.Join(out, "\n")
}

// migrateRounds records the review rounds in the task file, each as crew read
// it before rounds were records, and says how many it recorded. A round holds
// the user's feedback when it has a comment of theirs or asks for another
// automated review; other lines a person typed into a round aren't recorded.
// A task with round records has had its rounds recorded already.
func migrateRounds(t Task) (int, error) {
	var rounds []ReviewRound
	var cm *RoundComment // the comment whose quote the line may continue
	for _, l := range strings.Split(t.Section("## Review rounds"), "\n") {
		quote, isQuote := strings.CutPrefix(l, "  >")
		item, isItem := checklistItem(l)
		var round *ReviewRound // the round the line is in
		if len(rounds) > 0 {
			round = &rounds[len(rounds)-1]
		}
		switch rest, isHeading := strings.CutPrefix(l, "### Round "); {
		case isHeading:
			n, at := numberedHeading(rest)
			if n < 1 || at.IsZero() {
				return 0, fmt.Errorf("can't read the round heading %q", l)
			}
			rounds, cm = append(rounds, ReviewRound{Round{Number: n}, at}), nil
		case isItem && round != nil && strings.HasPrefix(item, "Automated review requested "):
			round.ReviewRequested, cm = true, nil
		case isItem && round != nil && isComment(item):
			comment, err := commentItem(item)
			if err != nil {
				return 0, err
			}
			round.Comments = append(round.Comments, comment)
			cm = &round.Comments[len(round.Comments)-1]
		case isQuote && cm != nil:
			if cm.Text != "" {
				cm.Text += "\n"
			}
			cm.Text += strings.TrimPrefix(quote, " ")
		default:
			cm = nil
		}
	}
	if len(rounds) == 0 {
		return 0, nil
	}
	records, err := t.readRecords()
	if err != nil || slices.ContainsFunc(records, func(r Record) bool { return r.Round != nil }) {
		return 0, err
	}
	var recorded []Record
	for _, round := range rounds {
		r := newRecord("crew migrate", slices.Concat(records, recorded))
		r.At, r.Round = round.At, &round.Round
		recorded = append(recorded, r)
	}
	return len(recorded), t.AddRecords(recorded...)
}

// isComment: the checklist item in a review round is one of the user's
// comments, as reviewRound.add wrote it: "review 7 (changes_requested)",
// "comment 10 — …" or "`a.go:3` — comment 9 — …".
func isComment(item string) bool {
	return strings.HasPrefix(item, "review ") || strings.HasPrefix(item, "comment ") || strings.Contains(item, " — comment ")
}

// commentItem reads a comment's checklist item in a review round.
func commentItem(item string) (RoundComment, error) {
	firstWord := func(s string) string { w, _, _ := strings.Cut(s, " "); return w }
	if rest, ok := strings.CutPrefix(item, "review "); ok {
		return RoundComment{Kind: reviewSummary, ID: firstWord(rest)}, nil
	}
	if rest, ok := strings.CutPrefix(item, "comment "); ok {
		return RoundComment{Kind: convoComment, ID: firstWord(rest)}, nil
	}
	where, rest, _ := strings.Cut(item, " — comment ")
	place, ok := strings.CutPrefix(where, "`")
	place, ok2 := strings.CutSuffix(place, "`")
	i := strings.LastIndex(place, ":")
	if !ok || !ok2 || i < 1 {
		return RoundComment{}, fmt.Errorf("can't read the place of the inline comment %q", item)
	}
	line := 0 // "?": GitHub gave the comment no line
	if n := place[i+1:]; n != "?" {
		var err error
		if line, err = strconv.Atoi(n); err != nil || line < 1 {
			return RoundComment{}, fmt.Errorf("can't read the line of the inline comment %q", item)
		}
	}
	return RoundComment{Kind: inlineComment, ID: firstWord(rest), Path: place[:i], Line: line}, nil
}
