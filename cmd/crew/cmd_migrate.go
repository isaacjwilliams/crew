package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
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
// Format 2 moves memory suggestions from checkboxes in memory-suggestions.md
// into records: each becomes a suggested record and, once ticked, a kept or
// declined record. The file keeps any line that wasn't a suggestion, under the
// head crew remember writes.
//
// Format 3 records each task's status history: a status record for each
// change its Log tells (migrateStatusHistory).
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
		if have < 1 {
			items := 0
			for _, t := range c.tasks(p) {
				n, err := migrateStillToLearn(t, comment)
				if err != nil {
					return fail("%s: %v", t.ID(), err)
				}
				items += n
			}
			fmt.Printf("%s: moved %d item(s) still to learn into records\n", p, items)
		}
		if have < 2 {
			n, err := migrateMemorySuggestions(c.suggestionsDoc(p), p)
			if err != nil {
				return fail("%s: %v", p, err)
			}
			fmt.Printf("%s: moved %d memory suggestion(s) into records\n", p, n)
		}
		if have < 3 {
			histories := 0
			for _, t := range c.tasks(p) {
				recorded, err := migrateStatusHistory(t)
				if err != nil {
					return fail("%s: %v", t.ID(), err)
				}
				if recorded {
					histories++
				}
			}
			fmt.Printf("%s: recorded the status history of %d task(s)\n", p, histories)
		}
		if err := writeLedgerFormat(pdir); err != nil {
			return err
		}
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
// stopped in between leaves both, so a task that has items crew migrate
// recorded has had them moved already, and a second run only changes its file.
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

// migrateMemorySuggestions moves the suggestions in a project's
// memory-suggestions.md into records, and gives the file the head crew remember
// writes, keeping any line that wasn't a suggestion. It says how many it moved.
//
// Like migrateStillToLearn, it writes the records in one go before it changes
// the file, so a run stopped in between is safe to run again: when crew migrate
// has written records already, it only rewrites the file.
func migrateMemorySuggestions(d Doc, project string) (int, error) {
	ls, err := d.read()
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	// Every checkbox line is a suggestion, as crew counted them before records,
	// read as it stands: crew remember wrote its text as given, "<!--" and all.
	var found []oldSuggestion
	others := slices.Clone(ls) // the file with its suggestions blanked out
	for i, l := range ls {
		l = strings.TrimRight(l, " \t")
		if _, ok := checklistItem(l); !ok {
			continue
		}
		o, err := readOldSuggestion(l)
		if err != nil {
			return 0, fmt.Errorf("%s:%d: %v", suggestionsFile, i+1, err)
		}
		found, others[i] = append(found, o), ""
	}
	// The other lines are kept, except the heading and comments. A comment is
	// looked for without the suggestions, so one's "<!--" can't hide a line.
	var rest []string
	for i, l := range uncommented(others) {
		if t := strings.TrimSpace(l); t != "" && t != "# Memory suggestions: "+project {
			rest = append(rest, ls[i])
		}
	}
	records, err := d.readRecords()
	if err != nil {
		return 0, err
	}
	if slices.ContainsFunc(records, func(r Record) bool { return r.By == "crew migrate" }) {
		found = nil
	}
	var moved []Record
	for _, o := range found {
		r := newRecord("crew migrate", records)
		r.At, r.Suggested = o.made, &o.Suggested
		records, moved = append(records, r), append(moved, r)
		if !o.kept && o.declined == "" {
			continue
		}
		settled := newRecord("crew migrate", records)
		if o.kept {
			settled.Kept = &Kept{Suggestion: r.ID}
		} else {
			settled.Declined = &Declined{Suggestion: r.ID, Reason: o.declined}
		}
		records, moved = append(records, settled), append(moved, settled)
	}
	if len(moved) > 0 {
		if err := d.AddRecords(moved...); err != nil {
			return 0, err
		}
	}
	body := suggestionsHead(project)
	if len(rest) > 0 {
		body += "\n" + strings.Join(rest, "\n") + "\n"
	}
	return len(found), replaceFile(d.Path, []byte(body))
}

