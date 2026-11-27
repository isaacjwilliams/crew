package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Every row with a PR shows its checks beside its number: from GitHub for a
// pr-open task crew status asked about, and as crew sync recorded them for one
// GitHub didn't answer for and for a task of any other status.
func TestEveryRowWithAPRShowsItsChecks(t *testing.T) {
	const url = "https://github.com/o/r/pull/7"
	for _, from := range []struct {
		name   string
		status string
		read   func(ci ciState) (prRead, bool) // GitHub's answer, and whether crew status asked
	}{
		{"pr-open, from GitHub", "pr-open", func(ci ciState) (prRead, bool) {
			return prRead{pr: gitHubPR{State: "OPEN", CI: ci}}, true
		}},
		{"pr-open, GitHub not answering", "pr-open", func(ciState) (prRead, bool) {
			return prRead{err: errors.New("gh: no answer")}, true
		}},
		{"working", "working", func(ciState) (prRead, bool) { return prRead{}, false }},
	} {
		for _, tc := range []struct {
			state  string // how crew sync records it, or "unknown" for a PR it hasn't
			ci     ciState
			want   string
			passed bool
		}{
			{"running", ciRunning, "#7 CI⧗", false},
			{"failing", ciFailing, "#7 CI✗", false},
			{"cancelled", ciCancelled, "#7 CI⊘", false},
			{"passing", ciPassing, "#7 CI✓", true},
			{"no checks", "", "#7 CI-", false},
			{"unknown", "", "#7 CI?", false},
		} {
			t.Run(from.name+", "+tc.state, func(t *testing.T) {
				front := "---\nstatus: " + from.status + "\npr: " + url + "\nci_state: " + string(tc.ci) + "\npr_state: open\n---\n"
				s := &statusView{prs: map[string]prRead{}}
				if read, asked := from.read(tc.ci); asked {
					s.prs[url] = read
				}
				if tc.state == "unknown" {
					front = strings.Replace(front, "pr_state: open", "pr_state:", 1)
					delete(s.prs, url)
				}
				task := Task{docWith(t, front)}
				pr, _ := s.prOf(task)
				live := rowFields(taskRow{Task: task, PR: pr})[prField].live
				if got := uncolored(live); got != tc.want {
					t.Errorf("PR = %q, want %q", got, tc.want)
				}
				if strings.Contains(uncolored(live), "✓") != tc.passed {
					t.Errorf("PR %q reads as passing: %v, want %v", uncolored(live), !tc.passed, tc.passed)
				}
				if red := lipgloss.NewStyle().Foreground(lipgloss.Red).Render("CI✗"); tc.ci == ciFailing && !strings.Contains(live, red) {
					t.Errorf("PR %q doesn't show CI in red", live)
				}
			})
		}
	}
}

