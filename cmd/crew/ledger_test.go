package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The expected outputs below are what the bash crew's fm_set, section_body,
// append_to_section and insert_before_heading produced for the same input.

const sampleTask = "---\nid: 1\ntitle: \"A\"\nstatus: draft\npr:\n---\n# A\n\n## Goal\nx\n\n## Log\n- one\n"

func docWith(t *testing.T, content string) Doc {
	t.Helper()
	p := filepath.Join(t.TempDir(), "001-a.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Doc{p}
}

func contents(t *testing.T, d Doc) string {
	t.Helper()
	b, err := os.ReadFile(d.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFrontMatterQuoting(t *testing.T) {
	for _, c := range []struct{ value, line, get string }{
		{"plain", "status: plain", "plain"},
		{"has: colon", `status: "has: colon"`, "has: colon"},
		{"has #hash", `status: "has #hash"`, "has #hash"},
		{`"starts quoted`, `status: "\"starts quoted"`, `"starts quoted`},
		{`mid "quote" here`, `status: "mid \"quote\" here"`, `mid "quote" here`},
		{"two\nlines", "status: two lines", "two lines"},
		{"", "status: ", ""},
		{"a#b", "status: a#b", "a#b"},
		{"colon:nospace", "status: colon:nospace", "colon:nospace"},
	} {
		d := docWith(t, sampleTask)
		if err := d.Set("status", c.value); err != nil {
			t.Fatal(err)
		}
		if line := strings.Split(contents(t, d), "\n")[3]; line != c.line {
			t.Errorf("Set(%q) wrote %q, want %q", c.value, line, c.line)
		}
		if got := d.Get("status"); got != c.get {
			t.Errorf("Get after Set(%q) = %q, want %q", c.value, got, c.get)
		}
	}
}

func TestFrontMatterKeys(t *testing.T) {
	d := docWith(t, sampleTask)
	d.Set("newkey", "val")
	if want := "---\nid: 1\ntitle: \"A\"\nstatus: draft\npr:\nnewkey: val\n---\n# A\n\n## Goal\nx\n\n## Log\n- one\n"; contents(t, d) != want {
		t.Errorf("a new key:\n%s", contents(t, d))
	}
	if _, ok := d.Lookup("missing"); ok {
		t.Error("Lookup found a missing key")
	}
	if v, ok := d.Lookup("pr"); !ok || v != "" {
		t.Errorf("an empty key: %q, %v", v, ok)
	}
	if d.Get("title") != "A" {
		t.Errorf("a quoted value: %q", d.Get("title"))
	}

	dup := docWith(t, "---\nid: 1\npr: x\npr: y\n---\nbody\n")
	dup.Set("pr", "z")
	if contents(t, dup) != "---\nid: 1\npr: z\n---\nbody\n" {
		t.Errorf("a duplicated key:\n%s", contents(t, dup))
	}
	plain := docWith(t, "no front matter\nstatus: x\n")
	plain.Set("status", "y")
	if contents(t, plain) != "no front matter\nstatus: x\n" || plain.Get("status") != "" {
		t.Errorf("a file without front matter:\n%s", contents(t, plain))
	}
	// A key is matched with its colon: pr isn't pr_state.
	ps := docWith(t, "---\npr_state: open\n---\n")
	if ps.Get("pr") != "" {
		t.Error("pr matched pr_state")
	}
}

const sampleSections = "---\nid: 1\n---\n## Open questions\n<!-- comment\n still -->\n- [ ] q1 <!-- inline --> tail\n\n  \n- [x] done\n## Decisions\n"

func TestSections(t *testing.T) {
	d := docWith(t, sampleSections)
	if got := d.Section("## Open questions"); got != "- [ ] q1  tail\n- [x] done" {
		t.Errorf("Section = %q", got)
	}
	if d.Section("## Decisions") != "" || d.Section("## Missing") != "" {
		t.Error("an empty or missing section has text")
	}
	if d.OpenQuestions() != 1 {
		t.Errorf("OpenQuestions = %d", d.OpenQuestions())
	}

	last := docWith(t, sampleSections)
	last.AppendToSection("## Decisions", "line1\nline2\n")
	if want := sampleSections + "\nline1\nline2\n\n"; contents(t, last) != want {
		t.Errorf("append to the last section:\n%s", contents(t, last))
	}
	mid := docWith(t, sampleSections)
	mid.AppendToSection("## Open questions", "line1\nline2\n")
	if want := strings.Replace(sampleSections, "## Decisions", "line1\nline2\n\n## Decisions", 1); contents(t, mid) != want {
		t.Errorf("append to a section in the middle:\n%s", contents(t, mid))
	}
	missing := docWith(t, sampleTask)
	missing.AppendToSection("## Decisions", "line1\nline2\n")
	if want := strings.Replace(sampleTask, "## Log", "## Decisions\n\nline1\nline2\n\n## Log", 1); contents(t, missing) != want {
		t.Errorf("append to a missing section:\n%s", contents(t, missing))
	}

	ins := docWith(t, sampleTask)
	ins.InsertBefore("## Log", "line1\nline2\n")
	if want := strings.Replace(sampleTask, "## Log", "line1\nline2\n\n## Log", 1); contents(t, ins) != want {
		t.Errorf("insert before a heading:\n%s", contents(t, ins))
	}
	nohead := docWith(t, "no newline")
	nohead.InsertBefore("## Log", "line1\nline2\n")
	if contents(t, nohead) != "no newline\nline1\nline2\n" {
		t.Errorf("insert without the heading:\n%q", contents(t, nohead))
	}

	rep := docWith(t, "# T\n\n## Context\nold\nmore\n\n## Plan\np\n")
	rep.ReplaceSection("## Context", "new\n")
	if contents(t, rep) != "# T\n\n## Context\nnew\n\n## Plan\np\n" {
		t.Errorf("replace a section:\n%s", contents(t, rep))
	}
}

func TestLog(t *testing.T) {
	d := docWith(t, "---\nid: 1\n---\n## Log")
	d.Log("did a thing")
	if !regexp.MustCompile(`\n## Log\n- \d{4}-\d\d-\d\d \d\d:\d\d — did a thing\n$`).MatchString(contents(t, d)) {
		t.Errorf("Log:\n%q", contents(t, d))
	}
}

// Every status LEDGER.md documents is one crew knows, and the other way round.
func TestStatusesMatchLedgerDoc(t *testing.T) {
	b, err := os.ReadFile("../../LEDGER.md")
	if err != nil {
		t.Fatal(err)
	}
	table := func(heading string) []string {
		_, rest, _ := strings.Cut(string(b), heading+"\n")
		rest, _, _ = strings.Cut(rest, "\n## ")
		var vals []string
		for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\|").FindAllStringSubmatch(rest, -1) {
			vals = append(vals, m[1])
		}
		return vals
	}
	documented := table("## Task lifecycle")
	var known []string
	for _, s := range taskStatuses {
		known = append(known, string(s))
	}
	if strings.Join(documented, " ") != strings.Join(known, " ") {
		t.Errorf("task statuses in LEDGER.md %v, in crew %v", documented, known)
	}
	documented = table("## Goals")
	known = nil
	for _, s := range goalStatuses {
		known = append(known, string(s))
	}
	if strings.Join(documented, " ") != strings.Join(known, " ") {
		t.Errorf("goal statuses in LEDGER.md %v, in crew %v", documented, known)
	}
}

func TestResolveIn(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, f := range []string{"001-saved-filters", "002-saved-search", "010-other", "100-big"} {
		p := filepath.Join(dir, f+".md")
		pr := ""
		if f == "010-other" {
			pr = "https://github.com/o/r/pull/12"
		}
		os.WriteFile(p, []byte("---\npr: "+pr+"\n---\n"), 0o644)
		files = append(files, p)
	}
	for arg, want := range map[string]string{
		"001-saved-filters": "001-saved-filters",
		"1":                 "001-saved-filters",
		"010":               "010-other",
		"10":                "010-other",
		"other":             "010-other",
		"#12":               "010-other",
		"big":               "100-big",
	} {
		f, _ := resolveIn(arg, files)
		if (Doc{f}).ID() != want {
			t.Errorf("resolveIn(%q) = %q, want %s", arg, f, want)
		}
	}
	if f, m := resolveIn("saved", files); f != "" || len(m) != 2 {
		t.Errorf("an ambiguous substring: %q, %v", f, m)
	}
	if f, m := resolveIn("nothing", files); f != "" || len(m) != 0 {
		t.Errorf("no match: %q, %v", f, m)
	}
}

// questions_routed holds what POSIX cksum printed for the questions, so the
// checksum mustn't change.
func TestCksum(t *testing.T) {
	for in, want := range map[string]uint32{"abc\n": 1112837078, "- [ ] Which header style? A or B.\n": 2648987952, "": 4294967295} {
		if got := cksum([]byte(in)); got != want {
			t.Errorf("cksum(%q) = %d, want %d", in, got, want)
		}
	}
}
