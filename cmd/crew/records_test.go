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

// add appends a record, as crew to-learn or crew learned does, and returns it.
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
	if len(items) != 2 || items[0].ID != a.ID || !items[0].Open() || items[1].ID != b.ID || len(items[1].Answers) != 2 || openItems(items) != 1 {
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
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","learned":{"item":"cccccccc","answer":"A"}}`:                         "follows cccccccc, which no line adds as an item to learn",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","ci_cause":{"red_ci":"aaaaaaaa","branch_caused":false}}`:             "follows aaaaaaaa, which no line adds as red CI",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","red_ci":{"head":"","checks":["test"]}}`:                             "needs its head",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","ci_cause":{"red_ci":"","branch_caused":true}}`:                      "needs its red_ci",
		`{"id":"bbbbbbbb","at":"2026-11-25T10:00:00Z","by":"crew x","base_conflict":{"base":"main","at":""}}`:                            "needs its base and its at",
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
		"":    "p's ledger isn't migrated to this crew's records yet: run crew migrate",
		"0\n": "run crew migrate",
		"1\n": "run crew migrate",
		"3\n": "p's ledger is from a newer crew: run crew upgrade",
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
func TestShownTaskRoundReports(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	task := learnTask(t, "# A\n\n## Review rounds\n<!-- Appended by crew. -->\n\n### Round 1 — 2026-11-25 10:00 (PR #12)\n\n- [x] CI failed on aaa1111: test (the branch didn't cause it)\n\n## Log\n")
	for _, r := range []Record{
		{ID: "aaaaaaaa", At: at, By: "crew ci-fix", RedCI: &RedCI{Head: "aaa1111", Checks: []string{"test", "lint"}}},
		{ID: "bbbbbbbb", At: at.Add(time.Hour), By: "crew ci-summary", CICause: &CICause{RedCI: "aaaaaaaa", BranchCaused: false}},
		{ID: "cccccccc", At: at.Add(2 * time.Hour), By: "crew ci-summary", CICause: &CICause{RedCI: "aaaaaaaa", BranchCaused: true}},
		{ID: "dddddddd", At: at.Add(3 * time.Hour), By: "crew ci-fix", RedCI: &RedCI{Head: "bbb2222", Checks: []string{"test"}}},
		{ID: "eeeeeeee", At: at.Add(4 * time.Hour), By: "crew catch-up", BaseConflict: &BaseConflict{Base: "main", At: "ccc3333"}},
	} {
		if err := task.AddRecords(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := shownTask(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# A\n\n## Review rounds\n<!-- Appended by crew. -->\n\nRecorded by crew:\n" +
		"- Red CI on aaa1111: test, lint (aaaaaaaa, sent 2026-11-25 10:00 by crew ci-fix)\n" +
		"  - the branch didn't cause it, said 2026-11-25 11:00 by crew ci-summary\n" +
		"  - the branch caused it, said 2026-11-25 12:00 by crew ci-summary\n" +
		"- Red CI on bbb2222: test (dddddddd, sent 2026-11-25 13:00 by crew ci-fix)\n" +
		"  - no word from the worker yet on whether its branch caused it\n" +
		"- Conflict with main at ccc3333 (eeeeeeee, sent 2026-11-25 14:00 by crew catch-up)\n" +
		"\n### Round 1 — 2026-11-25 10:00 (PR #12)\n\n- [x] CI failed on aaa1111: test (the branch didn't cause it)\n\n## Log\n"
	if got != want {
		t.Errorf("shownTask =\n%s\nwant\n%s", got, want)
	}

	noSection := learnTask(t, "# B\n\n## Log\n")
	if got, _ := shownTask(noSection.Path); got != "# B\n\n## Log\n" {
		t.Errorf("shownTask with no reports = %q; want the file as it is", got)
	}
	if err := noSection.AddRecords(Record{ID: "aaaaaaaa", At: at, By: "crew catch-up", BaseConflict: &BaseConflict{Base: "main", At: "ccc3333"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := shownTask(noSection.Path); got != "# B\n\n## Review rounds\nRecorded by crew:\n- Conflict with main at ccc3333 (aaaaaaaa, sent 2026-11-25 10:00 by crew catch-up)\n\n## Log\n" {
		t.Errorf("shownTask with a report and no section = %q; want the section before Log", got)
	}
	os.WriteFile(noSection.RecordsPath(), []byte("oops\n"), 0o644)
	if got, _ := shownTask(noSection.Path); !strings.Contains(got, "## Review rounds\ncrew can't read the red CI and conflicts it sent: 001-a.records.jsonl:1: not a record crew can read") {
		t.Errorf("shownTask with an unreadable records file = %q; want it to say so in the section", got)
	}
	os.Remove(ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path))))
	if got, _ := shownTask(task.Path); strings.Contains(got, "Recorded by crew") {
		t.Errorf("shownTask in a project not yet migrated = %q; want the file as it is", got)
	}
}

// Before format 2, the rounds' lines were what crew read: reportsTask's rounds
// report red CI on aaa1111, which the branch didn't cause, and on bbb2222, and
// conflicts at eee5555 and fff6666. Lines quoted in a comment, in the brief or
// in an open question report nothing.
func TestMigrateRoundReports(t *testing.T) {
	task := learnTask(t, reportsTask)
	os.Remove(ledgerFormatPath(filepath.Dir(filepath.Dir(task.Path))))
	redCI, conflicts, err := migrateRoundReports(task)
	if err != nil || redCI != 2 || conflicts != 2 {
		t.Fatalf("migrateRoundReports = %d, %d, %v; want 2 red CI and 2 conflicts", redCI, conflicts, err)
	}
	records, err := task.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	rr := roundReports(records)
	var heads []string
	for _, r := range rr.RedCI {
		heads = append(heads, fmt.Sprintf("%s %q %v", r.RedCI.Head, r.RedCI.Checks, r.Counts()))
	}
	if want := []string{`aaa1111 ["test" "lint"] false`, `bbb2222 ["test"] true`}; !slices.Equal(heads, want) {
		t.Errorf("red CI = %q, want %q", heads, want)
	}
	if rr.CIFixes() != 1 || !rr.ConflictAt("eee5555") || !rr.ConflictAt("fff6666") || len(rr.Conflicts) != 2 || rr.Conflicts[0].BaseConflict.Base != "main" {
		t.Errorf("round reports = %+v; want 1 CI fix, and conflicts with main at eee5555 and fff6666", rr)
	}
	round1 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local)
	if !rr.RedCI[0].At.Equal(round1) || !rr.RedCI[0].Causes[0].At.Equal(round1) || rr.RedCI[0].By != "crew migrate" {
		t.Errorf("aaa1111's records are dated %v and %v by %q; want round 1's time, by crew migrate", rr.RedCI[0].At, rr.RedCI[0].Causes[0].At, rr.RedCI[0].By)
	}
	if b, _ := os.ReadFile(task.Path); string(b) != reportsTask {
		t.Errorf("the task file changed:\n%s", b)
	}
	// A second run, after one stopped before the project's format, records nothing more.
	if redCI, conflicts, err := migrateRoundReports(task); err != nil || redCI+conflicts != 0 {
		t.Errorf("a second run = %d, %d, %v; want nothing", redCI, conflicts, err)
	}
	if again, _ := task.readRecords(); len(again) != len(records) {
		t.Errorf("a second run left %d records, want %d", len(again), len(records))
	}
	// Once migrated, the lines are only for the worker to read: taking the mark
	// off one, or adding it to another, counts for nothing.
	edited := strings.NewReplacer(
		"aaa1111: test, lint (the branch didn't cause it)", "aaa1111: test, lint",
		"bbb2222: test\n", "bbb2222: test (the branch didn't cause it)\n",
	).Replace(reportsTask)
	if err := os.WriteFile(task.Path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerFormat(filepath.Dir(filepath.Dir(task.Path))); err != nil {
		t.Fatal(err)
	}
	if after, err := task.RoundReports(); err != nil || after.CIFixes() != 1 || after.RedCI[0].Counts() || !after.RedCI[1].Counts() {
		t.Errorf("after editing the marks by hand: %+v, %v; want aaa1111 still not the branch's, and bbb2222 the branch's", after, err)
	}
}

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
