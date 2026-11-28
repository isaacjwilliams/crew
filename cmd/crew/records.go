package main

// Records are typed facts about a ledger file, kept beside it in its records
// file: tasks/<id>.records.jsonl beside tasks/<id>.md, and
// memory-suggestions.records.jsonl beside a project's memory-suggestions.md.
// crew decides from them, never from the file's prose. Only crew commands write records, one JSON line
// each, appended under the ledger lock, and none is ever changed: a later fact
// is a new record that names the one it follows. Two clones that each append
// records merge without a conflict, since git's union driver keeps both sides'
// lines (.gitattributes).
//
// Reading is strict: a line crew can't read, such as a field from a newer crew
// or one typed by hand, fails the read, naming the line, so nothing decided
// from records ever rests on part of them.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A Record holds exactly one payload, one of the pointers below.
type Record struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	By string    `json:"by"` // the crew command that wrote it

	ToLearn   *ToLearn       `json:"to_learn,omitempty"`
	Learned   *Learned       `json:"learned,omitempty"`
	Suggested *Suggested     `json:"suggested,omitempty"`
	Kept      *Kept          `json:"kept,omitempty"`
	Declined  *Declined      `json:"declined,omitempty"`
	Status    *StatusChanged `json:"status,omitempty"`
}

// ToLearn adds an item to a draft's Still to learn: something its objective or
// gates rest on that nobody has checked yet.
type ToLearn struct {
	Text string `json:"text"`
}

// Learned settles a ToLearn item with its answer.
type Learned struct {
	Item   string `json:"item"` // the ToLearn record's ID
	Answer string `json:"answer"`
}

// Suggested adds a fact suggested for the project's memory.md, which waits on
// the user until Kept or Declined settles it.
type Suggested struct {
	Fact   string `json:"fact"`
	Why    string `json:"why"`
	Source string `json:"source"` // who suggested it, as ledgerSource checks it
}

// Kept settles a suggestion the user kept for memory.md.
type Kept struct {
	Suggestion string `json:"suggestion"` // the Suggested record's ID
}

// Declined settles a suggestion the user turned down, with why.
type Declined struct {
	Suggestion string `json:"suggestion"` // the Suggested record's ID
	Reason     string `json:"reason"`
}

// StatusChanged records that a task's status changed to To. Task.SetStatus
// writes one for every change, and crew reads a task's history from them
// (StatusHistory): whether it was handed over, when it merged, and so on.
type StatusChanged struct {
	To Status `json:"to"`
}

// newRecord is a record by the command `by`, with an ID no other record in
// records has, made now. The caller sets its payload.
func newRecord(by string, records []Record) Record {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		id := hex.EncodeToString(b)
		if !slices.ContainsFunc(records, func(r Record) bool { return r.ID == id }) {
			return Record{ID: id, At: time.Now().Truncate(time.Second), By: by}
		}
	}
}

// RecordsPath is the records file beside the ledger file.
func (d Doc) RecordsPath() string { return strings.TrimSuffix(d.Path, ".md") + ".records.jsonl" }

// Records are the file's records, oldest first, or none when it has no records
// file. It fails on any line it can't read, naming the line, and when the
// project's ledger isn't in the format this crew reads (ledgerFormat).
func (d Doc) Records() ([]Record, error) {
	if err := checkLedgerFormat(d.projectDir()); err != nil {
		return nil, err
	}
	return d.readRecords()
}

// projectDir is the project the file belongs to: projects/<p>/, which holds
// memory-suggestions.md and the folders of tasks, goals and proposals.
func (d Doc) projectDir() string {
	if filepath.Base(d.Path) == suggestionsFile {
		return filepath.Dir(d.Path)
	}
	return filepath.Dir(filepath.Dir(d.Path))
}

// readRecords are the records in the file, whatever the project's format.
// crew migrate calls it, to add records to a project that isn't in the format
// yet; everything else calls Records.
func (d Doc) readRecords() ([]Record, error) {
	b, err := os.ReadFile(d.RecordsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	name := filepath.Base(d.RecordsPath())
	var records []Record
	adds := map[string]string{} // what the record with each ID adds, if anything
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		r, err := parseRecord([]byte(line))
		if _, taken := adds[r.ID]; err == nil && taken {
			err = fmt.Errorf("id %s is already taken by an earlier line", r.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, i+1, err)
		}
		adds[r.ID] = r.adds()
		records = append(records, r)
	}
	// Checked once every line is in: a merge may put a record before the one it names.
	for i, r := range records {
		if item, what := r.settles(); what != "" && adds[item] != what {
			return nil, fmt.Errorf("%s:%d: it settles %s, which no line adds as %s", name, i+1, item, what)
		}
	}
	return records, nil
}

// The kinds of item records add. Each is also the words an error names it by.
const (
	kindToLearn    = "an item to learn"
	kindSuggestion = "a memory suggestion"
)

// adds is the kind of item the record adds for a later record to settle, or ""
// when it adds nothing.
func (r Record) adds() string {
	switch {
	case r.ToLearn != nil:
		return kindToLearn
	case r.Suggested != nil:
		return kindSuggestion
	}
	return ""
}

// settles is the ID of the item the record settles, and that item's kind; what
// is "" for a record that settles nothing.
func (r Record) settles() (item, what string) {
	switch {
	case r.Learned != nil:
		return r.Learned.Item, kindToLearn
	case r.Kept != nil:
		return r.Kept.Suggestion, kindSuggestion
	case r.Declined != nil:
		return r.Declined.Suggestion, kindSuggestion
	}
	return "", ""
}

