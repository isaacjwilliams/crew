package main

import (
	"fmt"
	"maps"
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

// A task's auto_review turns its automated review off only while it isn't high
// risk, and hand-over waits for a review whenever one should have run.
func TestAutomatedReviewOffAndAwaited(t *testing.T) {
	reviewed := "## Automated reviews\n### Automated review 1\n\n## Log\n"
	for _, c := range []struct {
		front, body, project string // the task's front matter and body, and the project's AUTO_REVIEWS
		reviews              string // what AutoReviews gives
		awaits               bool   // what AwaitsAutomatedReview gives
	}{
		// Without auto_review, hand-over waits for a review of a PR.
		{"risk: low\npr: u", "## Log\n", "3", "3", true},
		{"risk: medium\npr: u", "## Log\n", "3", "3", true},
		{"risk: high\npr: u", "## Log\n", "3", "3", true},
		{"risk: high\npr: u", reviewed, "3", "3", false},
		// No PR, no review to wait for.
		{"risk: high", "## Log\n", "3", "3", false},
		// Off below high risk.
		{"auto_review: no\nrisk: medium\npr: u", "## Log\n", "3", "0", false},
		// Back on at high risk.
		{"auto_review: no\nrisk: high\npr: u", "## Log\n", "3", "3", true},
		{"auto_review: no\nrisk: high\npr: u", reviewed, "3", "3", false},
		// A project without automated review has none to wait for.
		{"risk: low\npr: u", "## Log\n", "0", "0", false},
		{"auto_review: no\nrisk: high\npr: u", "## Log\n", "0", "0", false},
	} {
		task := Task{docWith(t, "---\n"+c.front+"\n---\n"+c.body)}
		if got := task.AutoReviews(c.project); got != c.reviews {
			t.Errorf("%q with AUTO_REVIEWS=%s: AutoReviews = %s, want %s", c.front, c.project, got, c.reviews)
		}
		if got := task.AwaitsAutomatedReview(c.project); got != c.awaits {
			t.Errorf("%q, %q with AUTO_REVIEWS=%s: AwaitsAutomatedReview = %v", c.front, c.body, c.project, got)
		}
	}
}

// A task's pre_pr_review turns its pre-PR passes off only while it isn't high risk.
func TestPrePRReviewOff(t *testing.T) {
	for _, c := range []struct{ front, want string }{
		{"risk: low", "yes"},
		{"pre_pr_review: yes\nrisk: low", "yes"},
		{"pre_pr_review: no\nrisk: low", "no"},
		{"pre_pr_review: no\nrisk: medium", "no"},
		{"pre_pr_review: no\nrisk: high", "yes"},
	} {
		task := Task{docWith(t, "---\n"+c.front+"\n---\n## Log\n")}
		if got := task.PrePRReview(); got != c.want {
			t.Errorf("%q: PrePRReview = %s, want %s", c.front, got, c.want)
		}
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

func TestReviewedRange(t *testing.T) {
	for line, want := range map[string][2]string{
		// A review of the whole PR, in the record's form before 027 and after.
		"`f1..h1`, 4 commits on `b` since `main` (fork point from the PR's base)": {"f1", "h1"},
		"`f1..h1`, 4 commits on `b` since `main` (fork point `f1`)":               {"f1", "h1"},
		// A review of the commits since an earlier one names its fork point.
		"`h1..h2`, 2 commits on `b` since review 1 (fork point `f1`)": {"f1", "h2"},
		"not a range":     {"", ""},
		"`h1`, 2 commits": {"", ""},
	} {
		if fork, head := reviewedRange(line); fork != want[0] || head != want[1] {
			t.Errorf("reviewedRange(%q) = %q, %q; want %q, %q", line, fork, head, want[0], want[1])
		}
	}
	task := "## Automated reviews\n### Automated review 1 — 2026-11-10 10:00\n\n- Range: `f1..h1`, 4 commits on `b` since `main` (fork point `f1`)\n" +
		"### Automated review 2 — 2026-11-10 11:00\n\n- Range: `h1..h2`, 2 commits on `b` since review 1 (fork point `f1`)\n" +
		"#### 1. [nitpick] a — `a.go:1`\n- Finding:\n  > - Range: `x..y`\n" +
		"### Automated review 3 — 2026-11-10 12:00\n\n## Log\n"
	var got []string
	for _, r := range (Task{docWith(t, task)}).AutomatedReviews() {
		got = append(got, r.Fork+".."+r.Head)
	}
	if want := []string{"f1..h1", "f1..h2", ".."}; !slices.Equal(got, want) {
		t.Errorf("reviewed ranges = %q, want %q", got, want)
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
		{"a heading in an indented fence belongs to the entry it's in", "## Evidence\n### 1. one\n  ```\n### 2. two\nok\n  ```\n### 3. three\nok\n\n", []int{2}},
		{"a shorter fence inside a longer one doesn't close it", "## Evidence\n### 1. one\n````\n```\n### 2. two\nok\n````\n### 3. three\nok\n\n", []int{2}},
		{"lines under another heading belong to no entry", "## Evidence\n### 1. one\n### Notes\nok\n### 2. two\nok\n### 3. three\nok\n\n", []int{1}},
	} {
		task := Task{docWith(t, criteria+c.evidence+"## Log\n")}
		if gaps := task.Evidence().Gaps(3); !slices.Equal(gaps, c.gaps) {
			t.Errorf("%s: Gaps = %v; want %v", c.name, gaps, c.gaps)
		}
	}
}

func TestEvidenceNamesItsCommit(t *testing.T) {
	task := Task{docWith(t, criteria+"## Evidence\n### 1. one (on 0a1b2c3)\nok\n### 2. two\n```\nran (on 4d5e6f7)\n```\n"+
		"### 3. three (on 89abcdef0123456789abcdef0123456789abcdef)\nok\n### Security review\nNo findings.\n\n## Log\n")}
	want := map[int]string{1: "0a1b2c3", 2: "", 3: "89abcdef0123456789abcdef0123456789abcdef"}
	if got := commits(task.Evidence()); !maps.Equal(got, want) {
		t.Errorf("Evidence = %v, want %v", got, want)
	}
	if got := task.Evidence().Unnamed(); !slices.Equal(got, []int{2}) {
		t.Errorf("Unnamed = %v, want [2]: only the heading names the commit", got)
	}
	for heading, want := range map[string]string{
		"### 1. one (on 0a1b2c)":  "", // too short to be a commit
		"### 1. one (on 0A1B2C3)": "", // git writes SHAs in lower case
		"### 1. one on 0a1b2c3":   "",
		"### 1. one (0a1b2c3)":    "",
	} {
		task := Task{docWith(t, criteria+"## Evidence\n"+heading+"\nok\n\n## Log\n")}
		if got := task.Evidence()[1].Commit; got != want {
			t.Errorf("%q names %q, want %q", heading, got, want)
		}
	}
}

func TestPREvidence(t *testing.T) {
	body := "Adds saved filters.\r\n\r\n## Risk\r\n\r\n1. not evidence (on 1111111)\r\n\r\n## Evidence\r\n\r\n" +
		"1. one (on 0a1b2c3): `go test`\r\n   <details><summary>Output</summary>\r\n\r\n   ```\r\n   2. output (on 2222222)\r\n   ```\r\n\r\n   </details>\r\n\r\n" +
		"```\r\n3. output too (on 3333333)\r\n```\r\n" +
		"<!-- 3. in a comment (on 4444444) -->\r\n" +
		"2. two, which names no commit: a look at the page\r\n\r\n## Notes for reviewers\r\n\r\n3. not evidence (on 5555555)\r\n"
	want := map[int]string{1: "0a1b2c3", 2: ""}
	if got := commits(PREvidence(body)); !maps.Equal(got, want) {
		t.Errorf("PREvidence = %v, want %v: only top-level items under ## Evidence, outside fences and comments, count", got, want)
	}
	if got := PREvidence(body).Gaps(3); !slices.Equal(got, []int{3}) {
		t.Errorf("Gaps = %v, want [3]", got)
	}
	if got := PREvidence("No evidence section.").Gaps(2); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("Gaps without ## Evidence = %v, want [1 2]", got)
	}
}

// commits is the commit each item of e names, by its criterion's number.
func commits(e Evidence) map[int]string {
	c := map[int]string{}
	for n, item := range e {
		c[n] = item.Commit
	}
	return c
}

func TestPREvidenceItemText(t *testing.T) {
	body := "## Evidence\n\n1. one (on 0a1b2c3): `go test`\n   ```\n   4. output\n   ```\n   Not done.\n" +
		"2. two (on 4d5e6f7): seen\n\n## Notes\n\nNot evidence.\n"
	e := PREvidence(body)
	if want := ": `go test`\n   ```\n   4. output\n   ```\n   Not done."; e[1].Text != want {
		t.Errorf("item 1's text = %q, want %q: an item runs up to the next one, past its fenced output", e[1].Text, want)
	}
	if want := ": seen\n"; e[2].Text != want {
		t.Errorf("item 2's text = %q, want %q: it ends with the section", e[2].Text, want)
	}
}

func TestUserChecks(t *testing.T) {
	task := Task{docWith(t, "## Acceptance criteria\n- one\n- User check: a live smoke test\n- three\n- four\n- five\n\n"+
		"## Decisions\n- 2026-11-24 — Criterion 3 is a user check: needs a real terminal (user)\n"+
		"- 2026-11-24 — criterion 4 is a user check, says the lead (lead)\n"+
		"- 2026-11-24 — Criterion 5 is a user check (user) — no: the line must end with the user's mark\n\n## Log\n")}
	if got, want := task.UserChecks(), map[int]bool{2: true, 3: true}; !maps.Equal(got, want) {
		t.Errorf("UserChecks = %v, want %v: the brief's 'User check:' criteria and those a (user) Decision names", got, want)
	}
}

// Evidence that leaves its check to the user, as workers wrote it. The first
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

func TestEvidenceNotRun(t *testing.T) {
	task := Task{docWith(t, "## Acceptance criteria\n- one\n- User check: two\n- three\n\n## Evidence\n"+
		"### 1. one (on 0a1b2c3)\nNot done. To try: open the page.\n"+
		"### 2. two (on 0a1b2c3)\nNeeds a live session. To try: start one.\n"+
		"### 3. three (on 0a1b2c3)\n```\nok\n```\n\n## Log\n")}
	if got, want := task.Evidence().NotRun(task.UserChecks()), "1 ('Not done')"; got != want {
		t.Errorf("NotRun = %q, want %q: only a criterion that isn't the user's check is refused", got, want)
	}
	pr := PREvidence("## Evidence\n\n1. one (on 0a1b2c3): Not\n   run: it needs a real terminal.\n")
	if got, want := pr.NotRun(nil), "1 ('Not run')"; got != want {
		t.Errorf("PR NotRun = %q, want %q: a phrase wrapped onto an item's next line is still read", got, want)
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
		if gaps := task.Evidence().Gaps(3); want && !slices.Equal(gaps, []int{2, 3}) {
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

func TestTaskKind(t *testing.T) {
	for kind, want := range map[string]Kind{"": KindBuild, "build": KindBuild, "spike": KindSpike, "fix": KindFix, "refine": KindRefine, "drill": "drill"} {
		task := Task{docWith(t, "---\nkind: "+kind+"\n---\n## Log\n")}
		if got := task.Kind(); got != want {
			t.Errorf("kind %q: Kind() = %q, want %q", kind, got, want)
		}
		if _, err := task.rules(); (err == nil) != (kind != "drill") {
			t.Errorf("kind %q: rules() error = %v", kind, err)
		}
	}
	if got := kindNames(); got != "build, spike, fix or refine" {
		t.Errorf("kindNames() = %q", got)
	}
}

// A build's brief sets an objective, its gates and a size, and leaves the design
// to the worker. A spike's brief has a plan, what to try, but no size; it
// informs a draft, and ends done rather than with a PR for the user. A fix is a
// build that also names the merged task it fixes, or unknown, and the check that
// missed the defect. A refine is a build by another name.
func TestKindRules(t *testing.T) {
	build, _ := KindBuild.rules()
	spike, _ := KindSpike.rules()
	fix, _ := KindFix.rules()
	refine, _ := KindRefine.rules()
	if !slices.Contains(build.brief, "## Size") || !slices.Contains(build.design, "## Plan") || !slices.Contains(build.design, "## Structure") ||
		!build.pr || build.keys != nil || build.link != "" || build.handedOver != PROpen {
		t.Errorf("build's rules: %+v", build)
	}
	if !slices.Contains(spike.brief, "## Plan") || slices.Contains(spike.brief, "## Size") || spike.design != nil ||
		spike.pr || spike.keys != nil || spike.link != "informs" || spike.linkTo != Draft || spike.linkUnknown || spike.handedOver != Done {
		t.Errorf("spike's rules: %+v", spike)
	}
	if !slices.Equal(fix.brief, build.brief) || !slices.Equal(fix.design, build.design) || !fix.pr || !slices.Equal(fix.keys, []string{"fixes", "missed_by"}) ||
		fix.link != "fixes" || fix.linkTo != Merged || !fix.linkUnknown || fix.handedOver != PROpen {
		t.Errorf("fix's rules: %+v", fix)
	}
	if !slices.Equal(refine.brief, build.brief) || !slices.Equal(refine.design, build.design) || !refine.pr || refine.keys != nil || refine.link != "" || refine.handedOver != PROpen {
		t.Errorf("refine's rules: %+v", refine)
	}
}

// A brief that says how to meet its objective is refused, naming every section
// that says it; one whose design sections hold only the template's notes isn't.
func TestDesignInBrief(t *testing.T) {
	notes := Task{docWith(t, "---\nid: 001-a\nkind: build\n---\n## Structure\n<!-- where the code lives -->\n\n## Plan\n\n## Size\nAbout 40 lines.\n")}
	if err := notes.DesignInBrief(); err != nil {
		t.Errorf("template notes only: %v", err)
	}
	old := "---\nid: 001-a\nkind: %s\n---\n## Structure\nA cache package.\n\n## Plan\n1. Add it.\n\n## Constraints and conventions\nNo Redis.\n"
	err := Task{docWith(t, fmt.Sprintf(old, "refine"))}.DesignInBrief()
	if err == nil || !strings.Contains(err.Error(), "001-a's brief still says how to meet its objective, under Structure, Plan and Constraints and conventions") ||
		!strings.Contains(err.Error(), "then run crew ready 001-a") {
		t.Errorf("old-shape brief: %v", err)
	}
	if err := (Task{docWith(t, fmt.Sprintf(old, "spike"))}).DesignInBrief(); err != nil {
		t.Errorf("a spike's plan: %v", err)
	}
}

// The task template has every section a kind's brief needs, and of those it
// leaves empty only the Plan, which a spike's brief needs.
func TestTemplateHasTheBriefSections(t *testing.T) {
	b, err := os.ReadFile("../../templates/task.md")
	if err != nil {
		t.Fatal(err)
	}
	headings := strings.Split(string(b), "\n")
	for _, r := range kinds {
		for _, h := range r.brief {
			if !slices.Contains(headings, h) {
				t.Errorf("%s's brief needs %s, which the template lacks", r.kind, h)
			}
		}
		for _, h := range r.design {
			if h != "## Plan" && slices.Contains(headings, h) { // a spike's brief needs a Plan
				t.Errorf("%s's brief leaves %s empty, yet the template has it", r.kind, h)
			}
		}
	}
}

func TestOrList(t *testing.T) {
	for want, items := range map[string][]string{"": nil, "low": {"low"}, "low or high": {"low", "high"}, "low, medium or high": {"low", "medium", "high"}} {
		if got := orList(items); got != want {
			t.Errorf("orList(%q) = %q, want %q", items, got, want)
		}
		if got, want := andList(items), strings.Replace(want, " or ", " and ", 1); got != want {
			t.Errorf("andList(%q) = %q, want %q", items, got, want)
		}
	}
}

func TestDoneIsFinishedAndDelivered(t *testing.T) {
	for _, s := range taskStatuses {
		delivered := s == Merged || s == Done
		if s.Delivered() != delivered {
			t.Errorf("%s.Delivered() = %v", s, !delivered)
		}
		if s.Finished() != (delivered || s == Abandoned) {
			t.Errorf("%s.Finished() = %v", s, s.Finished())
		}
		if s.Finished() && s.InFlight() {
			t.Errorf("%s is both finished and in flight", s)
		}
	}
}

func TestStillToLearn(t *testing.T) {
	task := Task{docWith(t, "## Context\n- [ ] not this one\n\n## Still to learn\n<!-- - [ ] a comment -->\n- [ ] does the API page?\n- [x] which store fits\n- [ ] is it fast enough?\n\n## Log\n")}
	if n := task.StillToLearn(); n != 2 {
		t.Errorf("StillToLearn = %d, want 2", n)
	}
}

func TestAnswer(t *testing.T) {
	for report, want := range map[string]string{
		"## Report\nAnswer: yes, it pages by cursor\n\nTried: …\n":  "yes, it pages by cursor",
		"## Report\n<!-- comment -->\n\nAnswer:   no  \nmore\n":     "no",
		"## Report\nTried: …\nAnswer: later, so it doesn't count\n": "",
		"## Report\nAnswer:\n":            "",
		"## Goal\nAnswer: not a report\n": "",
	} {
		if got := (Task{docWith(t, report+"\n## Log\n")}).Answer(); got != want {
			t.Errorf("Answer with %q = %q, want %q", report, got, want)
		}
	}
}

// Two CI rounds, one of them ticked and marked as not the branch's doing, a
// round of comments with a CI line, a question about a head that stayed red,
// and two rounds that report a conflict with main, one of them ticked. A line
// quoted in a comment, or in the brief, reports nothing.
const reportsTask = "## Context\n- [ ] CI failed on 0000000: brief\n- [ ] Conflicts with main at 0000000\n\n" +
	"## Open questions\n- [ ] CI is still red on ccc3333 after 2 CI rounds (MAX_CI_FIXES=2): test. Look at it yourself.\n\n" +
	"## Review rounds\n" +
	"### Round 1 — 2026-11-11 10:00 (PR #12)\n\n- [x] CI failed on aaa1111: test, lint (the branch didn't cause it)\n\n" +
	"### Round 2 — 2026-11-11 11:00 (PR #12)\n\n- [ ] CI failed on bbb2222: test\n\n**Conversation comments**\n- [ ] comment 5 — [link](u5)\n  > - [ ] CI failed on 9999999: quoted\n  > - [ ] Conflicts with main at 9999999\n\n" +
	"### Round 3 — 2026-11-11 12:00 (PR #12)\n\n- [ ] Automated review requested (crew review)\n\n" +
	"### Round 4 — 2026-11-11 13:00 (PR #12)\n\n- [x] Conflicts with main at eee5555\n\n" +
	"### Round 5 — 2026-11-11 14:00 (PR #12)\n\n- [ ] Conflicts with main at fff6666\n\n" +
	"## Log\n"

func TestCIFailures(t *testing.T) {
	task := Task{docWith(t, reportsTask)}
	if got := task.CIFailures(); !slices.Equal(got, []string{"aaa1111", "bbb2222"}) {
		t.Errorf("CIFailures = %q", got)
	}
	if got := (Task{docWith(t, sampleTask)}).CIFailures(); got != nil {
		t.Errorf("a task without review rounds: CIFailures = %q", got)
	}
}

func TestCIFixes(t *testing.T) {
	if got := (Task{docWith(t, reportsTask)}).CIFixes(); got != 1 {
		t.Errorf("CIFixes = %d, want 1: a round the branch didn't cause doesn't count", got)
	}
	if got := (Task{docWith(t, sampleTask)}).CIFixes(); got != 0 {
		t.Errorf("a task without review rounds: CIFixes = %d", got)
	}
}

func TestRecordCICause(t *testing.T) {
	task := Task{docWith(t, reportsTask)}
	record := func(head string, branchCaused, wantChanged bool, wantFixes int) {
		t.Helper()
		changed, err := task.RecordCICause(head, branchCaused)
		if err != nil || changed != wantChanged || task.CIFixes() != wantFixes {
			t.Errorf("RecordCICause(%s, %v) = %v, %v with CIFixes %d; want %v with CIFixes %d", head, branchCaused, changed, err, task.CIFixes(), wantChanged, wantFixes)
		}
	}
	record("bbb2222", false, true, 0)
	if !strings.Contains(contents(t, task.Doc), "\n- [ ] CI failed on bbb2222: test (the branch didn't cause it)\n") {
		t.Errorf("the round's line isn't marked:\n%s", contents(t, task.Doc))
	}
	record("bbb2222", false, false, 0) // marked already
	record("aaa1111", true, true, 1)   // the worker's later account wins
	if !strings.Contains(contents(t, task.Doc), "\n- [x] CI failed on aaa1111: test, lint\n") {
		t.Errorf("the mark isn't taken off:\n%s", contents(t, task.Doc))
	}
	record("ccc3333", false, false, 1) // only an open question reports it
	record("0000000", false, false, 1) // only the brief mentions it
	record("9999999", false, false, 1) // only a quote in a comment mentions it
	if got := task.CIFailures(); !slices.Equal(got, []string{"aaa1111", "bbb2222"}) {
		t.Errorf("CIFailures after marking = %q", got)
	}
}

func TestCIReported(t *testing.T) {
	task := Task{docWith(t, reportsTask)}
	for head, want := range map[string]bool{"aaa1111": true, "bbb2222": true, "ccc3333": true, "0000000": false, "9999999": false, "ddd4444": false} {
		if got := task.CIReported(head); got != want {
			t.Errorf("CIReported(%s) = %v, want %v", head, got, want)
		}
	}
}

func TestConflictReported(t *testing.T) {
	task := Task{docWith(t, reportsTask)}
	for sha, want := range map[string]bool{"eee5555": true, "fff6666": true, "0000000": false, "9999999": false, "aaa1111": false, "ggg7777": false} {
		if got := task.ConflictReported(sha); got != want {
			t.Errorf("ConflictReported(%s) = %v, want %v", sha, got, want)
		}
	}
}

func TestFeedbackRounds(t *testing.T) {
	// Round 1 only reports red CI; round 2 has a comment besides; round 3 asks
	// for a review; rounds 4 and 5 only report a conflict.
	if n := (Task{docWith(t, reportsTask)}).FeedbackRounds(); n != 2 {
		t.Errorf("FeedbackRounds = %d, want 2", n)
	}
	if n := (Task{docWith(t, sampleTask)}).FeedbackRounds(); n != 0 {
		t.Errorf("a task without review rounds: FeedbackRounds = %d", n)
	}
}

func TestReviewComments(t *testing.T) {
	// Round 2 has one conversation comment, with a CI line quoted in it.
	if n := (Task{docWith(t, reportsTask)}).ReviewComments(); n != 1 {
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

// Round 1 comes before any automated review; review 1 follows it. Round 2
// only reports red CI, round 3 has a review summary and an inline comment,
// and round 4 a conversation comment, after review 2.
const commentedTask = "## Automated reviews\n" +
	"### Automated review 1 — 2026-11-10 12:00\n\n- Reviewers: Claude reviewer\n\n" +
	"#### 1. [should fix] a — `a.go:1`\n- Agreed severity: should fix\n- Outcome: fixed in abc1234\n\nSummary posted\n\n" +
	"### Automated review 2 — 2026-11-11 09:00\n\n- Reviewers: Claude reviewer\n\nNo findings.\n\nSummary posted\n\n" +
	"## Review rounds\n" +
	"### Round 1 — 2026-11-10 10:00 (PR #12)\n\n**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > too early\n\n" +
	"### Round 2 — 2026-11-10 13:00 (PR #12)\n\n- [x] CI failed on aaa1111: test\n\n" +
	"### Round 3 — 2026-11-10 14:00 (PR #12)\n\n**Review summaries**\n- [x] review 7 (changes_requested)\n  > Two things.\n  > - [ ] not an item\n\n" +
	"**Inline comments**\n- [x] `a.go:3` — comment 9 — [link](u9)\n  > why?\n\n- [x] Automated review requested (comment 11)\n\n" +
	"### Round 4 — 2026-11-11 10:00 (PR #12)\n\n**Conversation comments**\n- [ ] comment 12 — [link](u12)\n  > ok\n\n" +
	"## Log\n"

func TestReviewRounds(t *testing.T) {
	rounds := Task{docWith(t, commentedTask)}.ReviewRounds()
	var got []string
	for _, r := range rounds {
		line := fmt.Sprintf("%d %s:", r.Number, r.At.Format(logTime))
		for _, cm := range r.Comments {
			line += fmt.Sprintf(" [%s: %q]", cm.Item, cm.Text)
		}
		got = append(got, line)
	}
	want := []string{
		`1 2026-11-10 10:00: [comment 5 — [link](u5): "too early"]`,
		"2 2026-11-10 13:00:",
		`3 2026-11-10 14:00: [review 7 (changes_requested): "Two things.\n- [ ] not an item"] [` + "`a.go:3`" + ` — comment 9 — [link](u9): "why?"]`,
		`4 2026-11-11 10:00: [comment 12 — [link](u12): "ok"]`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("ReviewRounds =\n%q\nwant\n%q", got, want)
	}
}

func TestCommentsAfterReview(t *testing.T) {
	var got []string
	for _, c := range (Task{docWith(t, commentedTask)}).CommentsAfterReview() {
		got = append(got, fmt.Sprintf("round %d, %s, after review %d", c.Round, c.Comment.Item, c.Review.Number))
	}
	// Round 1 came before any review, and round 2 holds no comment.
	want := []string{"round 3, review 7 (changes_requested), after review 1", "round 3, `a.go:3` — comment 9 — [link](u9), after review 1", "round 4, comment 12 — [link](u12), after review 2"}
	if !slices.Equal(got, want) {
		t.Errorf("CommentsAfterReview = %q\nwant %q", got, want)
	}
	// Without a time, a review can't be placed before a round.
	undated := strings.Replace(strings.Replace(commentedTask, "1 — 2026-11-10 12:00", "1", 1), "2 — 2026-11-11 09:00", "2", 1)
	if got := (Task{docWith(t, undated)}).CommentsAfterReview(); got != nil {
		t.Errorf("after undated reviews: %v", got)
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

func TestSetStatusToTheSameStatusLogsNothing(t *testing.T) {
	task := Task{docWith(t, sampleTask)}
	if err := task.SetStatus(PROpen); err != nil {
		t.Fatal(err)
	}
	before := contents(t, task.Doc)
	if err := task.SetStatus(PROpen); err != nil {
		t.Fatal(err)
	}
	if after := contents(t, task.Doc); after != before {
		t.Errorf("SetStatus(pr-open) on a pr-open task changed the file:\n%s", after)
	}
}

// crew stats takes a task whose "dispatched" line doesn't follow a status line
// for one dispatched before SetStatus, so the go-ahead's line goes first.
func TestSetStatusEndsAGoAheadBeforeTheStatusLine(t *testing.T) {
	task := Task{docWith(t, strings.Replace(sampleTask, "status: draft\n", "status: ready\ngo_ahead: 002-b pr-open\n", 1))}
	if err := task.SetStatus(Working); err != nil {
		t.Fatal(err)
	}
	if err := task.Log("dispatched: worker a"); err != nil {
		t.Fatal(err)
	}
	if g := task.Get("go_ahead"); g != "" {
		t.Errorf("go_ahead = %q after SetStatus(working), want it ended", g)
	}
	if dispatchedBeforeSetStatus(task.LogEntries()) {
		t.Errorf("crew stats takes the task for one dispatched before SetStatus:\n%s", contents(t, task.Doc))
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

// A message that quotes a comment's opener doesn't hide the lines after it,
// such as the status line that says when a task merged.
func TestLogEntriesReadPastAQuotedCommentOpener(t *testing.T) {
	d := docWith(t, "---\nid: 1\n---\n## Log\n- 2026-11-01 09:00 — a '<!--' in grep's output hid entry 4\n"+
		"- 2026-11-01 10:00 — status → merged\n")
	var got []string
	for _, e := range d.LogEntries() {
		got = append(got, e.msg)
	}
	if want := []string{"a '<!--' in grep's output hid entry 4", "status → merged"}; !slices.Equal(got, want) {
		t.Errorf("LogEntries = %q\nwant %q", got, want)
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

// LEDGER.md lists exactly the checks a fix's missed_by can name, in order.
func TestMissedByMatchesLedgerDoc(t *testing.T) {
	b, err := os.ReadFile("../../LEDGER.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(b), "## Fixes\n")
	rest, _, _ = strings.Cut(rest, "\n## ")
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\|").FindAllStringSubmatch(rest, -1) {
		documented = append(documented, m[1])
	}
	if !slices.Equal(documented, missedBy) {
		t.Errorf("missed_by checks in LEDGER.md %q, in crew %q", documented, missedBy)
	}
}

// LEDGER.md lists exactly crew's kinds of task, in order.
func TestKindsMatchLedgerDoc(t *testing.T) {
	b, err := os.ReadFile("../../LEDGER.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(b), "## Kinds of task\n")
	rest, _, _ = strings.Cut(rest, "\n## ")
	var documented, known []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(rest, -1) {
		documented = append(documented, m[1])
	}
	for _, r := range kinds {
		known = append(known, string(r.kind))
	}
	if !slices.Equal(documented, known) {
		t.Errorf("kinds in LEDGER.md %q, in crew %q", documented, known)
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

func TestResolveName(t *testing.T) {
	files := []string{"/h/news/2026-11-24.log", "/h/reports/2026-11-24-standup.md", "/h/reports/plan.md", "/h/logs/plan-b.log", "/h/logs/push.log"}
	for arg, want := range map[string]string{
		"2026-11-24":     "/h/news/2026-11-24.log",
		"2026-11-24.log": "/h/news/2026-11-24.log",
		"standup":        "/h/reports/2026-11-24-standup.md",
		"plan":           "/h/reports/plan.md",
		"plan.md":        "/h/reports/plan.md",
		"plan-b":         "/h/logs/plan-b.log",
		"push":           "/h/logs/push.log",
	} {
		if f, _ := resolveName(arg, files); f != want {
			t.Errorf("resolveName(%q) = %q, want %s", arg, f, want)
		}
	}
	if f, m := resolveName("2026", files); f != "" || len(m) != 2 {
		t.Errorf("a part of two names: %q, %v", f, m)
	}
	if f, m := resolveName("news", files); f != "" || len(m) != 0 {
		t.Errorf("a part of the directories, not of a name: %q, %v", f, m)
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
	// The settings deny some of Claude Code's own agents too; only crew's count here.
	var denied []string
	for _, rule := range settings.child("permissions").vals["deny"].([]any) {
		name, ok := strings.CutPrefix(rule.(string), "Agent(")
		name = strings.TrimSuffix(name, ")")
		if ok && isFile(filepath.Join("../../agents", name+".md")) {
			denied = append(denied, name)
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
		"gardener": "the gardener", "misses": "the misses agent", "user": "the user", "lead": "a lead",
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
	want := "From proposal `001-a.md` (" + short.Path + "), written by the user. Run normal intake: confirm the objective and its gates with the user.\n" +
		"\nRevisit when: a second rule varies by place\n" +
		"\nWhy it matters:\nRules vary by place.\n"
	if got := short.TaskContext(); got != want {
		t.Errorf("a short proposal's context:\n%s\nwant:\n%s", got, want)
	}

	full := Proposal{docWith(t, "---\nid: 1\n---\n# T\n\n## Why it matters\nw\n\n## Proposal\np\n\n## First step\nf\n\n## Log\n")}
	want = "From proposal `001-a.md` (" + full.Path + "). Run normal intake: confirm the objective and its gates with the user.\n" +
		"\nWhy it matters:\nw\n\nProposal:\np\n\nFirst step:\nf\n"
	if got := full.TaskContext(); got != want {
		t.Errorf("a proposal without a source:\n%s\nwant:\n%s", got, want)
	}
}

func TestProposalCheckSetKeepsAnAcceptedProposalOnItsTask(t *testing.T) {
	for _, c := range []struct {
		status, task, key, value string
		refused                  bool
	}{
		{"accepted", "047-a", "task", "048-b", true},
		{"accepted", "047-a", "task", "", true},
		{"accepted", "047-a", "status", "open", true},
		{"accepted", "047-a", "task", "047-a", false},
		{"accepted", "047-a", "status", "accepted", false},
		{"accepted", "047-a", "size", "S", false},
		{"accepted", "", "status", "open", false},
		{"open", "047-a", "task", "048-b", false},
		{"open", "", "status", "accepted", false},
	} {
		pr := Proposal{docWith(t, "---\nstatus: "+c.status+"\ntask: "+c.task+"\n---\n## Log\n")}
		err := pr.CheckSet(c.key, c.value)
		if (err != nil) != c.refused {
			t.Errorf("CheckSet(%s, %q) on a proposal %s for task %q = %v, want refused %v", c.key, c.value, c.status, c.task, err, c.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "accepted for task 047-a,") {
			t.Errorf("the refusal doesn't name the task: %v", err)
		}
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

// A task whose worker stalled again, so crew nudge asked the lead, with each
// kind of question someone else may ask beside it. after is what the Log says
// after the question, one line each.
func nudgeQuestionTask(t *testing.T, after ...string) Task {
	text := "---\nid: 1\nstatus: needs-input\n---\n## Open questions\n" +
		"- [ ] Which way? A or B.\n" +
		"- [ ] " + nudgeQuestionStart + " again after a nudge, with no new commit.\n" +
		"- [ ] CI still red on abc1234\n" +
		"- [ ] Comment from a collaborator\n\n## Log\n" +
		"- 2026-11-24 16:00 — status → working\n" +
		"- 2026-11-24 16:30 — nudge: stalled again at aaa1111; for the lead\n" +
		"- 2026-11-24 16:30 — status → needs-input\n"
	for _, l := range after {
		text += "- 2026-11-24 17:00 — " + l + "\n"
	}
	return Task{docWith(t, text)}
}

func TestWorkerMovedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		after []string
		head  string
		want  string
	}{
		"tick's own needs-input is not moving on": {nil, "aaa1111", ""},
		"a repeated tick is not moving on":        {[]string{"status → needs-input"}, "aaa1111", ""},
		"a new commit alone":                      {nil, "bbb2222", "new commit bbb2222"},
		"no known head, no new commit":            {nil, "", ""},
		"a status change":                         {[]string{"status → pr-open"}, "aaa1111", "status pr-open"},
		"a followup":                              {[]string{"followup (resume): worker w1 — note: go on"}, "aaa1111", "a followup to its worker"},
	} {
		if got := nudgeQuestionTask(t, tc.after...).WorkerMovedOn(tc.head); got != tc.want {
			t.Errorf("%s: WorkerMovedOn(%q) = %q, want %q", name, tc.head, got, tc.want)
		}
	}
	// Only what came after the last question counts.
	task := nudgeQuestionTask(t, "status → working", "nudge: worktree missing; for the lead")
	if got := task.WorkerMovedOn("bbb2222"); got != "" {
		t.Errorf("WorkerMovedOn after a later question = %q, want none", got)
	}
}

func TestSettleNudgeQuestionsLeavesOthersOpen(t *testing.T) {
	task := nudgeQuestionTask(t)
	if n := task.NudgeQuestions(); n != 1 {
		t.Fatalf("NudgeQuestions = %d, want 1", n)
	}
	if err := task.SettleNudgeQuestions("new commit bbb2222"); err != nil {
		t.Fatal(err)
	}
	if n := task.NudgeQuestions(); n != 0 {
		t.Errorf("NudgeQuestions = %d after settling, want 0", n)
	}
	if n := task.OpenQuestions(); n != 3 {
		t.Errorf("OpenQuestions = %d after settling, want the 3 that aren't nudge questions", n)
	}
	want := "- [x] " + nudgeQuestionStart + " again after a nudge, with no new commit. Settled: new commit bbb2222."
	if !strings.Contains(contents(t, task.Doc), want) {
		t.Errorf("the settled question isn't marked as settled:\n%s", contents(t, task.Doc))
	}
}

// A handed-over task that tick marked needs-input for a nudge question goes
// back to pr-open, not to its worker's turn.
func TestStatusBeforeNeedsInputKeepsAHandOver(t *testing.T) {
	task := nudgeQuestionTask(t, "status → pr-open", "status → needs-input")
	if got := statusBeforeNeedsInput(task); got != PROpen {
		t.Errorf("statusBeforeNeedsInput = %s, want pr-open", got)
	}
	if got := backToWorker(task); got != Working {
		t.Errorf("backToWorker = %s, want working: a followup note sends the worker back to work", got)
	}
}
