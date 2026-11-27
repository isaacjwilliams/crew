package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// learnTask is a task file in a project whose ledger is in this crew's format.
func learnTask(t *testing.T, content string) Task {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "projects", "p")
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerFormat(dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "tasks", "001-a.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

// add appends a record, as the crew command that writes it does, and returns it.
func add(t *testing.T, task Task, r Record) Record {
	t.Helper()
	records, _ := task.Records()
	n := newRecord("test", records)
	n.ToLearn, n.Learned, n.Question, n.HandedOn, n.Answer = r.ToLearn, r.Learned, r.Question, r.HandedOn, r.Answer
	if err := task.AddRecords(n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStillToLearnReadsRecordsAlone(t *testing.T) {
	// Checkboxes typed into the section count for nothing.
	task := learnTask(t, "## Still to learn\n- [ ] typed by hand\n- [x] and ticked\n\n## Log\n")
	if items, err := task.StillToLearn(); err != nil || len(items) != 0 {
		t.Fatalf("StillToLearn with only typed lines = %v, %v; want none", items, err)
	}
	a := add(t, task, Record{ToLearn: &ToLearn{"Does the API page?"}})
	b := add(t, task, Record{ToLearn: &ToLearn{"Is a scan fast enough?"}})
	add(t, task, Record{Learned: &Learned{Item: b.ID, Answer: "Yes, under 50 ms."}})
	add(t, task, Record{Learned: &Learned{Item: b.ID, Answer: "Yes."}})
	items, err := task.StillToLearn()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != a.ID || !items[0].Open() || items[1].ID != b.ID || len(items[1].Answers) != 2 || countOpen(items) != 1 {
		t.Errorf("StillToLearn = %+v; want %s open, then %s with both answers", items, a.ID, b.ID)
	}
}

func TestRecordsRefuseWhatTheyCantRead(t *testing.T) {
	good := `{"id":"aaaaaaaa","at":"2026-11-25T10:00:00Z","by":"crew to-learn","to_learn":{"text":"Q?"}}`
	for line, want := range map[string]string{
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q?"},"extra":1}`:                                                  `unknown field "extra"`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q?","why":"newer"}}`:                                              `unknown field "why"`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x"}`:                                                                                     "not 0",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"},"learned":{"item":"aaaaaaaa","answer":"A"}}`:                  "not 2",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":" "}}`:                                                             "needs its text",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","learned":{"item":"aaaaaaaa","answer":""}}`:                                           "needs its item and its answer",
		`{"id":"bbbbbbbb","by":"crew x","to_learn":{"text":"Q"}}`:                                                                                         "needs an id, an at and a by",
		`{"id":"aaaaaaaa","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"}}`:                                                             "already taken",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","learned":{"item":"cccccccc","answer":"A"}}`:                                          "settles cccccccc, which no line adds",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"}} {}`:                                                          "more than one value",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"worry","asker":"worker","for":"user","text":"Q?"}}`:               `kind is one of`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"collaborator","asker":"alice","for":"user","text":"Q?"}}`:         `asked by an @login, not "alice"`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"worker","asker":"@alice","for":"user","text":"Q?"}}`:              `a worker question is asked by one of`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"worker","asker":"worker","for":"worker","text":"Q?"}}`:            `is for one of`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"worker","asker":"worker","for":"user","text":" "}}`:               "a question needs its text",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"red-ci","asker":"crew","for":"user","text":"Q?"}}`:                "the head it's about",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","question":{"kind":"worker","asker":"worker","for":"user","text":"Q?","head":"abc"}}`: "the head it's about",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","handed_on":{"question":"aaaaaaaa","why":"W"}}`:                                       "hands on aaaaaaaa, which no line asks",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","answer":{"question":"cccccccc","text":"A"}}`:                                         "answers cccccccc, which no line asks",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","answer":{"question":"aaaaaaaa","text":""}}`:                                          "needs its question and its text",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","handed_on":{"question":"aaaaaaaa","why":""}}`:                                        "needs its question and why",
		`- [ ] typed by hand`: "not a record crew can read",
		``:                    "not a record crew can read",
	} {
		task := learnTask(t, "## Log\n")
		if err := os.WriteFile(task.RecordsPath(), []byte(good+"\n"+line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := task.StillToLearn()
		if err == nil || !strings.Contains(err.Error(), "001-a.records.jsonl:2: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("StillToLearn with %s = %v; want an error at line 2 saying %q", line, err, want)
		}
	}
}

func TestQuestionsReadRecordsAlone(t *testing.T) {
	// An item typed by hand is no question record.
	task := learnTask(t, "## Open questions\n- [ ] typed by hand\n\n## Log\n")
	if items, err := task.Questions(); err != nil || len(items) != 0 {
		t.Fatalf("Questions with only typed lines = %v, %v; want none", items, err)
	}
	rs, err := task.ask("test",
		Question{Kind: QuestionWorker, Asker: "worker", For: "goal-lead", Text: "Which index?"},
		Question{Kind: QuestionCollaborator, Asker: "@alice", For: "user", Text: "@alice commented on PR #7\n> Why not a scan?\n>\n> Just asking."},
		Question{Kind: QuestionNudge, Asker: "crew", For: "lead", Text: "Its worker's turn ended mid-task again."},
	)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := rs[0], rs[1], rs[2]
	add(t, task, Record{HandedOn: &HandedOn{Question: a.ID, Why: "a gate the worker can't run"}})
	add(t, task, Record{Answer: &Answer{Question: c.ID, Text: "Moved on."}})
	items, err := task.Questions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, q := range items {
		got = append(got, fmt.Sprintf("%s open=%v waits=%s", q.ID, q.Open(), q.WaitsOn()))
	}
	want := []string{a.ID + " open=true waits=user", b.ID + " open=true waits=user", c.ID + " open=false waits=lead"}
	if !slices.Equal(got, want) {
		t.Errorf("Questions = %v; want %v", got, want)
	}

	// Until crew decides from the records, each question is an item under Open
	// questions too, which crew counts.
	if err := task.tickQuestions(c.ID); err != nil {
		t.Fatal(err)
	}
	if n := task.OpenQuestions(); n != 3 {
		t.Errorf("OpenQuestions = %d; want the typed item and two crew asked", n)
	}
	wantSection := "- [ ] typed by hand\n\n" +
		"- [ ] Which index? (question " + a.ID + ")\n" +
		"- [ ] @alice commented on PR #7 (question " + b.ID + ")\n  > Why not a scan?\n  >\n  > Just asking.\n" +
		"- [x] Its worker's turn ended mid-task again. (question " + c.ID + ")"
	if got := strings.Join(sectionLines(mustRead(t, task), "## Open questions"), "\n"); strings.TrimSpace(got) != wantSection {
		t.Errorf("Open questions =\n%s\nwant\n%s", got, wantSection)
	}
}

func TestShownTaskQuestions(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	task := learnTask(t, "# A\n\n## Open questions\n<!-- Questions. -->\n- [ ] typed by hand\n"+
		"- [ ] Which index? (question aaaaaaaa)\n"+
		"- [x] @alice commented on PR #7 (question bbbbbbbb)\n  > Why?\n\n## Log\n- x\n")
	for _, r := range []Record{
		{ID: "aaaaaaaa", At: at, By: "crew ask", Question: &Question{Kind: QuestionWorker, Asker: "worker", For: "goal-lead", Text: "Which index?"}},
		{ID: "bbbbbbbb", At: at, By: "crew feedback", Question: &Question{Kind: QuestionCollaborator, Asker: "@alice", For: "user", Text: "@alice commented on PR #7\n> Why?"}},
		{ID: "cccccccc", At: at, By: "crew ci-fix", Question: &Question{Kind: QuestionRedCI, Asker: "crew", For: "user", Text: "CI is still red on abc1234.", Head: "abc1234"}},
		{ID: "dddddddd", At: at.Add(time.Hour), By: "crew hand-on", HandedOn: &HandedOn{Question: "aaaaaaaa", Why: "a gate it can't run"}},
		{ID: "eeeeeeee", At: at.Add(2 * time.Hour), By: "crew answer", Answer: &Answer{Question: "bbbbbbbb", Text: "Because:\n- it's faster"}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := shownTask(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# A\n\n## Open questions\n<!-- Questions. -->\n- [ ] typed by hand\n" +
		"- [ ] Which index? (question aaaaaaaa)\n" +
		"  - worker's question: asked by the worker, 2026-11-25 10:00 by crew ask\n" +
		"  - handed on to the user 2026-11-25 11:00 by crew hand-on: a gate it can't run\n" +
		"  - waits on the user\n" +
		"- [x] @alice commented on PR #7 (question bbbbbbbb)\n  > Why?\n" +
		"  - PR comment: asked by @alice, 2026-11-25 10:00 by crew feedback\n" +
		"  - settled 2026-11-25 12:00 by crew answer:\n    Because:\n    - it's faster\n" +
		"- [ ] CI is still red on abc1234. (question cccccccc)\n" +
		"  - red CI past MAX_CI_FIXES: asked by crew, 2026-11-25 10:00 by crew ci-fix\n" +
		"  - waits on the user\n" +
		"\n## Log\n- x\n"
	if got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
	}
}

// mustRead is a task file's lines.
func mustRead(t *testing.T, task Task) []string {
	t.Helper()
	ls, err := task.read()
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

// crew migrate writes a task's records, then its file, then the project's
// format. Each replaces the file whole, so a write that fails partway leaves the
// old file, never part of the new one: a reader holding the old file open still
// reads it as it was, and no temporary file is left beside it.
func TestMigrationWritesReplaceTheFileWhole(t *testing.T) {
	task := learnTask(t, "## Still to learn\n- [ ] Open?\n")
	add(t, task, Record{ToLearn: &ToLearn{Text: "First?"}})
	second := newRecord("test", nil)
	second.ToLearn = &ToLearn{Text: "Second?"}
	project := filepath.Dir(filepath.Dir(task.Path))
	if err := os.WriteFile(ledgerFormatPath(project), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		name, path string
		write      func() error
	}{
		{"AddRecords", task.RecordsPath(), func() error { return task.AddRecords(second) }},
		{"ReplaceSection", task.Path, func() error { return task.ReplaceSection(learnHeading, "") }},
		{"writeLedgerFormat", ledgerFormatPath(project), func() error { return writeLedgerFormat(project) }},
	} {
		before, _ := os.ReadFile(w.path)
		f, err := os.Open(w.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.write(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		kept := make([]byte, len(before)+100)
		n, _ := f.Read(kept)
		f.Close()
		if string(kept[:n]) != string(before) {
			t.Errorf("%s wrote into the file: one opened before it reads\n%s\nwant\n%s", w.name, kept[:n], before)
		}
		if left, _ := filepath.Glob(w.path + ".*"); len(left) > 0 {
			t.Errorf("%s left %v", w.name, left)
		}
	}
}

func TestReplaceFileGivesTheModeWriteFileWould(t *testing.T) {
	for _, c := range []struct {
		name      string
		umask     int
		old, want os.FileMode // old is 0 for no file yet
	}{
		{"an old file's mode, under a narrower umask", 0o077, 0o640, 0o640},
		{"a private file's mode, under a wider umask", 0o022, 0o600, 0o600},
		{"a new file, under the usual umask", 0o022, 0, 0o644},
		{"a new file, under a narrower umask", 0o077, 0, 0o600},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task.md")
			if c.old != 0 {
				if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, c.old); err != nil {
					t.Fatal(err)
				}
			}
			defer syscall.Umask(syscall.Umask(c.umask))

			f, err := createReplacement(path)
			if err != nil {
				t.Fatal(err)
			}
			fi, err := f.Stat()
			f.Close()
			os.Remove(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != c.want {
				t.Errorf("replacement made at %v before it's written, want %v", fi.Mode().Perm(), c.want)
			}

			if err := replaceFile(path, []byte("new\n")); err != nil {
				t.Fatal(err)
			}
			fi, err = os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != c.want {
				t.Errorf("file left at %v, want %v", fi.Mode().Perm(), c.want)
			}
		})
	}
}

func TestLedgerFormat(t *testing.T) {
	task := learnTask(t, "## Log\n")
	marker := ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path)))
	for content, want := range map[string]string{
		"":    "p's ledger isn't migrated to this crew's records yet: run crew migrate",
		"0\n": "run crew migrate",
		"2\n": "p's ledger is from a newer crew: run crew upgrade",
		"x\n": `holds "x", not a format number`,
	} {
		if content == "" {
			os.Remove(marker)
		} else if err := os.WriteFile(marker, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := task.StillToLearn(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("StillToLearn with ledger-format %q = %v; want %q", content, err, want)
		}
	}
}

func TestShownTask(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	write := func(task Task, records ...Record) {
		for _, r := range records {
			if err := task.AddRecords(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	task := learnTask(t, "# A\n\n## Still to learn\n<!-- Items: crew to-learn.\n     More. -->\n- [ ] typed by hand\n\n## Plan\n\n## Log\n- x\n")
	write(task,
		Record{ID: "aaaaaaaa", At: at, By: "crew to-learn", ToLearn: &ToLearn{"Does the API page?"}},
		Record{ID: "bbbbbbbb", At: at, By: "crew migrate", ToLearn: &ToLearn{"Which index?"}},
		Record{ID: "cccccccc", At: at.Add(time.Hour), By: "crew learned", Learned: &Learned{"bbbbbbbb", "The existing one."}},
		Record{ID: "dddddddd", At: at.Add(2 * time.Hour), By: "crew learned", Learned: &Learned{"bbbbbbbb", "Either:\n- the existing one\n- a new one"}},
	)
	got, err := shownTask(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# A\n\n## Still to learn\n<!-- Items: crew to-learn.\n     More. -->\n" +
		"- [ ] Does the API page? (aaaaaaaa, added 2026-11-25 10:00 by crew to-learn)\n" +
		"- [x] Which index? (bbbbbbbb, added 2026-11-25 10:00 by crew migrate)\n" +
		"  - settled 2026-11-25 11:00 by crew learned: The existing one.\n" +
		"  - settled 2026-11-25 12:00 by crew learned:\n    Either:\n    - the existing one\n    - a new one\n" +
		"\nTyped into the file, so not items crew counts (crew to-learn adds one):\n- [ ] typed by hand\n" +
		"\n## Plan\n\n## Log\n- x\n"
	if got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
	}

	plain := "# B\n\n## Still to learn\n<!-- Items. -->\n\n## Log\n"
	bare := learnTask(t, plain)
	if got, _ := shownTask(bare.Path); got != plain {
		t.Errorf("shownTask of a task with no items = %q; want the file as it is", got)
	}
	noSection := learnTask(t, "# C\n\n## Log\n- x\n")
	if got, _ := shownTask(noSection.Path); got != "# C\n\n## Log\n- x\n" {
		t.Errorf("shownTask of a task with no section and no items = %q; want the file as it is", got)
	}
	write(noSection, Record{ID: "aaaaaaaa", At: at, By: "crew to-learn", ToLearn: &ToLearn{"Q?"}})
	if got, _ := shownTask(noSection.Path); got != "# C\n\n## Still to learn\n- [ ] Q? (aaaaaaaa, added 2026-11-25 10:00 by crew to-learn)\n\n## Log\n- x\n" {
		t.Errorf("shownTask of a task with items and no section = %q; want the section before Log", got)
	}
	os.WriteFile(noSection.RecordsPath(), []byte("oops\n"), 0o644)
	if got, _ := shownTask(noSection.Path); !strings.Contains(got, "## Still to learn\ncrew can't read the items still to learn: 001-a.records.jsonl:1: not a record crew can read") {
		t.Errorf("shownTask with an unreadable records file = %q; want it to say so in the section", got)
	}
	os.WriteFile(task.RecordsPath(), []byte("oops\n"), 0o644)
	if got, _ := shownTask(task.Path); !strings.Contains(got, "\n\nTyped into the file, so not items crew counts (crew to-learn adds one):\n- [ ] typed by hand\n") {
		t.Errorf("shownTask with an unreadable records file in a migrated project = %q; want what's typed still marked as not items", got)
	}
}

func TestMigrateStillToLearn(t *testing.T) {
	task := learnTask(t, `---
status: draft
---
## Goal
G.

## Still to learn
<!-- The old comment: tick each item. -->
- [x] Is it an indented answer?
  - Yes, on one line.
- [ ] Still open?
  A note under an open item.
- [x] Several lines?
  - **Answer:**
    - one
      - nested
    - two

- [x] Answered on its own line? Yes: like this.
- [x] Ticked with nothing?
Some prose that isn't an item.

## Plan
`)
	os.Remove(ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path))))
	comment := []string{"<!-- The new comment. -->", ""}
	n, err := migrateStillToLearn(task, comment)
	if err != nil || n != 5 {
		t.Fatalf("migrateStillToLearn = %d, %v; want 5 items", n, err)
	}
	records, err := task.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	type item struct {
		text    string
		answers []string
	}
	var got []item
	for _, i := range learnItems(records) {
		it := item{text: i.ToLearn.Text}
		for _, a := range i.Answers {
			it.answers = append(it.answers, a.Learned.Answer)
		}
		got = append(got, it)
		if i.By != "crew migrate" {
			t.Errorf("%q is by %q, want crew migrate", i.ToLearn.Text, i.By)
		}
	}
	want := []item{
		{"Is it an indented answer?", []string{"Yes, on one line."}},
		{"Still open?", nil},
		{"Several lines?", []string{"- **Answer:**\n  - one\n    - nested\n  - two"}},
		{"Answered on its own line?", []string{"Yes: like this."}},
		{"Ticked with nothing?", []string{noAnswer}},
	}
	if !slices.EqualFunc(got, want, func(a, b item) bool { return a.text == b.text && slices.Equal(a.answers, b.answers) }) {
		t.Errorf("items = %q\nwant %q", got, want)
	}
	b, _ := os.ReadFile(task.Path)
	wantFile := `---
status: draft
---
## Goal
G.

## Still to learn
<!-- The new comment. -->

  A note under an open item.
Some prose that isn't an item.

## Plan
`
	if string(b) != wantFile {
		t.Errorf("task file =\n%s\nwant\n%s", b, wantFile)
	}
}

// A crew migrate stopped after writing a task's records, and before taking the
// checkboxes out of its file, leaves both. Run again, it only takes them out.
func TestMigrateStillToLearnAfterARunCutShort(t *testing.T) {
	file := `---
status: draft
---
## Still to learn
- [x] Settled? Yes.
- [ ] Open?
`
	task := learnTask(t, file)
	os.Remove(ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path))))
	if _, err := migrateStillToLearn(task, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(task.RecordsPath())
	if err := os.WriteFile(task.Path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := migrateStillToLearn(task, nil); err != nil || n != 0 {
		t.Fatalf("migrateStillToLearn run again = %d, %v; want 0 items", n, err)
	}
	if after, _ := os.ReadFile(task.RecordsPath()); string(after) != string(before) {
		t.Errorf("records run again =\n%s\nwant them as the first run left them\n%s", after, before)
	}
	if b, _ := os.ReadFile(task.Path); strings.Contains(string(b), "- [") {
		t.Errorf("task file still has its checkboxes:\n%s", b)
	}
}