// An oldSuggestion is a suggestion as memory-suggestions.md kept it before
// records: "- [ ] **<fact>** <why>. (<source>, <date>)", as crew remember wrote
// it, which the lead ticked "- [x]" with " → kept" or " → declined: <reason>"
// after it.
type oldSuggestion struct {
	Suggested
	made     time.Time // the date on the line
	kept     bool
	declined string // the reason, for one declined
}

var oldSuggestionLine = regexp.MustCompile(`^- \[([ xX])\] \*\*(.+?)\*\* (.+?)\. \(([^(),]+), (\d{4}-\d{2}-\d{2})\)(?: → (?:(kept)|declined: (.+)))?$`)

// readOldSuggestion reads a suggestion's line from before records.
func readOldSuggestion(line string) (oldSuggestion, error) {
	m := oldSuggestionLine.FindStringSubmatch(line)
	if m == nil {
		return oldSuggestion{}, fmt.Errorf("not a suggestion crew can read: %s", line)
	}
	o := oldSuggestion{Suggested: Suggested{Fact: m[2], Why: m[3], Source: m[4]}, kept: m[6] != "", declined: m[7]}
	if ticked := m[1] != " "; ticked != (o.kept || o.declined != "") {
		return o, fmt.Errorf("a ticked suggestion, and only a ticked one, ends \"→ kept\" or \"→ declined: <reason>\": %s", line)
	}
	var err error
	if o.made, err = time.ParseInLocation("2006-01-02", m[5], time.Local); err != nil {
		return o, fmt.Errorf("%v: %s", err, line)
	}
	return o, nil
}

// migrateStatusHistory records the task's status history from its Log: a
// status record for each "status → " line, at the line's time. It says whether
// it recorded any. A task that has status records already has its history, so
// a second run, after one cut short, leaves it as it is.
//
// A task dispatched before Task.SetStatus existed has status lines for some
// changes only: crew dispatch, crew followup and crew sync changed its status
// without one. So for such a task, each of their lines stands for the change
// it made too (statusLinesBeforeSetStatus).
func migrateStatusHistory(t Task) (bool, error) {
	var changes []logEntry
	for _, e := range statusLinesBeforeSetStatus(t.LogEntries()) {
		if _, ok := e.status(); ok {
			changes = append(changes, e)
		}
	}
	if len(changes) == 0 {
		return false, nil
	}
	records, err := t.readRecords()
	if err != nil || slices.ContainsFunc(records, func(r Record) bool { return r.Status != nil }) {
		return false, err
	}
	var history []Record
	for _, e := range changes {
		s, _ := e.status()
		r := newRecord("crew migrate", slices.Concat(records, history))
		r.At, r.Status = e.at, &StatusChanged{To: s}
		history = append(history, r)
	}
	return true, t.AddRecords(history...)
}

// statusLinesBeforeSetStatus adds the status lines a task dispatched before
// Task.SetStatus lacks, each after the line of the change it stands for: a
// "dispatched" line moved the task to working, a "followup (" line to its
// worker's turn, and "PR merged" to merged. Any other task's Log is returned
// as it is.
func statusLinesBeforeSetStatus(log []logEntry) []logEntry {
	if !dispatchedBeforeSetStatus(log) {
		return log
	}
	var out []logEntry
	for _, e := range log {
		out = append(out, e)
		var status Status
		switch {
		case strings.HasPrefix(e.msg, "dispatched"):
			status = Working
		case strings.HasPrefix(e.msg, "followup ("):
			status = AddressingFeedback // or working, after a note: the worker's turn either way
		case e.msg == "PR merged":
			status = Merged
		}
		if status != "" {
			out = append(out, logEntry{e.at, statusLogged + string(status)})
		}
	}
	return out
}

// dispatchedBeforeSetStatus: the task's "dispatched" line has no status line
// just before it, as crew dispatch writes one now.
func dispatchedBeforeSetStatus(log []logEntry) bool {
	i := slices.IndexFunc(log, func(e logEntry) bool { return strings.HasPrefix(e.msg, "dispatched") })
	if i < 0 {
		return false
	}
	if i == 0 {
		return true
	}
	s, ok := log[i-1].status()
	return !ok || s != Working
}
