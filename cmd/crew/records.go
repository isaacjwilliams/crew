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

	ToLearn  *ToLearn  `json:"to_learn,omitempty"`
	Learned  *Learned  `json:"learned,omitempty"`
	Question *Question `json:"question,omitempty"`
	HandedOn *HandedOn `json:"handed_on,omitempty"`
	Answer   *Answer   `json:"answer,omitempty"`
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

// A Question waits on someone's answer: the asker's, of its kind, for the one
// it waits on.
type Question struct {
	Kind  QuestionKind `json:"kind"`
	Asker string       `json:"asker"` // a role (worker, lead, goal-lead or crew), or a collaborator's @login
	For   string       `json:"for"`   // who answers it: user, lead or goal-lead
	Text  string       `json:"text"`
	Head  string       `json:"head,omitempty"` // red CI's only: the commit CI was red on
}

// QuestionKind says what made a question.
type QuestionKind string

const (
	QuestionWorker       QuestionKind = "worker"       // a worker needs a decision
	QuestionSplit        QuestionKind = "split"        // a worker proposes smaller pieces for its branch
	QuestionParked       QuestionKind = "parked"       // a lead left a draft for the user
	QuestionCollaborator QuestionKind = "collaborator" // a collaborator commented on the PR
	QuestionNudge        QuestionKind = "nudge"        // a worker stalled again, or its worktree is gone
	QuestionRedCI        QuestionKind = "red-ci"       // CI is still red past MAX_CI_FIXES
)

var (
	questionKinds = []QuestionKind{QuestionWorker, QuestionSplit, QuestionParked, QuestionCollaborator, QuestionNudge, QuestionRedCI}
	askers        = []string{"worker", "lead", "goal-lead", "crew"}
	answerers     = []string{"user", "lead", "goal-lead"}
)

// HandedOn leaves a question for the user: a goal lead hands on a question it
// won't answer itself.
type HandedOn struct {
	Question string `json:"question"` // the Question record's ID
	Why      string `json:"why"`
}

// Answer settles a question: with the answer someone gave, or, for crew
// settle's, with what showed the worker moved on.
type Answer struct {
	Question string `json:"question"` // the Question record's ID
	Text     string `json:"text"`
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
	byID := map[string]Record{}
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		r, err := parseRecord([]byte(line))
		if _, taken := byID[r.ID]; err == nil && taken {
			err = fmt.Errorf("id %s is already taken by an earlier line", r.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, i+1, err)
		}
		byID[r.ID] = r
		records = append(records, r)
	}
	// Checked once every line is in: a merge may put a record before the one it names.
	for i, r := range records {
		var err error
		switch {
		case r.Learned != nil && byID[r.Learned.Item].ToLearn == nil:
			err = fmt.Errorf("it settles %s, which no line adds as an item to learn", r.Learned.Item)
		case r.HandedOn != nil && byID[r.HandedOn.Question].Question == nil:
			err = fmt.Errorf("it hands on %s, which no line asks as a question", r.HandedOn.Question)
		case r.Answer != nil && byID[r.Answer.Question].Question == nil:
			err = fmt.Errorf("it answers %s, which no line asks as a question", r.Answer.Question)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, i+1, err)
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
	for _, set := range []bool{r.ToLearn != nil, r.Learned != nil, r.Question != nil, r.HandedOn != nil, r.Answer != nil} {
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
	case r.Question != nil:
		return r.Question.check()
	case r.HandedOn != nil && (r.HandedOn.Question == "" || strings.TrimSpace(r.HandedOn.Why) == ""):
		return errors.New("a handed_on record needs its question and why")
	case r.Answer != nil && (r.Answer.Question == "" || strings.TrimSpace(r.Answer.Text) == ""):
		return errors.New("an answer record needs its question and its text")
	}
	return nil
}

// check says what's missing from a question or wrong with it. Only a
// collaborator's comment is asked by someone outside the crew, so only it names
// an @login; only red CI names the commit it was red on.
func (q Question) check() error {
	switch {
	case !slices.Contains(questionKinds, q.Kind):
		return fmt.Errorf("a question's kind is one of %v, not %q", questionKinds, q.Kind)
	case q.Kind == QuestionCollaborator && (len(q.Asker) < 2 || q.Asker[0] != '@'):
		return fmt.Errorf("a collaborator's question is asked by an @login, not %q", q.Asker)
	case q.Kind != QuestionCollaborator && !slices.Contains(askers, q.Asker):
		return fmt.Errorf("a %s question is asked by one of %v, not %q", q.Kind, askers, q.Asker)
	case !slices.Contains(answerers, q.For):
		return fmt.Errorf("a question is for one of %v, not %q", answerers, q.For)
	case strings.TrimSpace(q.Text) == "":
		return errors.New("a question needs its text")
	case (q.Kind == QuestionRedCI) != (q.Head != ""):
		return errors.New("a question names the head it's about if, and only if, it's about red CI")
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
// moves into records. crew project add writes the file for a new project.
const ledgerFormat = 1

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
