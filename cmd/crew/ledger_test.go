package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestNewestReview(t *testing.T) {
	none := Task{docWith(t, sampleTask)}
	if n := none.NewestReview(); n != 0 {
		t.Errorf("a task without automated reviews: NewestReview = %d", n)
	}
	// The newest is the last heading, across passes; a heading quoted in a
	// review round isn't a review.
	two := Task{docWith(t, "## Automated reviews\n<!-- Appended by the worker. -->\n\n"+
		"### Automated review 1\n#### [should fix] x\nSummary posted\n\n"+
		"### Automated review 2\n#### [nitpick] y\n\n"+
		"## Review rounds\n### Round 1\n  > ### Automated review 9\n\n## Log\n")}
	if n := two.NewestReview(); n != 2 {
		t.Errorf("NewestReview = %d, want 2", n)
	}
}

// Four automated reviews over two passes: the first needs fixes, the second
// fixes only a nitpick, but its Claude reviewer didn't report, the third is
// clean though Codex failed, and the fourth, in a later pass, has no findings.
// Their outcomes take every form the record has, 026's and 027's included.
const reviewedTask = "## Automated reviews\n<!-- Appended by the worker. -->\n\n" +
	"### Automated review 1 — 2026-11-10 10:00\n\n- Reviewers: Claude reviewer and Codex\n\n" +
	"#### 1. [must fix] a — `a.go:1`\n- Finding:\n  > - Outcome: quoted, not a line of the record\n- Agreed severity: must fix\n- Outcome: fixed in abc1234\n\n" +
	"#### 2. [should fix] b — `b.go:2`\n- Agreed severity: split: held at pre-existing: should fix, Codex refutes\n- Outcome: not a defect: `b.go:2` checks it first\n\n" +
	"#### 3. [nitpick] c — `c.go:3`\n- Agreed severity: nitpick\n- Outcome: recorded\n\n" +
	"#### 4. [fix later] d — `d.go:4`\n- Agreed severity: fix later\n- Outcome: proposal 012-d.md\n\n" +
	"### Automated review 2 — 2026-11-10 11:00\n\n- Reviewers: Claude reviewer (failed: it stalled) and Codex\n\n" +
	"#### 1. [nitpick] e — `e.go:5`\n- Agreed severity: nitpick\n- Outcome: fixed in def5678\n\n" +
	"#### 2. [should fix] f — `f.go:6`\n- Agreed severity: withdrawn\n- Outcome: withdrawn in round 1\n\n" +
	"### Automated review 3 — 2026-11-10 12:00\n\n- Reviewers: Claude reviewer (failed in round 2: it stalled) and Codex (failed: signed out)\n\n" +
	"#### 1. [must fix] g — `g.go:7`\n- Agreed severity: disputed\n- Outcome: left for the user\n\n" +
	"#### 2. [must fix] h — `h.go:8`\n- Agreed severity: pre-existing: must fix\n- Outcome: left for the user: pre-existing, proposal 013-h.md\n\n" +
	"#### 3. [must fix] i — `i.go:9`\n- Agreed severity: flipped: must fix, was withdrawn in review 2\n- Outcome: left for the user: verdict changed since review 2\n\n" +
	"#### 4. [nitpick] j — `j.go:10`\n- Agreed severity: nitpick\n- Outcome:\n\nSummary posted\n\n" +
	"### Automated review 4 — 2026-11-11 09:00\n\n- Reviewers: Claude reviewer\n\nNo findings.\n\n" +
	"## Review rounds\n### Round 1\n  > ### Automated review 9\n\n## Log\n"

