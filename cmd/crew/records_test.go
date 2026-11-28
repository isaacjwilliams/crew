package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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

// add appends a record with r's payload, as a crew command does, and returns it.
func add(t *testing.T, task Task, r Record) Record {
	t.Helper()
	records, _ := task.Records()
	n := newRecord("test", records)
	r.ID, r.At, r.By = n.ID, n.At, n.By
	if err := task.AddRecords(r); err != nil {
		t.Fatal(err)
	}
	return r
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
	if len(items) != 2 || items[0].ID != a.ID || !items[0].Open() || items[1].ID != b.ID || len(items[1].Settled) != 2 || openItems(items) != 1 {
		t.Errorf("StillToLearn = %+v; want %s open, then %s with both answers", items, a.ID, b.ID)
	}
}

func TestRecordsRefuseWhatTheyCantRead(t *testing.T) {
	good := `{"id":"aaaaaaaa","at":"2026-11-25T10:00:00Z","by":"crew to-learn","to_learn":{"text":"Q?"}}`
	for line, want := range map[string]string{
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q?"},"extra":1}`:                                 `unknown field "extra"`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q?","why":"newer"}}`:                             `unknown field "why"`,
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x"}`:                                                                    "not 0",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"},"learned":{"item":"aaaaaaaa","answer":"A"}}`: "not 2",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":" "}}`:                                            "needs its text",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","learned":{"item":"aaaaaaaa","answer":""}}`:                          "needs its item and its answer",
		`{"id":"bbbbbbbb","by":"crew x","to_learn":{"text":"Q"}}`:                                                                        "needs an id, an at and a by",
		`{"id":"aaaaaaaa","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"}}`:                                            "already taken",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","learned":{"item":"cccccccc","answer":"A"}}`:                         "settles cccccccc, which no line adds",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","to_learn":{"text":"Q"}} {}`:                                         "more than one value",
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
		"":                             "p's ledger isn't migrated to this crew's records yet: run crew migrate",
		"0\n":                          "run crew migrate",
		fmt.Sprintln(ledgerFormat - 1): "run crew migrate",
		fmt.Sprintln(ledgerFormat + 1): "p's ledger is from a newer crew: run crew upgrade",
		"x\n":                          `holds "x", not a format number`,
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

// crew show prints each criterion's newest evidence record in full, and the
// newest security review, with what's typed under ## Evidence kept below as it
// was written.
func TestShownTaskEvidence(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	sha := "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
	task := learnTask(t, "# A\n\n## Acceptance criteria\n- one\n- User check: two\n- three\n\n## Evidence\n<!-- Record it. -->\n"+
		"### 1. one (on 0a1b2c3)\nTyped.\n\n```\nok\n```\n\n## Log\n- x\n")
	for _, r := range []Record{
		{ID: "aaaaaaaa", At: at, By: "crew record-evidence", Evidence: &Evidence{1, sha, false, "Not yet."}},
		{ID: "bbbbbbbb", At: at.Add(time.Hour), By: "crew record-evidence", Evidence: &Evidence{1, sha, true, "`go test`\n```\nok\n```"}},
		{ID: "cccccccc", At: at, By: "crew record-evidence", Evidence: &Evidence{2, sha, false, "Needs a live session. To try: start one."}},
		{ID: "dddddddd", At: at, By: "crew migrate", Evidence: &Evidence{3, sha, false, "No clone here."}},
		{ID: "eeeeeeee", At: at, By: "crew record-evidence", Evidence: &Evidence{5, sha, true, "A criterion since cut."}},
		{ID: "ffffffff", At: at, By: "crew record-security-review", SecurityReview: &SecurityReview{"An early read."}},
		{ID: "abababab", At: at.Add(time.Hour), By: "crew record-security-review", SecurityReview: &SecurityReview{"1. Shell injection: fixed in 1234567.\n2. None else."}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := shownTask(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# A\n\n## Acceptance criteria\n- one\n- User check: two\n- three\n\n## Evidence\n<!-- Record it. -->\n" +
		"### 1. one (on 0a1b2c3d4e5f)\nRan. Recorded 2026-11-25 11:00 by crew record-evidence:\n`go test`\n```\nok\n```\n\n" +
		"### 2. User check: two (on 0a1b2c3d4e5f)\nNot run: a user check. Recorded 2026-11-25 10:00 by crew record-evidence:\nNeeds a live session. To try: start one.\n\n" +
		"### 3. three (on 0a1b2c3d4e5f)\nNot run, and not a user check. Recorded 2026-11-25 10:00 by crew migrate:\nNo clone here.\n\n" +
		"### 5. (a criterion the brief no longer has) (on 0a1b2c3d4e5f)\nRan. Recorded 2026-11-25 10:00 by crew record-evidence:\nA criterion since cut.\n\n" +
		"### Security review\nRecorded 2026-11-25 11:00 by crew record-security-review:\n1. Shell injection: fixed in 1234567.\n2. None else.\n" +
		"\nTyped into the file, so not evidence crew counts (crew record-evidence records it):\n### 1. one (on 0a1b2c3)\nTyped.\n\n```\nok\n```\n" +
		"\n## Log\n- x\n"
	if got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
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
		for _, a := range i.Settled {
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

// suggestionsBefore is a memory-suggestions.md from before records, with each
// kind of line crew remember wrote and the lead ticked.
const suggestionsBefore = `# Memory suggestions: p

