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

	ToLearn *ToLearn `json:"to_learn,omitempty"`
	Learned *Learned `json:"learned,omitempty"`
	Round   *Round   `json:"round,omitempty"`
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

// Round is a review round crew recorded on the task's PR, numbered as its
// heading in the task file is: the user's comments in it, and whether they
// asked for another automated review. A round with neither holds only crew's
// own reports, of red CI or a conflict with the base. crew records a round
// before it writes the round's text, so a run that stops in between records the
// same round again under its number, and the newer record is the round.
type Round struct {
	Number          int            `json:"number"`
	Comments        []RoundComment `json:"comments,omitempty"`
	ReviewRequested bool           `json:"review_requested,omitempty"`
}

// A comment of the user's in a review round: a review summary, a conversation
// comment or an inline comment, with its ID on GitHub and its text.
type RoundComment struct {
	Kind commentKind `json:"kind"`
	ID   string      `json:"id"`
	Path string      `json:"path,omitempty"` // an inline comment's file
	Line int         `json:"line,omitempty"` // and its line, or 0 when GitHub gave none
	Text string      `json:"text"`
}

// Feedback: the round holds the user's feedback, which MAX_REVIEW_ROUNDS
// limits, rather than only crew's reports.
func (r Round) Feedback() bool { return len(r.Comments) > 0 || r.ReviewRequested }

// Name is how the round's text names the comment, and crew reply and crew
// react take it: "review 7" or "comment 9".
func (cm RoundComment) Name() string {
	if cm.Kind == reviewSummary {
		return "review " + cm.ID
	}
	return "comment " + cm.ID
}

// Where is an inline comment's place in the diff, `a.go:3`, with ? for a line
// GitHub didn't give.
func (cm RoundComment) Where() string {
	line := "?"
	if cm.Line > 0 {
		line = strconv.Itoa(cm.Line)
	}
	return "`" + cm.Path + ":" + line + "`"
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
	toLearn := map[string]bool{} // whether the record with each ID adds an item
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		r, err := parseRecord([]byte(line))
		if _, taken := toLearn[r.ID]; err == nil && taken {
			err = fmt.Errorf("id %s is already taken by an earlier line", r.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, i+1, err)
		}
		toLearn[r.ID] = r.ToLearn != nil
		records = append(records, r)
	}
	// Checked once every line is in: a merge may put a record before the one it names.
	for i, r := range records {
		if r.Learned != nil && !toLearn[r.Learned.Item] {
			return nil, fmt.Errorf("%s:%d: it settles %s, which no line adds as an item to learn", name, i+1, r.Learned.Item)
		}
	}
	return records, nil
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
	for _, set := range []bool{r.ToLearn != nil, r.Learned != nil, r.Round != nil} {
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
	case r.Round != nil:
		return r.Round.check()
	}
	return nil
}

func (r Round) check() error {
	if r.Number < 1 {
		return errors.New("a round record needs its number")
	}
	for _, cm := range r.Comments {
		switch {
		case cm.Kind != reviewSummary && cm.Kind != convoComment && cm.Kind != inlineComment:
			return fmt.Errorf("a round's comment is a review, a comment or an inline comment, not %q", cm.Kind)
		case cm.ID == "":
			return errors.New("a round's comment needs its id")
		case (cm.Kind == inlineComment) != (cm.Path != ""):
			return errors.New("an inline comment, and only one, has a path")
		case cm.Line < 0:
			return errors.New("a round's comment has a negative line number")
		}
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
// records; before it they were checkboxes in the task file, which crew migrate
// moves into records. Format 2 records review rounds too; before it crew read
// them from the task file's text, which crew migrate reads into records. crew
// project add writes the file for a new project.
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