// An Item is something a record adds for later records to settle, an item
// still to learn or a memory suggestion, and the records that settle it, oldest
// first. Two clones may each settle it, so it can have more than one. It's open
// until one does.
type Item struct {
	Record
	Settled []Record
}

func (i Item) Open() bool { return len(i.Settled) == 0 }

// items are the items of one kind the records add, in the order they were
// added, each with what settles it. Records from readRecords settle only items
// of the kind they name, which it checks.
func items(records []Record, kind string) []Item {
	var items []Item
	for _, r := range records {
		if r.adds() == kind {
			items = append(items, Item{Record: r})
		}
	}
	for _, r := range records {
		item, _ := r.settles()
		if i := slices.IndexFunc(items, func(i Item) bool { return i.ID == item }); item != "" && i >= 0 {
			items[i].Settled = append(items[i].Settled, r)
		}
	}
	return items
}

// openItems counts the items that are still open.
func openItems(items []Item) int {
	n := 0
	for _, i := range items {
		if i.Open() {
			n++
		}
	}
	return n
}

// parseRecord reads one line of a records file, refusing a field it doesn't
// know, more or less than one payload, and a payload missing what it needs.
func parseRecord(line []byte) (Record, error) {
	var r Record
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("not a record crew can read: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return r, errors.New("not a record crew can read: more than one value on the line")
	}
	return r, r.check()
}

// check says what's missing from a record or wrong with it.
func (r Record) check() error {
	if r.ID == "" || r.At.IsZero() || r.By == "" {
		return errors.New("a record needs an id, an at and a by")
	}
	payloads := 0
	for _, set := range []bool{r.ToLearn != nil, r.Learned != nil, r.Suggested != nil, r.Kept != nil, r.Declined != nil, r.Status != nil} {
		if set {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("a record holds exactly one payload, not %d", payloads)
	}
	switch {
	case r.ToLearn != nil && strings.TrimSpace(r.ToLearn.Text) == "":
		return errors.New("a to_learn record needs its text")
	case r.Learned != nil && (r.Learned.Item == "" || strings.TrimSpace(r.Learned.Answer) == ""):
		return errors.New("a learned record needs its item and its answer")
	case r.Suggested != nil && (strings.TrimSpace(r.Suggested.Fact) == "" || strings.TrimSpace(r.Suggested.Why) == "" || r.Suggested.Source == ""):
		return errors.New("a suggested record needs its fact, its why and its source")
	case r.Kept != nil && r.Kept.Suggestion == "":
		return errors.New("a kept record needs its suggestion")
	case r.Declined != nil && (r.Declined.Suggestion == "" || strings.TrimSpace(r.Declined.Reason) == ""):
		return errors.New("a declined record needs its suggestion and its reason")
	case r.Status != nil && !slices.Contains(taskStatuses, r.Status.To):
		return fmt.Errorf("a status record needs a task's status, not %q", r.Status.To)
	}
	return nil
}

// AddRecords adds records after the file's records, in one write, so a reader
// never sees some of them without the rest. The caller holds the ledger lock and
// commits.
func (d Doc) AddRecords(rs ...Record) error {
	lines, err := os.ReadFile(d.RecordsPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, r := range rs {
		if err := r.check(); err != nil {
			return err
		}
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		lines = append(append(lines, b...), '\n')
	}
	return replaceFile(d.RecordsPath(), lines)
}

// ledgerFormat is the format of the ledger this crew reads, which a project
// records in its ledger-format file. Format 1 keeps still-to-learn items in
// records, format 2 memory suggestions and format 3 each task's status history.
// Before, the items and suggestions were checkboxes in the task file and in
// memory-suggestions.md, and the history was told only by the task's Log; crew
// migrate moves them into records. crew project add writes the file for a new
// project.
const ledgerFormat = 3

func ledgerFormatPath(projectDir string) string { return filepath.Join(projectDir, "ledger-format") }

// projectLedgerFormat is the format the project's ledger is in: 0 before its
// first crew migrate.
func projectLedgerFormat(projectDir string) (int, error) {
	b, err := os.ReadFile(ledgerFormatPath(projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("%s holds %q, not a format number", ledgerFormatPath(projectDir), strings.TrimSpace(string(b)))
	}
	return n, nil
}

// errNotMigrated is why crew can't read a project's records before crew migrate.
var errNotMigrated = errors.New("isn't migrated to this crew's records yet: run crew migrate")

// checkLedgerFormat fails unless the project's ledger is in ledgerFormat: before
// crew migrate, its records are missing what the task files still hold.
func checkLedgerFormat(projectDir string) error {
	have, err := projectLedgerFormat(projectDir)
	if err != nil {
		return err
	}
	project := filepath.Base(projectDir)
	switch {
	case have < ledgerFormat:
		return fmt.Errorf("%s's ledger %w", project, errNotMigrated)
	case have > ledgerFormat:
		return fmt.Errorf("%s's ledger is from a newer crew: run crew upgrade", project)
	}
	return nil
}

// writeLedgerFormat records that the project's ledger is in ledgerFormat.
func writeLedgerFormat(projectDir string) error {
	return replaceFile(ledgerFormatPath(projectDir), []byte(strconv.Itoa(ledgerFormat)+"\n"))
}