<!-- The old comment: tick each one. -->
- [ ] **Columns are unlimited** The user chose it over a cap of 12. (lead, 2026-11-09)
- [x] **Screenshots go in Dropbox** Collaborators can't see them. (worker:007-shots, 2026-11-10) → kept
- [X] **Specs run in parallel** It's faster: twice. (gardener, 2026-11-11) → declined: Not on CI, where they share a database.

A note someone typed.
`

// oldSuggestionsDoc is memory-suggestions.md in a project not yet in this crew's
// format, holding content.
func oldSuggestionsDoc(t *testing.T, content string) Doc {
	t.Helper()
	c := &Crew{Home: t.TempDir()}
	writeFile(t, c, content, "projects", "p", suggestionsFile)
	return c.suggestionsDoc("p")
}

func TestMigrateMemorySuggestions(t *testing.T) {
	d := oldSuggestionsDoc(t, suggestionsBefore)
	n, err := migrateMemorySuggestions(d, "p")
	if err != nil || n != 3 {
		t.Fatalf("migrateMemorySuggestions = %d, %v; want 3 suggestions", n, err)
	}
	records, err := d.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.By != "crew migrate" {
			t.Errorf("%s is by %q, want crew migrate", r.ID, r.By)
		}
	}
	// As the lines read before, with the date each was made on.
	id, settled := regexp.MustCompile(`^(- \[.\]) [0-9a-f]{8} `), regexp.MustCompile(`^  - (kept|declined) .* by crew migrate`)
	got := suggestionLines(suggestions(records))
	for i, l := range got {
		got[i] = settled.ReplaceAllString(id.ReplaceAllString(l, "$1 "), "  $1")
	}
	want := []string{
		"- [ ] **Columns are unlimited** The user chose it over a cap of 12. (lead, 2026-11-09)",
		"- [x] **Screenshots go in Dropbox** Collaborators can't see them. (worker:007-shots, 2026-11-10)",
		"  kept",
		"- [x] **Specs run in parallel** It's faster: twice. (gardener, 2026-11-11)",
		"  declined: Not on CI, where they share a database.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("suggestions =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	b, _ := os.ReadFile(d.Path)
	if want := suggestionsHead("p") + "\nA note someone typed.\n"; string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
}

// A crew migrate stopped after writing the records, and before rewriting the
// file, leaves both. Run again, it only rewrites the file.
func TestMigrateMemorySuggestionsAfterARunCutShort(t *testing.T) {
	d := oldSuggestionsDoc(t, suggestionsBefore)
	if _, err := migrateMemorySuggestions(d, "p"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(d.RecordsPath())
	if err := os.WriteFile(d.Path, []byte(suggestionsBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := migrateMemorySuggestions(d, "p"); err != nil || n != 0 {
		t.Fatalf("migrateMemorySuggestions run again = %d, %v; want 0", n, err)
	}
	if after, _ := os.ReadFile(d.RecordsPath()); string(after) != string(before) {
		t.Errorf("records run again =\n%s\nwant them as the first run left them\n%s", after, before)
	}
	if b, _ := os.ReadFile(d.Path); strings.Contains(string(b), "- [") {
		t.Errorf("file still has its checkboxes:\n%s", b)
	}
}

// A suggestion's text may hold "<!--", which crew remember wrote as given: the
// migration keeps it, and doesn't take it for a comment that hides the lines
// after it, suggestions or typed.
func TestMigrateMemorySuggestionsKeepsCommentMarkersInTheirText(t *testing.T) {
	d := oldSuggestionsDoc(t, "# Memory suggestions: p\n\n"+
		"- [x] **Imports keep `<!--private-->` markers** As `<!-- x -->` in the why. (lead, 2026-11-09) → declined: Only `<!--this-->` one.\n"+
		"- [ ] **An open `<!--` marker** Never closed. (lead, 2026-11-10)\n"+
		"A note someone typed.\n"+
		"- [ ] **The next one** Still read. (lead, 2026-11-11)\n")
	if n, err := migrateMemorySuggestions(d, "p"); err != nil || n != 3 {
		t.Fatalf("migrateMemorySuggestions = %d, %v; want 3 suggestions", n, err)
	}
	records, err := d.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	all := suggestions(records)
	got := []string{all[0].Suggested.Fact, all[0].Suggested.Why, all[0].Settled[0].Declined.Reason, all[1].Suggested.Fact, all[2].Suggested.Fact}
	want := []string{"Imports keep `<!--private-->` markers", "As `<!-- x -->` in the why", "Only `<!--this-->` one.", "An open `<!--` marker", "The next one"}
	if !slices.Equal(got, want) {
		t.Errorf("migrated %q, want %q", got, want)
	}
	b, _ := os.ReadFile(d.Path)
	if want := suggestionsHead("p") + "\nA note someone typed.\n"; string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
}

// A suggestion line crew can't read stops the migration before it writes
// anything, naming the line.
func TestMigrateMemorySuggestionsRefusesWhatItCantRead(t *testing.T) {
	for line, want := range map[string]string{
		"- [ ] **A** a. (lead, 2026-11-09) → kept":   "only a ticked one",
		"- [x] **A** a. (lead, 2026-11-09)":          "only a ticked one",
		"- [x] **A** a. (lead, 2026-11-09) → sorted": "not a suggestion crew can read",
		"- [ ] A fact typed without the shape":       "not a suggestion crew can read",
		"- [ ] **A** a. (lead, 2026-13-40)":          "month out of range",
	} {
		content := "# Memory suggestions: p\n\n- [ ] **B** b. (lead, 2026-11-09)\n" + line + "\n"
		d := oldSuggestionsDoc(t, content)
		_, err := migrateMemorySuggestions(d, "p")
		if err == nil || !strings.Contains(err.Error(), "memory-suggestions.md:4: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("migrating %q: %v; want memory-suggestions.md:4 and %q", line, err, want)
		}
		if b, _ := os.ReadFile(d.Path); string(b) != content || isFile(d.RecordsPath()) {
			t.Errorf("migrating %q changed the ledger", line)
		}
	}
}

// A project with no suggestions file has nothing to migrate.
func TestMigrateMemorySuggestionsWithoutAFile(t *testing.T) {
	d := Doc{filepath.Join(t.TempDir(), "projects", "p", suggestionsFile)}
	if n, err := migrateMemorySuggestions(d, "p"); err != nil || n != 0 || isFile(d.Path) {
		t.Errorf("migrateMemorySuggestions = %d, %v, file made %v; want nothing done", n, err, isFile(d.Path))
	}
}

// Evidence that left its check to the user, as workers wrote it before records. The first
// two are 237's original Evidence 5 and 238's Evidence 7 in rolemodel's ledger.
var notRunEvidence = map[string]string{
	"237's Evidence 5": "Not done as the brief describes: this worktree's development database is empty and no production backup is on this machine, so a Chrome DevTools pass on a clone would mean capturing a production backup. I didn't, and ran the nearest real-browser check instead:\n" +
		"`bundle exec rspec spec/system/stage_leader_board_run_card_broadcasting_spec.rb`\n```\n3 examples, 0 failures\n```\n" +
		"It covers a division board filling, emptying and moving between Courses while the page is open (on-deck slot, pin released on a Course change). The highlighted slot after a Wave move is covered only by the Jest and batch specs. To try by hand on a clone: move a Wave to another Course with the dashboard open, start a run in it, see both slots take the new Course's card, then stop the run and see both advance.\n",
	"238's Evidence 7":               "Not done. It needs a production clone in an isolated database and a dev server; I didn't set those up in this session. To try: on a dashboard, add a Passthrough over a Stage board through the picker, then stop a run that board includes; its on-deck and highlighted cards should advance without a reload.\n",
	"237's, reworded":                "Ran the nearest real-browser check; steps to try: move a Wave to another Course with the dashboard open, then stop the run.\n```\n3 examples, 0 failures\n```\n",
	"a live check left to the user":  "Only the user can check this: the repo's rules forbid starting real model sessions to test.\n",
	"a session it can't start":       "I can't run a real session from here.\n",
	"a check left to someone named":  "Live check for Isaac: with the keyring locked, run it.\n",
	"a check left to the lead":       "Lead check (before merge, no model session):\n```\ncodex exec -\n```\n",
	"a count it couldn't make":       "No count was possible for the waves API endpoint.\n",
	"a smoke test for the user only": "Live smoke test, for Isaac only: it needs a real goal.\n",
	"a phrase wrapped onto a line":   "Not\n   run: it needs a real terminal.\n",
}

func TestNotRunSign(t *testing.T) {
	for name, text := range notRunEvidence {
		if notRunSign(text) == "" {
			t.Errorf("%s: no sign it wasn't run in %q", name, text)
		}
	}
	for name, text := range map[string]string{
		"237's Evidence 5, redone":      "Chrome DevTools on the clone, as the brief describes: moved a Wave to another Course with the dashboard open, started a run in it, and both slots took the new Course's card. Stopped the run; both advanced.\n",
		"words in output":               "`crew hand-over 1`\n```\nNot done. To try: …\n```\n",
		"words in a list item's output": "`crew hand-over 1`\n   ```\n   Not done. To try: …\n   ```\n",
		"words in a command":            "`grep -c 'to try' agents/implementer.md`: 0\n",
		"a test's own words":            "The e2e live check (entry 1) does the same, and passes.\n",
		"findings left for the user":    "Two findings left for the user and one fixed give `merge now`.\n",
		"a rule quoted":                 "| `lead.md` | \"Only the user can reopen those decisions.\" |\n",
		"what an organizer can't do":    "An organizer can't make that single change through the app.\n",
		"a part run another way":        "I didn't run the browser on 1.x itself, so these formats are compared with 1.x's node output.\n",
		"a capitalized word":            "It touches only Stage boards.\n",
	} {
		if sign := notRunSign(text); sign != "" {
			t.Errorf("%s: %q read as not run, from %q", name, text, sign)
		}
	}
}

// migratedEvidence migrates the evidence of a task file holding content, and
// returns the task and what its records then hold for each criterion: the
// commit, and " not run" after it for a check that didn't run.
func migratedEvidence(t *testing.T, content string) (Task, map[int]string) {
	t.Helper()
	task := learnTask(t, content)
	if _, err := migrateEvidence(task); err != nil {
		t.Fatal(err)
	}
	evidence, err := task.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for n, r := range evidence {
		got[n] = r.Evidence.Commit
		if !r.Evidence.Ran {
			got[n] += " not run"
		}
	}
	return task, got
}

// The migration finds each criterion's entry as crew hand-over did when it read
// the prose: a "### n." heading outside comments and fenced output, with
// something under it.
func TestMigrateEvidenceFindsTheEntriesHandOverRead(t *testing.T) {
	for _, c := range []struct {
		name, evidence string
		gaps           []int
	}{
		{"every criterion has an entry", "## Evidence\n### 1. one\n```\nok\n```\n### 2. two\nseen\n### 3. three\nok\n\n", nil},
		{"one entry missing", "## Evidence\n### 1. one\nok\n### 3. three\nok\n\n", []int{2}},
		{"no Evidence section", "", []int{1, 2, 3}},
		{"entries in a comment don't count", "## Evidence\n<!-- ### 1. like this -->\n### 2. two\nok\n\n", []int{1, 3}},
		{"an entry with nothing under its heading doesn't count", "## Evidence\n### 1. one\n\n### 2. two\nok\n### 3. three\n\n", []int{1, 3}},
		{"an entry with only a comment under it doesn't count", "## Evidence\n### 1. one\n<!-- to do -->\n### 2. two\nok\n### 3. three\nok\n\n", []int{1}},
		{"a heading in fenced output belongs to the entry it's in", "## Evidence\n### 1. one\n```\n### 2. two\nok\n```\n### 3. three\nok\n\n", []int{2}},
		{"a heading in tilde-fenced output belongs to the entry it's in", "## Evidence\n### 1. one\n~~~\n### 2. two\nok\n~~~\n### 3. three\nok\n\n", []int{2}},
		{"a heading in an indented fence belongs to the entry it's in", "## Evidence\n### 1. one\n  ```\n### 2. two\nok\n  ```\n### 3. three\nok\n\n", []int{2}},
		{"a shorter fence inside a longer one doesn't close it", "## Evidence\n### 1. one\n````\n```\n### 2. two\nok\n````\n### 3. three\nok\n\n", []int{2}},
		{"lines under another heading belong to no entry", "## Evidence\n### 1. one\n### Notes\nok\n### 2. two\nok\n### 3. three\nok\n\n", []int{1}},
		{"an entry past the criteria is no criterion's", "## Evidence\n### 1. one\nok\n### 2. two\nok\n### 4. four\nok\n\n", []int{3}},
	} {
		named := regexp.MustCompile(`(?m)^(### \d\. \w+)$`).ReplaceAllString(c.evidence, "$1 (on 0a1b2c3)")
		_, got := migratedEvidence(t, criteria+named+"## Log\n")
		commits := EvidenceCommits{}
		for n, commit := range got {
			commits[n] = commit
		}
		if gaps := commits.Gaps(3); !slices.Equal(gaps, c.gaps) || len(got) != 3-len(c.gaps) {
			t.Errorf("%s: migrated %v; want every criterion but %v", c.name, got, c.gaps)
		}
	}
}