func TestAutomatedReviews(t *testing.T) {
	reviews := Task{docWith(t, reviewedTask)}.AutomatedReviews()
	var outcomes [][]Outcome
	var reported, clean []bool
	var setAside []string
	for i, r := range reviews {
		var found []Outcome
		for k, f := range r.Findings {
			found = append(found, f.Outcome())
			if f.SetAside() {
				setAside = append(setAside, fmt.Sprintf("%d.%d", i+1, k+1))
			}
		}
		outcomes = append(outcomes, found)
		reported = append(reported, r.ClaudeReported)
		clean = append(clean, r.Clean())
	}
	want := [][]Outcome{
		{OutcomeFixed, OutcomeNotADefect, OutcomeRecorded, OutcomeProposal},
		{OutcomeFixed, OutcomeWithdrawn},
		{OutcomeLeftForUser, OutcomeLeftForUser, OutcomeLeftForUser, OutcomeOpen},
		nil,
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Errorf("outcomes = %q\nwant %q", outcomes, want)
	}
	if want := []bool{true, false, true, true}; !slices.Equal(reported, want) {
		t.Errorf("ClaudeReported = %v, want %v", reported, want)
	}
	if want := []bool{false, false, true, true}; !slices.Equal(clean, want) {
		t.Errorf("Clean = %v, want %v", clean, want)
	}
	// The split settled as not a defect, and the disputed finding.
	if want := []string{"1.2", "3.1"}; !slices.Equal(setAside, want) {
		t.Errorf("set aside: %v, want %v", setAside, want)
	}
	if got := (Task{docWith(t, sampleTask)}).AutomatedReviews(); got != nil {
		t.Errorf("a task without automated reviews: %v", got)
	}
}

func TestCleanReadsTheSeverityHeld(t *testing.T) {
	for severity, clean := range map[string]bool{
		"must fix": false, "should fix": false, "pre-existing: should fix": false,
		"split: held at must fix, Codex refutes": false, "flipped: should fix, was withdrawn in review 1": false,
		"should fix (lowered from must fix: the pass still runs)": false, "should fix — lowered from must fix: it's rare": false,
		"nitpick": true, "split: held at nitpick, Claude reviewer refutes": true, "fix later": true,
	} {
		r := AutomatedReview{ClaudeReported: true, Findings: []Finding{{severity, "fixed in abc1234"}}}
		if r.Clean() != clean {
			t.Errorf("a fix at %q: Clean = %v, want %v", severity, r.Clean(), clean)
		}
	}
	other := Finding{"nitpick", "acted on at nitpick: fixed in abc1234"}
	if other.Outcome() != OutcomeOther {
		t.Errorf("an outcome in no known form: %q", other.Outcome())
	}
}

// Three acceptance criteria, the second with nested bullets and a bullet in a
// comment, neither of which is a criterion.
const criteria = "## Acceptance criteria\n<!-- Checkable statements:\n- not a criterion -->\n" +
	"- one\n- two, with details:\n  - nested\n  - nested\n  continued\n- three <!-- - inline -->\n\n"

func TestAcceptanceCriteria(t *testing.T) {
	task := Task{docWith(t, criteria+"## Log\n")}
	if got := task.AcceptanceCriteria(); !slices.Equal(got, []string{"one", "two, with details:", "three "}) {
		t.Errorf("AcceptanceCriteria = %q", got)
	}
	numbered := Task{docWith(t, "## Acceptance criteria\n1. one\n2. two\n\n## Log\n")}
	if got := numbered.AcceptanceCriteria(); got != nil {
		t.Errorf("criteria as a numbered list: AcceptanceCriteria = %q, want none", got)
	}
}

func TestEvidenceGaps(t *testing.T) {
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
		{"a shorter fence inside a longer one doesn't close it", "## Evidence\n### 1. one\n````\n```\n### 2. two\nok\n````\n### 3. three\nok\n\n", []int{2}},
		{"lines under another heading belong to no entry", "## Evidence\n### 1. one\n### Notes\nok\n### 2. two\nok\n### 3. three\nok\n\n", []int{1}},
	} {
		task := Task{docWith(t, criteria+c.evidence+"## Log\n")}
		if gaps := task.EvidenceGaps(); !slices.Equal(gaps, c.gaps) {
			t.Errorf("%s: EvidenceGaps = %v; want %v", c.name, gaps, c.gaps)
		}
	}
}

