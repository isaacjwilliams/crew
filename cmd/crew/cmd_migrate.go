package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
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
// Format 3 moves a task's decisions from its Decisions section into decided
// records, and its user checks into user_check records: the criteria its brief
// started "User check:", and those a decision of the user's named. The section
// keeps any line that wasn't part of a decision, under the template's comment;
// the criteria keep their words.
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
	learnComment, decisionsComment := sectionLines(template, learnHeading), sectionLines(template, decisionsHeading)
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
				n, err := migrateStillToLearn(t, learnComment)
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
			decisions, checks := 0, 0
			for _, t := range c.tasks(p) {
				d, u, err := migrateDecisions(t, decisionsComment)
				if err != nil {
					return fail("%s: %v", t.ID(), err)
				}
				decisions, checks = decisions+d, checks+u
			}
			fmt.Printf("%s: moved %d decision(s) and %d user check(s) into records\n", p, decisions, checks)
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
	if slices.ContainsFunc(records, func(r Record) bool { return r.By == "crew migrate" }) {
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
	out := dedent(under)
	if len(out) == 1 {
		return strings.TrimPrefix(out[0], "- ")
	}
	return strings.Join(out, "\n")
}

// dedent is lines without the indent they share.
func dedent(lines []string) []string {
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	var out []string
	for _, l := range lines {
		if len(l) >= indent && indent >= 0 {
			l = l[indent:]
		}
		out = append(out, l)
	}
	return out
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

// migrateDecisions moves the items under the task's Decisions into decided
// records, and its user checks into user_check records. It gives the section
// the template's comment, keeping any line that wasn't part of an item, and
// leaves the criteria as they are. It says how many decisions and user checks
// it moved.
//
// Like migrateStillToLearn, it writes the records in one go before it changes
// the file, so a run stopped in between is safe to run again: when crew migrate
// has written either kind of record already, it only rewrites the file.
func migrateDecisions(t Task, comment []string) (decisions, checks int, err error) {
	ls, err := t.read()
	if err != nil {
		return 0, 0, err
	}
	// A task's created dates only what has no date of its own, so a task
	// without one still migrates when nothing needs it.
	created, createdErr := time.Parse(time.RFC3339, t.Get("created"))
	found, rest := oldDecisions(sectionLines(ls, decisionsHeading), created)
	var moved []Record
	criteria := t.AcceptanceCriteria()
	for _, d := range found {
		moved = append(moved, Record{At: d.made, Decided: &d.Decided})
		decisions++
		if n := userCheckNamed(d); n >= 1 && n <= len(criteria) {
			moved = append(moved, Record{At: d.made, UserCheck: &UserCheck{Criterion: criteria[n-1], Why: d.Text}})
			checks++
		}
	}
	for _, c := range criteria {
		if strings.HasPrefix(c, "User check:") {
			moved = append(moved, Record{At: created, UserCheck: &UserCheck{Criterion: c, Why: "The brief started it \"User check:\"."}})
			checks++
		}
	}
	if createdErr != nil && slices.ContainsFunc(moved, func(r Record) bool { return r.At.IsZero() }) {
		return 0, 0, fmt.Errorf("its created isn't a time, to date a decision or user check that has no date by: %v", createdErr)
	}
	var records []Record
	if len(moved) > 0 {
		if records, err = t.readRecords(); err != nil {
			return 0, 0, err
		}
	}
	if slices.ContainsFunc(records, func(r Record) bool {
		return r.By == "crew migrate" && (r.Decided != nil || r.UserCheck != nil)
	}) {
		moved, decisions, checks = nil, 0, 0
	}
	for i, m := range moved {
		r := newRecord("crew migrate", records)
		r.At, r.Decided, r.UserCheck = m.At, m.Decided, m.UserCheck
		moved[i], records = r, append(records, r)
	}
	if len(moved) > 0 {
		if err := t.AddRecords(moved...); err != nil {
			return 0, 0, err
		}
	}
	if !slices.Contains(ls, decisionsHeading) {
		return decisions, checks, nil
	}
	body := trimBlankEnd(comment)
	if len(rest) > 0 {
		body = slices.Concat(body, []string{""}, rest)
	}
	return decisions, checks, t.ReplaceSection(decisionsHeading, strings.Join(body, "\n"))
}

// An oldDecision is a decision as a task's Decisions section kept it before
// records: a "- " item, with any lines indented under it.
type oldDecision struct {
	Decided
	made time.Time // the date at its start, or the task's created without one
}

var (
	// decisionDate is the date a decision's item starts with, and what parts
	// it from the text: "2026-11-24 — ", "2026-11-24 10:00: ".
	decisionDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?: \d{2}:\d{2})?(?:\s*(?:—|–|-|:)\s*|\s+|$)`)
	// callMark says whose call a decision was, in brackets: "(user)", or a
	// variant, "(user chose this option)", "(worker, round 1)".
	callMark       = `\((user|lead|worker)(?:[ ,;:][^()]*)?\)`
	callAtStart    = regexp.MustCompile(`^` + callMark)
	callAtLineEnd  = regexp.MustCompile(callMark + `\.?$`)
	exactCallStart = regexp.MustCompile(`^\((user|lead|worker)\)(?:\s*(?:—|–|-|:)\s*|\s+)`)
	exactCallEnd   = regexp.MustCompile(`\s*\((user|lead|worker)\)$`)
	// userCheckDecision is a decision of the user's that made a criterion
	// theirs to check: "Criterion 5 is a user check: …".
	userCheckDecision = regexp.MustCompile(`(?i)\bcriterion (\d+) is a user check\b`)
)

// oldDecisions splits a Decisions section, as task files kept it before
// records, into its decisions and the lines that weren't part of one, without
// their comments. A decision's text is its item's, without its date and
// without a mark of whose call it was that's exactly "(user)", "(lead)" or
// "(worker)" at its start or end. It was the user's call when any mark at the
// start of its text or the end of one of its lines names the user, and
// otherwise the last such mark's.
func oldDecisions(section []string, created time.Time) (found []oldDecision, rest []string) {
	plain := uncommented(section)
	for i := 0; i < len(plain); i++ {
		line := strings.TrimRight(plain[i], " \t")
		first, ok := strings.CutPrefix(line, "- ")
		if !ok {
			if strings.TrimSpace(line) != "" {
				rest = append(rest, line)
			}
			continue
		}
		end := i + 1 // past its last line
		for j := i + 1; j < len(plain); j++ {
			l := strings.TrimRight(plain[j], " \t")
			if l != "" && strings.TrimLeft(l, " \t") == l {
				break
			}
			if l != "" {
				end = j + 1
			}
		}
		under := plain[i+1 : end]
		for k := range under {
			under[k] = strings.TrimRight(under[k], " \t")
		}
		i = end - 1
		d := oldDecision{made: created}
		if m := decisionDate.FindStringSubmatch(first); m != nil {
			if day, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil {
				d.made, first = day, first[len(m[0]):]
			}
		}
		if m := callAtStart.FindStringSubmatch(first); m != nil {
			d.Call = m[1]
		}
		for _, l := range append([]string{first}, under...) {
			if m := callAtLineEnd.FindStringSubmatch(l); m != nil && d.Call != "user" {
				d.Call = m[1]
			}
		}
		lines := append([]string{exactCallStart.ReplaceAllString(first, "")}, dedent(under)...)
		lines[len(lines)-1] = exactCallEnd.ReplaceAllString(lines[len(lines)-1], "")
		if d.Text = strings.TrimSpace(strings.Join(lines, "\n")); d.Text == "" {
			d.Text = strings.TrimSpace(line[2:])
		}
		found = append(found, d)
	}
	return found, rest
}

// userCheckNamed is the number of the criterion a decision of the user's made
// theirs to check, or 0.
func userCheckNamed(d oldDecision) int {
	m := userCheckDecision.FindStringSubmatch(d.Text)
	if m == nil || d.Call != "user" {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