// An entry is migrated only when its heading names the commit it ran on, which
// hand-over refused it without; and only the last entry for a criterion counts.
func TestMigrateEvidenceNamesItsCommit(t *testing.T) {
	_, got := migratedEvidence(t, criteria+"## Evidence\n### 1. one (on 0a1b2c3)\nok\n### 2. two\n```\nran (on 4d5e6f7)\n```\n"+
		"### 3. three (on 1111111)\nok\n### 3. three (on 89abcdef0123456789abcdef0123456789abcdef)\nok\n\n## Log\n")
	if want := map[int]string{1: "0a1b2c3", 3: "89abcdef0123456789abcdef0123456789abcdef"}; !maps.Equal(got, want) {
		t.Errorf("migrated %v, want %v: only the heading names the commit, and the last entry counts", got, want)
	}
	for _, heading := range []string{
		"### 1. one (on 0a1b2c)",  // too short to be a commit
		"### 1. one (on 0A1B2C3)", // git writes SHAs in lower case
		"### 1. one on 0a1b2c3",
		"### 1. one (0a1b2c3)",
	} {
		if _, got := migratedEvidence(t, criteria+"## Evidence\n"+heading+"\nok\n\n## Log\n"); len(got) != 0 {
			t.Errorf("%q migrated as %v, want nothing: it names no commit", heading, got)
		}
	}
	// A later entry that names no commit was what hand-over read, and refused.
	if _, got := migratedEvidence(t, criteria+"## Evidence\n### 1. one (on 0a1b2c3)\nok\n### 1. one\nredone\n\n## Log\n"); len(got) != 0 {
		t.Errorf("a criterion whose last entry names no commit migrated as %v, want nothing", got)
	}
}