func TestSecurityReviewed(t *testing.T) {
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
		task := Task{docWith(t, criteria+evidence+"## Log\n")}
		if got := task.SecurityReviewed(); got != want {
			t.Errorf("SecurityReviewed with %q = %v, want %v", evidence, got, want)
		}
		if gaps := task.EvidenceGaps(); want && !slices.Equal(gaps, []int{2, 3}) {
			t.Errorf("a security review counts as a criterion's evidence: gaps %v", gaps)
		}
	}
}

func TestRiskLook(t *testing.T) {
	for r, want := range map[Risk]string{
		RiskLow:    "approve from the evidence",
		RiskMedium: "read the diff",
		RiskHigh:   "read the diff; security review ran",
		"":         "read the diff",
		"bogus":    "read the diff",
	} {
		if got := r.Look(); got != want {
			t.Errorf("Risk(%q).Look() = %q, want %q", r, got, want)
		}
	}
	for r, want := range map[Risk]bool{RiskLow: true, RiskMedium: true, RiskHigh: true, "": false, "High": false} {
		if r.Valid() != want {
			t.Errorf("Risk(%q).Valid() = %v", r, !want)
		}
	}
	for r, want := range map[Risk]string{RiskMedium: "medium", "": "no risk tier", "High": "no risk tier"} {
		if got := r.Label(); got != want {
			t.Errorf("Risk(%q).Label() = %q, want %q", r, got, want)
		}
	}
}

// Two CI rounds, one of them ticked, a round of comments with a CI line, and a
// question about a head that stayed red. A line quoted in a comment, or in the
// brief, reports nothing.
const ciTask = "## Context\n- [ ] CI failed on 0000000: brief\n\n" +
	"## Open questions\n- [ ] CI is still red on ccc3333 after 2 CI rounds (MAX_CI_FIXES=2): test. Look at it yourself.\n\n" +
	"## Review rounds\n" +
	"### Round 1 — 2026-11-11 10:00 (PR #12)\n\n- [x] CI failed on aaa1111: test, lint\n\n" +
	"### Round 2 — 2026-11-11 11:00 (PR #12)\n\n- [ ] CI failed on bbb2222: test\n\n**Conversation comments**\n- [ ] comment 5 — [link](u5)\n  > - [ ] CI failed on 9999999: quoted\n\n" +
	"### Round 3 — 2026-11-11 12:00 (PR #12)\n\n- [ ] Automated review requested (crew review)\n\n" +
	"## Log\n"

func TestCIFailures(t *testing.T) {
	task := Task{docWith(t, ciTask)}
	if got := task.CIFailures(); !slices.Equal(got, []string{"aaa1111", "bbb2222"}) {
		t.Errorf("CIFailures = %q", got)
	}
	if got := (Task{docWith(t, sampleTask)}).CIFailures(); got != nil {
		t.Errorf("a task without review rounds: CIFailures = %q", got)
	}
}

func TestCIReported(t *testing.T) {
	task := Task{docWith(t, ciTask)}
	for head, want := range map[string]bool{"aaa1111": true, "bbb2222": true, "ccc3333": true, "0000000": false, "9999999": false, "ddd4444": false} {
		if got := task.CIReported(head); got != want {
			t.Errorf("CIReported(%s) = %v, want %v", head, got, want)
		}
	}
}

func TestFeedbackRounds(t *testing.T) {
	// Round 1 only reports red CI; round 2 has a comment besides; round 3 asks for a review.
	if n := (Task{docWith(t, ciTask)}).FeedbackRounds(); n != 2 {
		t.Errorf("FeedbackRounds = %d, want 2", n)
	}
	if n := (Task{docWith(t, sampleTask)}).FeedbackRounds(); n != 0 {
		t.Errorf("a task without review rounds: FeedbackRounds = %d", n)
	}
}

