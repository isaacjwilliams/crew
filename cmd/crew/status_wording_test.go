package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
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