// The migration records a check as not run where hand-over read its prose as
// saying so, and keeps everything after the commit as what it shows.
func TestMigrateEvidenceCarriesOverWhetherItRan(t *testing.T) {
	task, got := migratedEvidence(t, "## Acceptance criteria\n- one\n- User check: two\n- three\n\n## Evidence\n"+
		"### 1. one (on 0a1b2c3)\nNot done. To try: open the page.\n"+
		"### 2. two (on 0a1b2c3)\nNeeds a live session. To try: start one.\n"+
		"### 3. three (on 0a1b2c3): `go test`\n```\nNot done: in output, so it ran\n```\n\n## Log\n")
	if want := map[int]string{1: "0a1b2c3 not run", 2: "0a1b2c3 not run", 3: "0a1b2c3"}; !maps.Equal(got, want) {
		t.Errorf("migrated %v, want %v", got, want)
	}
	evidence, _ := task.Evidence()
	if shows, want := evidence[3].Evidence.Shows, ": `go test`\n```\nNot done: in output, so it ran\n```"; shows != want {
		t.Errorf("criterion 3 shows %q, want %q: what follows the commit, with the lines under it", shows, want)
	}
}

func TestMigrateEvidenceSecurityReview(t *testing.T) {
	for evidence, want := range map[string]bool{
		"## Evidence\n### 1. one\nok\n### Security review\nNo findings.\n\n":   true,
		"## Evidence\n### 1. one\nok\n\n":                                      false,
		"## Evidence\n<!-- ### Security review -->\n\n":                        false,
		"## Goal\n### Security review\n\n":                                     false,
		"## Evidence\n### 1. one\nok\n### Security review\n\n":                 false,
		"## Evidence\n### Security review\n<!-- to do -->\n### 1. one\nok\n\n": false,
		"## Evidence\n### 1. one\n```\n### Security review\nok\n```\n\n":       false,
		"## Evidence\n### 1. one\n~~~\n### Security review\nok\n~~~\n\n":       false,
	} {
		task, got := migratedEvidence(t, criteria+evidence+"## Log\n- 2026-11-10 09:00 — status → working\n- 2026-11-11 08:11 — status → pr-open\n")
		reviews, err := task.SecurityReviews()
		if err != nil || (len(reviews) == 1) != want {
			t.Errorf("security reviews migrated from %q = %v, %v; want one: %v", evidence, reviews, err, want)
		}
		if want && !reviews[0].At.Equal(time.Date(2026, 11, 11, 8, 11, 0, 0, time.Local)) {
			t.Errorf("the security review is recorded at %v, want the first hand-over, which it let through", reviews[0].At)
		}
		if len(got) != 0 {
			t.Errorf("a security review migrated as a criterion's evidence: %v", got)
		}
	}
}