func TestReviewComments(t *testing.T) {
	// Round 2 has one conversation comment, with a CI line quoted in it.
	if n := (Task{docWith(t, ciTask)}).ReviewComments(); n != 1 {
		t.Errorf("ReviewComments = %d, want 1", n)
	}
	// A review summary, an inline comment and a conversation comment, each
	// with a quote; the CI line and the request for a review aren't comments.
	round := "## Review rounds\n### Round 1 — 2026-11-11 10:00 (PR #12)\n\n- [x] CI failed on aaa1111: test\n\n" +
		"**Review summaries**\n- [x] review 7 (changes_requested)\n  > - [ ] comment 8 — [link](u8)\n\n" +
		"**Inline comments**\n- [ ] `a.go:3` — comment 9 (in thread 4) — [link](u9)\n  > why?\n\n" +
		"**Conversation comments**\n- [x] comment 10 — [link](u10)\n  > ok\n\n" +
		"- [ ] Automated review requested (comment 11)\n\n## Log\n"
	if n := (Task{docWith(t, round)}).ReviewComments(); n != 3 {
		t.Errorf("ReviewComments = %d, want 3", n)
	}
}

func TestLog(t *testing.T) {
	d := docWith(t, "---\nid: 1\n---\n## Log")
	d.Log("did a thing")
	if !regexp.MustCompile(`\n## Log\n- \d{4}-\d\d-\d\d \d\d:\d\d — did a thing\n$`).MatchString(contents(t, d)) {
		t.Errorf("Log:\n%q", contents(t, d))
	}
}

func TestSetStatusLogsTheChange(t *testing.T) {
	task := Task{docWith(t, sampleTask)}
	if err := task.SetStatus(PROpen); err != nil {
		t.Fatal(err)
	}
	if task.Status() != PROpen {
		t.Errorf("Status = %s after SetStatus(pr-open)", task.Status())
	}
	if !strings.HasSuffix(contents(t, task.Doc), " — status → pr-open\n") {
		t.Errorf("SetStatus didn't log the change:\n%s", contents(t, task.Doc))
	}
}