// everyNoteLive is how the live view words each row of everyNoteLedger, with
// its color taken out: its head, its PR, and its notes. The strip words the
// rest, as the plain text does (everyNoteText).
const everyNoteLive = `
goal 001-full |  | ▮
    301-in-flight |  |
    1  302-next |  |
goal 002-bad-cap |  | max_in_flight?
    1  303-next |  |
091-open-parent | #91 CI✓ | merge low
  └ 110-stacked | #110 CI✓ | ⋯ low
  └ 128-branches-unread | #128 CI✓ | ⋯ low
  └ 209-branches-unread |  |
101-merge-now | #101 CI✓ | merge low
102-findings-left | #102 CI✓ | merge 2 findings medium
103-worker-busy | #103 CI✓ | ⧗ low
104-round-not-sent | #104 CI✓ | ⧗ low
105-checking-conflicts | #105 CI✓ ↯? | low
106-ci-running | #106 CI⧗ | low
107-not-reviewed | #107 CI✓ | ⧗ low
108-restack | #108 CI✓ | ↻ low
109-retarget | #109 CI✓ | ↻ low
111-based-elsewhere | #111 CI✓ | ↻ low
112-conflicts | #112 CI✓ ↯ | low
113-ci-failing | #113 CI✗ | low
114-draft | #114 CI✓ ◌ | low
115-no-security-review | #115 CI✓ | ⚷ high
116-behind | #116 CI✓ ⇣ | low
117-ci-cancelled | #117 CI⊘ | low
118-changes-requested | #118 CI✓ ✎ | low
119-review-required | #119 CI✓ ⦿ | low
120-asks | #120 CI✓ | 1 question low
121-blocked | #121 CI✓ ⊖ | low
122-merged | #122 CI✓ ⋈ | low
123-closed | #123 CI✓ ⊗ | low
124-last-sync | #124 CI✗ ✎ | ◷ low
125-never-synced | #125 CI? | low
126-not-asked | #126 CI- | low
127-no-pr |  | no PR low
129-no-risk-tier | #129 CI✓ | merge no risk tier
130-secured | #130 CI✓ | merge high
201-changes-requested | #201 CI✗ ✎ |
202-draft | #202 CI⧗ ◌ ↯ ⇣ |
203-closed | #203 CI- ⊗ |
204-approved | #204 CI✓ ⊖ ✦ |
205-review-required | #205 CI⊘ ↯? ⦿ |
206-no-pr-yet |  |
207-restack |  | ↻
208-retarget | #208 CI- | ↻
210-asks |  | 1 question
211-interrupted |  | ǁ
212-review-off |  |
213-merged-pr | #213 CI- ⋈ |
220-learning |  | ⌕
221-spike |  |
230-by-hand |  | dispatch
231-bad-go-ahead |  | go_ahead? dispatch
232-go-ahead-lost |  | dispatch
233-go-ahead-met |  | »
234-go-ahead-once |  | priority? ⋯
235-bad-dependency |  | depends_on? dispatch
236-after |  | ⋯ dispatch
237-after-abandoned |  | dep abandoned dispatch
238-after-many |  | dep abandoned dispatch
239-on-parent |  | dispatch
311-in-flight |  |
312-goal-full |  | ▮ dispatch
313-goal-bad-cap |  | max_in_flight? dispatch
`

// The live view words each note as a PR mark, the user's words, a sign, or not
// at all, leaving it to the strip.
func TestLiveViewWordsEveryNote(t *testing.T) {
	c, reads := everyNoteLedger(t)
	var got []string
	var walk func(lines []statusLine, depth int)
	walk = func(lines []statusLine, depth int) {
		for _, l := range lines {
			if l.hasFields() {
				f := l.fields
				got = append(got, strings.TrimRight(strings.Repeat("  ", depth)+l.head+" | "+uncolored(f[prField].live)+" | "+uncolored(f[notesField].live), " "))
			}
			walk(l.kids, depth+1)
		}
	}
	for _, project := range reportLines(buildReport(c, []string{"p"}, false, reads)) {
		walk(project.kids, 0)
	}
	if got := "\n" + strings.Join(got, "\n") + "\n"; got != everyNoteLive {
		t.Errorf("live rows:%s\nwant:%s", got, everyNoteLive)
	}
}

// The user's words are bold, and a PR that can merge says so in green.
func TestTheUsersWordsStandOut(t *testing.T) {
	bold := lipgloss.NewStyle().Bold(true)
	for _, tc := range []struct {
		note wording
		want string
	}{
		{questionsNote(2), bold.Render("2 questions")},
		{lookNote(Task{docWith(t, "---\nrisk: high\n---\n")}), bold.Render("high")},
		{mergeCheck{AwaitingUser: 1}.wording(), bold.Foreground(lipgloss.Green).Render("merge") + " " + bold.Render("1 finding")},
	} {
		if tc.note.live != tc.want {
			t.Errorf("%q shows as %q, want %q", tc.note.text, tc.note.live, tc.want)
		}
	}
}

// A merge check that went by the last sync says so in the live view too, so a
// merge GitHub couldn't confirm doesn't look like one it did.
func TestAMergeCheckFromTheLastSyncShowsItsSign(t *testing.T) {
	for _, tc := range []struct {
		check mergeCheck
		want  string
	}{
		{mergeCheck{LastSync: true}, "merge ◷"},
		{mergeCheck{LastSync: true, AwaitingUser: 1}, "merge 1 finding ◷"},
		{mergeCheck{LastSync: true, InTheWay: []wording{stripOnly("CI failing")}}, "◷"},
		{mergeCheck{}, "merge"},
	} {
		if got := uncolored(tc.check.wording().live); got != tc.want {
			t.Errorf("%+v shows %q, want %q", tc.check, got, tc.want)
		}
	}
}

