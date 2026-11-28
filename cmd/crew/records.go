package main

// Records are typed facts about a ledger file, kept beside it in its records
// file: tasks/<id>.records.jsonl beside tasks/<id>.md. crew decides from them,
// never from the file's prose. Only crew commands write records, one JSON line
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
	// Round is the review round that sends a red_ci or base_conflict to the
	// worker. It's 0 for one crew migrate recorded, from a round the task has.
	Round int `json:"round,omitempty"`

	ToLearn      *ToLearn      `json:"to_learn,omitempty"`
	Learned      *Learned      `json:"learned,omitempty"`
	RedCI        *RedCI        `json:"red_ci,omitempty"`
	CICause      *CICause      `json:"ci_cause,omitempty"`
	BaseConflict *BaseConflict `json:"base_conflict,omitempty"`
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

// RedCI is red CI on a PR's head commit, which crew ci-fix sent the worker in a
// review round.
type RedCI struct {
	Head   string   `json:"head"`   // short SHA
	Checks []string `json:"checks"` // the checks that failed
}

// CICause is the worker's word on red CI crew sent it: whether its branch caused
// it, which crew ci-summary records. The latest one counts.
type CICause struct {
	RedCI        string `json:"red_ci"` // the RedCI record's ID
	BranchCaused bool   `json:"branch_caused"`
}

// BaseConflict is a PR's conflict with its base branch at a commit of the base,
// which crew catch-up sent the worker in a review round.
type BaseConflict struct {
	Base string `json:"base"` // the task's base branch
	At   string `json:"at"`   // the base's commit, short SHA
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
	if err := checkLedgerFormat(filepath.Dir(filepath.Dir(d.Path))); err != nil {
		return nil, err
	}
	return d.readRecords()
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
	kinds := map[string]string{} // the kind of the record with each ID
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		r, err := parseRecord([]byte(line))
		if _, taken := kinds[r.ID]; err == nil && taken {
			err = fmt.Errorf("id %s is already taken by an earlier line", r.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, i+1, err)
		}
		kinds[r.ID] = r.kind()
		records = append(records, r)
	}
	// Checked once every line is in: a merge may put a record before the one it names.
	for i, r := range records {
		if id, kind := r.follows(); id != "" && kinds[id] != kind {
			return nil, fmt.Errorf("%s:%d: it follows %s, which no line adds as %s", name, i+1, id, kind)
		}
	}
	return records, nil
}

// The kinds of record another record can follow. Each is also the words an
// error names it by.
const (
	kindToLearn = "an item to learn"
	kindRedCI   = "red CI"
)

// kind is the kind of record r is, when another record can follow it.
func (r Record) kind() string {
	switch {
	case r.ToLearn != nil:
		return kindToLearn
	case r.RedCI != nil:
		return kindRedCI
	}
	return ""
}

// follows is the ID of the record r follows, and the kind that record has to
// be, or "" for a record that follows none.
func (r Record) follows() (id, kind string) {
	switch {
	case r.Learned != nil:
		return r.Learned.Item, kindToLearn
	case r.CICause != nil:
		return r.CICause.RedCI, kindRedCI
	}
	return "", ""
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
	for _, set := range []bool{r.ToLearn != nil, r.Learned != nil, r.RedCI != nil, r.CICause != nil, r.BaseConflict != nil} {
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
	case r.RedCI != nil && r.RedCI.Head == "":
		return errors.New("a red_ci record needs its head")
	case r.CICause != nil && r.CICause.RedCI == "":
		return errors.New("a ci_cause record needs its red_ci")
	case r.BaseConflict != nil && (r.BaseConflict.Base == "" || r.BaseConflict.At == ""):
		return errors.New("a base_conflict record needs its base and its at")
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
// records, and format 2 the red CI and conflicts crew sent workers; before,
// they were checkboxes in the task file, and lines in its review rounds, which
// crew migrate moves into records. crew project add writes the file for a new
// project.
const ledgerFormat = 2

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