func TestLogEntries(t *testing.T) {
	d := docWith(t, "---\nid: 1\n---\n## Log\n<!-- - 2026-11-01 08:00 — a comment -->\n"+
		"- 2026-11-01 09:00 — created\n- 2026-11-01 09:05 — a message\n  that goes on\n- not dated — skipped\n"+
		"- 2026-11-02 10:30 — status → pr-open\n- 2026-11-02 10:31 — status → nonsense\n")
	var got []string
	var statuses []Status
	for _, e := range d.LogEntries() {
		got = append(got, e.at.Format(logTime)+" "+e.msg)
		if s, ok := e.status(); ok {
			statuses = append(statuses, s)
		}
	}
	want := []string{"2026-11-01 09:00 created", "2026-11-01 09:05 a message", "2026-11-02 10:30 status → pr-open", "2026-11-02 10:31 status → nonsense"}
	if !slices.Equal(got, want) {
		t.Errorf("LogEntries = %q\nwant %q", got, want)
	}
	if !slices.Equal(statuses, []Status{PROpen}) {
		t.Errorf("statuses = %q, want only pr-open", statuses)
	}
	// Log writes what LogEntries reads.
	d.Log("did a thing")
	if es := d.LogEntries(); es[len(es)-1].msg != "did a thing" || time.Since(es[len(es)-1].at) > 2*time.Minute {
		t.Errorf("the entry Log wrote reads back as %+v", es[len(es)-1])
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

// LEDGER.md lists exactly crew's risk tiers, cheapest look first, each with the
// look Risk.Look gives it.
func TestRisksMatchLedgerDoc(t *testing.T) {
	b, err := os.ReadFile("../../LEDGER.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(b), "## Risk tiers\n")
	rest, _, _ = strings.Cut(rest, "\n## ")
	var documented, known []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\| ([^|]+) \\|").FindAllStringSubmatch(rest, -1) {
		documented = append(documented, m[1]+": "+m[2])
	}
	for _, r := range risks {
		known = append(known, string(r)+": "+r.Look())
	}
	if !slices.Equal(documented, known) {
		t.Errorf("risk tiers in LEDGER.md %q, in crew %q", documented, known)
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
	if f, _ := resolveIn(files[0], files); f != files[0] {
		t.Errorf("a path to one of the files: %q, want %s", f, files[0])
	}
	elsewhere := filepath.Join(t.TempDir(), "001-saved-filters.md")
	os.WriteFile(elsewhere, nil, 0o644)
	if f, m := resolveIn(elsewhere, files); f != "" || len(m) != 0 {
		t.Errorf("a path to a file that isn't one of them: %q, %v", f, m)
	}
	if f, m := resolveIn("saved", files); f != "" || len(m) != 2 {
		t.Errorf("an ambiguous substring: %q, %v", f, m)
	}
	if f, m := resolveIn("nothing", files); f != "" || len(m) != 0 {
		t.Errorf("no match: %q, %v", f, m)
	}
	twin := filepath.Join(t.TempDir(), "001-saved-filters.md")
	os.WriteFile(twin, nil, 0o644)
	if f, m := resolveIn("001-saved-filters", append(files, twin)); f != "" || len(m) != 2 {
		t.Errorf("an ID two projects share: %q, %v", f, m)
	}
}

func TestFilesAreInNumberOrder(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	for _, f := range []string{"a/tasks/1000-x", "a/tasks/101-x", "a/tasks/100-x", "a/tasks/999-x", "b/tasks/002-x", "b/tasks/1001-x"} {
		p := c.path("projects", f+".md")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	var got []string
	for _, f := range c.files("tasks", "") {
		got = append(got, filepath.Base(filepath.Dir(filepath.Dir(f)))+"/"+(Doc{f}).ID())
	}
	want := "a/100-x a/101-x a/999-x a/1000-x b/002-x b/1001-x"
	if strings.Join(got, " ") != want {
		t.Errorf("files = %v, want %s", got, want)
	}
	if n := c.nextNumber("tasks", "a"); n != 1001 {
		t.Errorf("nextNumber = %d, want 1001", n)
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

func TestJSONKeepsOrder(t *testing.T) {
	in := `{"z": 1, "a": {"y": [1, "two", true, null], "b": {}}, "m": [], "s": "say \"hi\" $HOME <&>"}`
	v, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := encodeJSON(v, false), `{"z":1,"a":{"y":[1,"two",true,null],"b":{}},"m":[],"s":"say \"hi\" $HOME <&>"}`; got != want {
		t.Errorf("compact:\n%s\nwant\n%s", got, want)
	}
	if got, want := encodeJSON(v, true), "{\n  \"z\": 1,\n  \"a\": {\n    \"y\": [\n      1,\n      \"two\",\n      true,\n      null\n    ],\n    \"b\": {}\n  },\n  \"m\": [],\n  \"s\": \"say \\\"hi\\\" $HOME <&>\"\n}\n"; got != want {
		t.Errorf("indented:\n%s", got)
	}
	for _, bad := range []string{`{"a": }`, `{"a": 1} {}`, `[1, 2`, ``} {
		if _, err := parseJSON([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A project's worker settings can add, but never take away or override.
func TestAddOnly(t *testing.T) {
	base, _ := parseJSON([]byte(`{"permissions": {"allow": ["a", "b"], "deny": ["x"], "defaultMode": "default"}, "hooks": {}}`))
	extra, _ := parseJSON([]byte(`{"permissions": {"allow": ["b", "c"], "deny": [], "defaultMode": "bypassPermissions"}, "env": {"K": "v"}}`))
	want := `{"permissions":{"allow":["a","b","c"],"deny":["x"],"defaultMode":"default"},"hooks":{},"env":{"K":"v"}}`
	if got := encodeJSON(addOnly(base, extra), false); got != want {
		t.Errorf("addOnly = %s\nwant %s", got, want)
	}
	one, _ := parseJSON([]byte(`{"a": [1, {"b": 2}], "c": 1.0}`))
	other, _ := parseJSON([]byte(`{"c": 1, "a": [1, {"b": 2}]}`))
	if !equalJSON(one, other) {
		t.Error("equal JSON compared unequal")
	}
}

// A field whose type changes in a later Claude Code loses only that field.
func TestAgentsReadEachFieldOnItsOwn(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte(`#!/bin/sh
echo '[{"id":"a1","state":"working","pid":4242,"status":"busy","sessionId":"a1-s","name":"crew-x"},
{"id":"b2","state":"blocked","pid":"4243","waitingFor":{"tool":"Bash"},"sessionId":"b2-s"}]'
`), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	as := (&Crew{Home: t.TempDir()}).agents()
	if len(as) != 2 || !as.jobBusy("a1") || as.jobBusy("b2") || !as.find("b2").open() || as.jobState("b2") != "blocked" {
		t.Errorf("agents = %+v", as)
	}
}

func TestAgentFilesAreTheAgentsOwnAndEverySubagents(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	os.Mkdir(c.path("agents"), 0o755)
	for _, a := range []string{"lead", "goal-lead", "implementer", "gardener", "reviewer", "casual-observer"} {
		os.WriteFile(c.path("agents", a+".md"), []byte(a), 0o644)
	}
	t.Setenv("HOME", t.TempDir())
	installed := installedAgents()
	want := []string{installed + "/implementer.md", installed + "/casual-observer.md", installed + "/reviewer.md"}
	if got := c.agentFiles("implementer"); !slices.Equal(got, want) {
		t.Errorf("agentFiles(implementer) = %v, want %v", got, want)
	}
	want = []string{installed + "/goal-lead.md", installed + "/lead.md", installed + "/casual-observer.md", installed + "/reviewer.md"}
	if got := c.agentFiles("goal-lead"); !slices.Equal(got, want) {
		t.Errorf("agentFiles(goal-lead) = %v, want %v: a goal lead follows the lead's intake", got, want)
	}
}

func TestSessionRolesAreTheAgentsTheLeadMayNotRunAsSubagents(t *testing.T) {
	settings, err := readSettings("../../lead/.claude/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var denied []string
	for _, rule := range settings.child("permissions").vals["deny"].([]any) {
		if name, ok := strings.CutPrefix(rule.(string), "Agent("); ok {
			denied = append(denied, strings.TrimSuffix(name, ")"))
		}
	}
	slices.Sort(denied)
	roles := slices.Sorted(slices.Values(sessionRoles))
	if !slices.Equal(denied, roles) {
		t.Errorf("the lead's settings deny the agents %v; sessionRoles is %v", denied, roles)
	}
}

func TestFingerprintCountsAMissingFileAsMissing(t *testing.T) {
	c, f := &Crew{}, filepath.Join(t.TempDir(), "x.md")
	missing := c.fingerprint([]string{"--agent", "x"}, f)
	os.WriteFile(f, nil, 0o644)
	empty := c.fingerprint([]string{"--agent", "x"}, f)
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(missing) || missing == empty {
		t.Errorf("fingerprint with the file missing = %q, empty = %q", missing, empty)
	}
}

func TestProposalAuthor(t *testing.T) {
	for source, want := range map[string]string{
		"gardener": "the gardener", "user": "the user", "lead": "a lead",
		"worker:007-saved-filters": "the worker on 007-saved-filters", "": "", "bob": "",
	} {
		pr := Proposal{docWith(t, "---\nsource: "+source+"\n---\n## Log\n")}
		if got := pr.Author(); got != want {
			t.Errorf("Author for source %q = %q, want %q", source, got, want)
		}
	}
}

func TestProposalTaskContext(t *testing.T) {
	short := Proposal{docWith(t, "---\nsource: user\nrevisit_when: a second rule varies by place\n---\n# T\n\n"+
		"## Evidence\n<!-- bullets -->\n\n## Why it matters\nRules vary by place.\n\n## Proposal\n<!-- target -->\n\n## First step\n\n## Log\n")}
	want := "From proposal `001-a.md` (" + short.Path + "), written by the user. Run normal intake: confirm the plan with the user.\n" +
		"\nRevisit when: a second rule varies by place\n" +
		"\nWhy it matters:\nRules vary by place.\n"
	if got := short.TaskContext(); got != want {
		t.Errorf("a short proposal's context:\n%s\nwant:\n%s", got, want)
	}

	full := Proposal{docWith(t, "---\nid: 1\n---\n# T\n\n## Why it matters\nw\n\n## Proposal\np\n\n## First step\nf\n\n## Log\n")}
	want = "From proposal `001-a.md` (" + full.Path + "). Run normal intake: confirm the plan with the user.\n" +
		"\nWhy it matters:\nw\n\nProposal:\np\n\nFirst step:\nf\n"
	if got := full.TaskContext(); got != want {
		t.Errorf("a proposal without a source:\n%s\nwant:\n%s", got, want)
	}
}

func TestMemorySuggestion(t *testing.T) {
	day := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for why, want := range map[string]string{
		"The user decided in 042":  "- [ ] **Columns are unlimited** The user decided in 042. (worker:042-columns, 2026-11-09)",
		"The user decided in 042.": "- [ ] **Columns are unlimited** The user decided in 042. (worker:042-columns, 2026-11-09)",
	} {
		if got := memorySuggestion("Columns are unlimited", why, "worker:042-columns", day); got != want {
			t.Errorf("suggestion with --why %q:\n%s\nwant:\n%s", why, got, want)
		}
	}
}

func TestMemorySuggestionsCountsOnlyUnsettledOnes(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	if n := c.memorySuggestions("p"); n != 0 {
		t.Errorf("no suggestions file: %d unsettled", n)
	}
	os.MkdirAll(c.path("projects", "p"), 0o755)
	os.WriteFile(c.path("projects", "p", "memory-suggestions.md"), []byte("# Memory suggestions: p\n\n"+
		"- [ ] **A** a. (lead, 2026-11-09)\n- [x] **B** b. (lead, 2026-11-09) → kept\n"+
		"- [x] **C** c. (user, 2026-11-09) → declined: not settled\n- [ ] **D** d. (gardener, 2026-11-09)\n"), 0o644)
	if n := c.memorySuggestions("p"); n != 2 {
		t.Errorf("two unsettled suggestions: counted %d", n)
	}
}

func TestSessionMemoryIsSilentWithoutOneProjectsMemory(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	os.MkdirAll(c.path("projects", "p"), 0o755)
	os.MkdirAll(c.path("projects", "q"), 0o755)
	os.MkdirAll(c.path("projects", "all"), 0o755)
	for _, p := range []string{"p", "all"} {
		os.WriteFile(c.path("projects", p, "memory.md"), []byte("# Project memory: "+p+"\n\n- **Fact** why. (lead, 2026-11-09)\n"), 0o644)
	}
	for _, crewProject := range []string{"", "all", "q", "nope"} {
		if got := c.sessionMemory(crewProject); got != "" {
			t.Errorf("CREW_PROJECT=%q printed:\n%s", crewProject, got)
		}
	}
	got := c.sessionMemory("p")
	want := "Project memory for p (" + c.path("projects", "p", "memory.md") + "): facts the user approved.\n" + memoryRule +
		"\n\n# Project memory: p\n\n- **Fact** why. (lead, 2026-11-09)\n"
	if got != want {
		t.Errorf("CREW_PROJECT=p printed:\n%s\nwant:\n%s", got, want)
	}
}

// Claude Code passes on at most hookOutputCap characters of a hook's output, so
// a longer memory is replaced by an instruction to read it. Characters, not
// bytes: é is two bytes.
func TestSessionMemoryTooLongToLoad(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	os.MkdirAll(c.path("projects", "p"), 0o755)
	f := c.path("projects", "p", "memory.md")
	room := hookOutputCap - utf8.RuneCountInString(memoryHeader("p", f))

	atCap := strings.Repeat("é", room-1) + "\n"
	os.WriteFile(f, []byte(atCap), 0o644)
	if got := c.sessionMemory("p"); got != memoryHeader("p", f)+atCap {
		t.Errorf("a memory of exactly %d characters, header included, wasn't printed whole", hookOutputCap)
	}

	os.WriteFile(f, []byte("é"+atCap), 0o644)
	got := c.sessionMemory("p")
	if want := memoryHeader("p", f) + "This memory is too long to load at session start. Read " + f + " in full now, before anything else.\n"; got != want {
		t.Errorf("a memory one character over the cap printed:\n%.300s\nwant:\n%s", got, want)
	}
}