// crew migrate may already have moved a task's items still to learn, so only
// evidence it recorded means the task's evidence is migrated; and it leaves the
// task file as it was.
func TestMigrateEvidenceOnce(t *testing.T) {
	content := criteria + "## Evidence\n### 1. one (on 0a1b2c3)\nok\n### Security review\nNone.\n\n## Log\n"
	task := learnTask(t, content)
	add(t, task, Record{ToLearn: &ToLearn{"Q?"}})
	records, _ := task.Records()
	records[0].By = "crew migrate"
	os.Remove(task.RecordsPath())
	task.AddRecords(records...)
	for run, want := range []int{2, 0} {
		if n, err := migrateEvidence(task); n != want || err != nil {
			t.Errorf("run %d: migrateEvidence = %d, %v; want %d", run+1, n, err, want)
		}
	}
	if got := contents(t, task.Doc); got != content {
		t.Errorf("the task file after the migration = %q, want it as it was", got)
	}
}

// The migration gives crew hand-over the answer it gave from the prose: each
// criterion's commit and whether it ran, a user check left undone accepted, one
// that isn't refused, and stale evidence named.
func TestMigratedEvidenceHandsOverAsBefore(t *testing.T) {
	g := gitRepo{t, stackRepo(t)}
	start := g.git("rev-parse", "HEAD")
	g.git("checkout", "-q", "-b", "agent/a")
	old := g.commit("first", "a", "1\n")
	now := g.commit("second", "a", "2\n")
	g.git("update-ref", "refs/remotes/origin/agent/a", "HEAD")
	task := learnTask(t, "---\nid: 001-a\nrisk: high\nbase: main\nbranch: agent/a\nbase_sha: "+start+"\n---\n# A\n\n"+
		"## Acceptance criteria\n- ran\n- User check: a live session\n- left undone\n- stale\n\n## Evidence\n"+
		"### 1. ran (on "+now[:7]+")\n`go test`\n```\nok\n```\n"+
		"### 2. a live session (on "+now+")\nNeeds a live session. To try: start one.\n"+
		"### 3. left undone (on "+now+")\nNot done: there's no clone here.\n"+
		"### 4. stale (on "+old+")\nok\n"+
		"### Security review\nNo findings.\n\n## Log\n")
	if _, err := migrateEvidence(task); err != nil {
		t.Fatal(err)
	}
	evidence, _ := task.Evidence()
	got := map[int]string{}
	for n, r := range evidence {
		got[n] = fmt.Sprint(r.Evidence.Commit, " ran: ", r.Evidence.Ran)
	}
	want := map[int]string{1: now[:7] + " ran: true", 2: now + " ran: false", 3: now + " ran: false", 4: old + " ran: true"}
	if !maps.Equal(got, want) {
		t.Errorf("migrated evidence = %v, want %v", got, want)
	}
	if awaits, err := task.AwaitsSecurityReview(); awaits || err != nil {
		t.Errorf("AwaitsSecurityReview = %v, %v; want the security review migrated", awaits, err)
	}
	if err := checkEvidence(task, g.repo, nil); err == nil || !strings.HasPrefix(err.Error(), "evidence for acceptance criteria 3 is recorded as not run.") {
		t.Errorf("hand-over = %v; want criterion 3 refused as not run, and not the user check", err)
	}
	add(t, task, Record{Evidence: &Evidence{Criterion: 3, Commit: now, Ran: true, Shows: "ran it on a clone"}})
	if err := checkEvidence(task, g.repo, nil); err == nil || !strings.Contains(err.Error(), "evidence ran on other work than the branch has now, here and on origin, in the recorded evidence: 4 (on "+old[:12]+")") {
		t.Errorf("hand-over = %v; want criterion 4 refused as stale", err)
	}
}
