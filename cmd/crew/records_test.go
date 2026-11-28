package main

import (
	"fmt"
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

// add appends a record, as crew to-learn or crew learned does, and returns it.
func add(t *testing.T, task Task, r Record) Record {
	t.Helper()
	records, _ := task.Records()
	n := newRecord("test", records)
	n.ToLearn, n.Learned = r.ToLearn, r.Learned
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
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","answered":{"answer":" "}}`:                                          "an answered record needs its answer",
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

// spikeTask is a spike's task file in a project whose ledger is in this crew's
// format, with the report given before its Log.
func spikeTask(t *testing.T, status, report string) Task {
	t.Helper()
	return learnTask(t, "---\nkind: spike\nstatus: "+status+"\n---\n# S\n\n"+report+"## Log\n- x\n")
}

func TestAnswerIsTheLastRecorded(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	task := spikeTask(t, "working", "## Report\nAnswer: from the report\n\n")
	if answer, err := task.Answer(); err != nil || answer != "" {
		t.Errorf("Answer with only a report = %q, %v; want none: crew reads the answer from records alone", answer, err)
	}
	// A union merge of two clones' records may put the later answer first.
	for _, r := range []Record{
		{ID: "bbbbbbbb", At: at.Add(time.Hour), By: "crew answer", Answered: &Answered{"yes, by cursor"}},
		{ID: "aaaaaaaa", At: at, By: "crew answer", Answered: &Answered{"no, by offset"}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	if answer, err := task.Answer(); err != nil || answer != "yes, by cursor" {
		t.Errorf("Answer = %q, %v; want the one recorded last, whatever its line", answer, err)
	}
	// Within a second, the later line is the later answer.
	if err := task.AddRecords(Record{ID: "cccccccc", At: at.Add(time.Hour), By: "crew answer", Answered: &Answered{"yes, 100 rows a page"}}); err != nil {
		t.Fatal(err)
	}
	if answer, _ := task.Answer(); answer != "yes, 100 rows a page" {
		t.Errorf("Answer with two recorded in the same second = %q; want the later line", answer)
	}
	os.Remove(ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path))))
	if _, err := task.Answer(); err == nil || !strings.Contains(err.Error(), "run crew migrate") {
		t.Errorf("Answer before crew migrate = %v; want it to say to run crew migrate", err)
	}
}

func TestShownTaskGivesASpikesAnswer(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	task := spikeTask(t, "working", "## Report\nTried paging.\n\n")
	if got, _ := shownTask(task.Path); strings.Contains(got, "## Answer") {
		t.Errorf("shownTask of a spike with no answer recorded = %q; want no Answer section", got)
	}
	for _, r := range []Record{
		{ID: "aaaaaaaa", At: at, By: "crew answer", Answered: &Answered{"no"}},
		{ID: "bbbbbbbb", At: at.Add(time.Hour), By: "crew answer", Answered: &Answered{"yes, by cursor"}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	want := "---\nkind: spike\nstatus: working\n---\n# S\n\n## Report\nTried paging.\n\n" +
		"## Answer\nyes, by cursor (recorded 2026-11-25 11:00 by crew answer)\n\n## Log\n- x\n"
	if got, _ := shownTask(task.Path); got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
	}
	os.WriteFile(task.RecordsPath(), []byte("oops\n"), 0o644)
	if got, _ := shownTask(task.Path); !strings.Contains(got, "## Answer\ncrew can't read the answer: 001-a.records.jsonl:1: not a record crew can read") {
		t.Errorf("shownTask with an unreadable records file = %q; want the Answer section to say so", got)
	}
}

func TestReportAnswer(t *testing.T) {
	for report, want := range map[string]string{
		"## Report\nAnswer: yes, it pages by cursor\n\nTried: …\n": "yes, it pages by cursor",
		"## Report\n<!-- comment -->\n\nAnswer:   no  \nmore\n":    "no",
		"## Report\nTried first\nAnswer: later\n":                  "",
		"## Report\nAnswer:\n":                                     "",
		"## Goal\nAnswer: not a report\n":                          "",
	} {
		if got := reportAnswer(spikeTask(t, "done", report+"\n")); got != want {
			t.Errorf("reportAnswer of %q = %q; want %q", report, got, want)
		}
	}
}

// crew migrate records a spike's answer from its report's first line, once:
// not for a spike that has one recorded, nor for one whose report doesn't start
// with it, and it leaves every task file as it was.
func TestMigrateSpikeAnswer(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	recorded := spikeTask(t, "done", "## Report\nAnswer: from the report\n\n")
	if err := recorded.AddRecords(Record{ID: "aaaaaaaa", At: at, By: "crew answer", Answered: &Answered{"as recorded"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		task   Task
		moved  bool
		answer string // the answer recorded once it's run, by whom
		by     string
	}{
		{"a report that starts with its answer", spikeTask(t, "done", "## Report\nAnswer: yes, 100 rows a page\nTried paging.\n\n"), true, "yes, 100 rows a page", "crew migrate"},
		{"a report that doesn't", spikeTask(t, "done", "## Report\nWe tried paging, and it didn't answer.\n\n"), false, "", ""},
		{"an answer recorded already", recorded, false, "as recorded", "crew answer"},
		{"a task that isn't a spike", learnTask(t, "---\nstatus: merged\n---\n## Report\nAnswer: not a spike\n\n## Log\n"), false, "", ""},
	} {
		project := filepath.Dir(filepath.Dir(c.task.Path))
		before, _ := os.ReadFile(c.task.Path)
		os.Remove(ledgerFormatPath(project))
		if moved, err := migrateSpikeAnswer(c.task); err != nil || moved != c.moved {
			t.Errorf("%s: migrateSpikeAnswer = %v, %v; want %v", c.name, moved, err, c.moved)
		}
		// Run again, as after a migration cut short, it records nothing more.
		if moved, err := migrateSpikeAnswer(c.task); err != nil || moved {
			t.Errorf("%s: migrateSpikeAnswer run again = %v, %v; want nothing recorded", c.name, moved, err)
		}
		writeLedgerFormat(project)
		records, err := c.task.Records()
		if err != nil {
			t.Fatal(err)
		}
		r, _ := lastAnswer(records)
		if answer, _ := c.task.Answer(); answer != c.answer || r.By != c.by || c.answer != "" && len(records) != 1 {
			t.Errorf("%s: answer %q by %q in %d record(s); want %q by %q in one", c.name, answer, r.By, len(records), c.answer, c.by)
		}
		if after, _ := os.ReadFile(c.task.Path); string(after) != string(before) {
			t.Errorf("%s: migrateSpikeAnswer changed the task file to\n%s\nfrom\n%s", c.name, after, before)
		}
	}
}

func TestReportLine(t *testing.T) {
	task := spikeTask(t, "done", "## Report\nTried paging.\n\n")
	task.Set("informs", "002-b")
	if line, err := reportLine(task); err != nil || line != "001-a reported, with no answer recorded: read its report; it informs 002-b (task file: "+task.Path+")" {
		t.Errorf("reportLine of a spike with no answer recorded = %q, %v; want it to say so, not send an empty answer", line, err)
	}
	if err := task.AddRecords(Record{ID: "aaaaaaaa", At: time.Now(), By: "crew answer", Answered: &Answered{"yes, by cursor"}}); err != nil {
		t.Fatal(err)
	}
	if line, err := reportLine(task); err != nil || line != "001-a reported: yes, by cursor; it informs 002-b (task file: "+task.Path+")" {
		t.Errorf("reportLine = %q, %v; want the recorded answer", line, err)
	}
}
