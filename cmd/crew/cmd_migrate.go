package main

import (
	"fmt"
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
// Format 2 records the red CI and the conflicts with the base that crew sent
// workers in review rounds, which the rounds' lines were the only record of:
// each "CI failed on" line becomes a red_ci record, with a ci_cause record when
// it ends "(the branch didn't cause it)", and each "Conflicts with" line a
// base_conflict record. The task files are left as they are.
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
			redCI, conflicts := 0, 0
			for _, t := range c.tasks(p) {
				r, k, err := migrateRoundReports(t)
				if err != nil {
					return fail("%s: %v", t.ID(), err)
				}
				redCI, conflicts = redCI+r, conflicts+k
			}
			fmt.Printf("%s: recorded %d red CI report(s) and %d conflict(s) from review rounds\n", p, redCI, conflicts)
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

// ciNotTheBranch ended a round's CI line, before format 2, once the worker had
// said its branch didn't cause the red CI.
const ciNotTheBranch = " (the branch didn't cause it)"

// migrateRoundReports records the red CI and conflicts the task's review rounds
// report, as crew read them from the rounds' lines before format 2, and says
// how many of each it recorded. Each record is dated by its round's heading.
//
// It writes the task's records in one go. No command writes red_ci or
// base_conflict records before format 2, so a task that has one has been
// migrated already, by a run that stopped before the project's format.
func migrateRoundReports(t Task) (redCI, conflicts int, err error) {
	rounds := t.Section("## Review rounds")
	if rounds == "" {
		return 0, 0, nil
	}
	records, err := t.readRecords()
	if err != nil {
		return 0, 0, err
	}
	if slices.ContainsFunc(records, func(r Record) bool { return r.RedCI != nil || r.BaseConflict != nil }) {
		return 0, 0, nil
	}
	var moved []Record
	record := func(at time.Time, fill func(*Record)) {
		r := newRecord("crew migrate", records)
		if !at.IsZero() {
			r.At = at
		}
		fill(&r)
		records, moved = append(records, r), append(moved, r)
	}
	var roundAt time.Time
	for _, l := range strings.Split(rounds, "\n") {
		if rest, ok := strings.CutPrefix(l, "### Round "); ok {
			_, roundAt = numberedHeading(rest)
			continue
		}
		item, _ := checklistItem(l)
		if rest, ok := strings.CutPrefix(item, ciFailedOn); ok {
			head, checks, _ := strings.Cut(strings.TrimSuffix(rest, ciNotTheBranch), " ")
			report := &RedCI{Head: strings.TrimSuffix(head, ":")}
			if checks != "" {
				report.Checks = strings.Split(checks, ", ")
			}
			record(roundAt, func(r *Record) { r.RedCI = report })
			redCI++
			if strings.HasSuffix(l, ciNotTheBranch) {
				id := moved[len(moved)-1].ID
				record(roundAt, func(r *Record) { r.CICause = &CICause{RedCI: id, BranchCaused: false} })
			}
		}
		if rest, ok := strings.CutPrefix(item, conflictsWith); ok {
			if base, at, _ := strings.Cut(rest, " at "); base != "" && at != "" {
				record(roundAt, func(r *Record) { r.BaseConflict = &BaseConflict{Base: base, At: at} })
				conflicts++
			}
		}
	}
	if len(moved) == 0 {
		return 0, 0, nil
	}
	return redCI, conflicts, t.AddRecords(moved...)
}