// A draft whose items still to learn can't be read asks the user to fix that.
func TestADraftWhoseItemsCantBeReadAsksTheUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "p", "tasks") // never migrated
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "001-a.md")
	if err := os.WriteFile(p, []byte("---\nstatus: draft\n---\n## Log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := (&statusView{}).learning(Task{Doc{p}})
	if want := usersWords.Render("to learn?"); got.live != want || !strings.Contains(got.text, "run crew migrate") {
		t.Errorf("learning = %q, want %q in full and %q live", got, "run crew migrate", want)
	}
}

// Beside its checks, a PR shows what GitHub says of it: merged or closed, or
// what's in its way and whether it's approved.
func TestPRMarks(t *testing.T) {
	open := gitHubPR{State: "OPEN", CI: ciPassing, MergeState: "CLEAN"}
	for _, tc := range []struct {
		name string
		pr   func(*gitHubPR)
		want string
	}{
		{"clean", func(*gitHubPR) {}, "CI✓"},
		{"merged", func(p *gitHubPR) { p.State = "MERGED" }, "CI✓ ⋈"},
		{"closed", func(p *gitHubPR) { p.State, p.Draft = "CLOSED", true }, "CI✓ ⊗"},
		{"draft", func(p *gitHubPR) { p.Draft = true }, "CI✓ ◌"},
		{"conflicts", func(p *gitHubPR) { p.Mergeable = conflicting }, "CI✓ ↯"},
		{"GitHub still checking for conflicts", func(p *gitHubPR) { p.Mergeable = mergeableUnknown }, "CI✓ ↯?"},
		{"behind", func(p *gitHubPR) { p.MergeState = "BEHIND" }, "CI✓ ⇣"},
		{"approved", func(p *gitHubPR) { p.ReviewDecision = "APPROVED" }, "CI✓ ✦"},
		{"changes requested", func(p *gitHubPR) { p.ReviewDecision = "CHANGES_REQUESTED" }, "CI✓ ✎"},
		{"review required", func(p *gitHubPR) { p.ReviewDecision, p.MergeState = "REVIEW_REQUIRED", "BLOCKED" }, "CI✓ ⦿"},
		{"blocked by a rule", func(p *gitHubPR) { p.MergeState = "BLOCKED" }, "CI✓ ⊖"},
		{"blocked while CI runs", func(p *gitHubPR) { p.CI, p.MergeState = ciRunning, "BLOCKED" }, "CI⧗"},
		{"blocked with no checks", func(p *gitHubPR) { p.CI, p.MergeState = "", "BLOCKED" }, "CI- ⊖"},
		{"several", func(p *gitHubPR) {
			p.CI, p.Draft, p.Mergeable, p.MergeState, p.ReviewDecision = ciFailing, true, conflicting, "BEHIND", "APPROVED"
		}, "CI✗ ◌ ↯ ⇣ ✦"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := open
			tc.pr(&pr)
			if got := uncolored(prMarks(pr)); got != tc.want {
				t.Errorf("marks = %q, want %q", got, tc.want)
			}
		})
	}
}

// Without color, every sign still says what it means: no two look alike, and
// each of the characters it's made of takes one cell.
func TestNoTwoSignsLookAlike(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "status_wording.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	signs := map[string]string{} // a symbol → the sign it's for
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, v := range spec.Values {
			lit, ok := v.(*ast.CompositeLit)
			if !ok || len(lit.Elts) == 0 {
				continue
			}
			if typ, _ := lit.Type.(*ast.Ident); typ == nil || typ.Name != "sign" {
				continue
			}
			symbol, err := strconv.Unquote(lit.Elts[0].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			name := spec.Names[i].Name
			if other, ok := signs[symbol]; ok {
				t.Errorf("%s and %s are both %q", other, name, symbol)
			}
			signs[symbol] = name
			for _, r := range symbol {
				if w := lipgloss.Width(string(r)); w != 1 {
					t.Errorf("%s's %q takes %d cells", name, r, w)
				}
			}
		}
		return true
	})
	if len(signs) < 16 {
		t.Fatalf("found %d signs in status_wording.go: %v", len(signs), signs)
	}
}
