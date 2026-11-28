package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixtures below spell tags inside Go strings, never at the start of a line,
// so crew's own tree holds no line that reads as a tag.

// The Contract of a component named guard, with two obligations.
const guardReadme = "# Guard\n\nKeeps each agent's edits in bounds.\n\n## Obligations\n\n" +
	"- **G1** Exit 0 allows the edit.\n" +
	"- **G2** Exit 2 blocks it.\n"

// What crew contract says of a line meant as an obligation or a tag that isn't
// in its form.
const (
	badObligation = `contract/guard/README.md:%d: an obligation is a line "- **G7** <what it promises>": a dash, its ID in bold (capital letters, then digits), a sentence`
	badTag        = `a tag is one comment naming one obligation, as in "// Contract: guard/G7" or "# Contract: guard/G7"`
)

// contractRepo is a new git repo holding files, from paths to contents, all
// committed.
func contractRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	runIn(t, repo, "git", "init", "-q")
	addFiles(t, repo, files)
	runIn(t, repo, "git", "add", "-A")
	runIn(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	return repo
}

// addFiles writes files, from paths to contents, in the working tree at repo.
func addFiles(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runContract runs crew contract in dir and returns its exit code and output.
func runContract(t *testing.T, dir string) (int, string) {
	t.Helper()
	t.Chdir(dir)
	done := watchTerminal(t)
	code := run([]string{"contract"})
	return code, done()
}

// treeSnapshot is every file and directory under root, .git included, with its
// mode, its time of change and a hash of its content.
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %v %v", p, info.Mode(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, " %x", sha256.Sum256(content))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestContractListsEachObligationWithItsTests(t *testing.T) {
	repo := contractRepo(t, map[string]string{
		"contract/guard/README.md":  guardReadme,
		"contract/ledger/README.md": "# Ledger\n\n- **L1** Front matter is flat key: value lines.\n",
		"guard/guard_test.go":       "package guard\n\n// Contract: guard/G1\n// Contract: guard/G2\nfunc TestGuard(t *testing.T) {}\n",
		"tests/guard.sh":            "# Contract: guard/G2\ncheck 'an edit outside the worktree is blocked'\n",
		"ledger/ledger_test.go":     "package ledger\n\n\t// Contract: ledger/L1\nfunc TestFrontMatter(t *testing.T) {}\n",
	})
	code, out := runContract(t, repo)
	t.Logf("crew contract:\n%s", out)
	want := "guard/G1   guard/guard_test.go\n" +
		"guard/G2   guard/guard_test.go, tests/guard.sh\n" +
		"ledger/L1  ledger/ledger_test.go\n" +
		"3 of 3 obligations have a test.\n"
	if code != 0 || out != want {
		t.Errorf("exit %d with\n%s\nwant exit 0 with\n%s", code, out, want)
	}
}

func TestContractFailsOnEachProblem(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			"an obligation with no tag",
			map[string]string{"guard_test.go": "// Contract: guard/G1\n"},
			[]string{"contract/guard/README.md:8: guard/G2 has no test tagged for it"},
		},
		{
			"a tag naming an obligation that doesn't exist",
			map[string]string{"guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n// Contract: guard/G9\n"},
			[]string{"guard_test.go:3: the tag names guard/G9, which contract/guard/README.md doesn't list"},
		},
		{
			"a tag naming a component that doesn't exist",
			map[string]string{"guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n// Contract: ledger/L1\n"},
			[]string{"guard_test.go:3: the tag names ledger/L1, but there's no component ledger (no contract/ledger/README.md)"},
		},
		{
			"an obligation written with a star",
			map[string]string{
				"contract/guard/README.md": guardReadme + "* **G19** A path it can't resolve blocks.\n",
				"guard_test.go":            "// Contract: guard/G1\n// Contract: guard/G2\n",
			},
			[]string{fmt.Sprintf(badObligation, 9)},
		},
		{
			"obligations in bold made with underscores",
			map[string]string{
				"contract/guard/README.md": guardReadme + "- __G3__ A path it can't resolve blocks.\n* __G4__ Input that isn't JSON blocks.\n",
				"guard_test.go":            "// Contract: guard/G1\n// Contract: guard/G2\n",
			},
			[]string{fmt.Sprintf(badObligation, 9), fmt.Sprintf(badObligation, 10)},
		},
		{
			"obligations numbered, in lowercase, or with a colon",
			map[string]string{
				"contract/guard/README.md": guardReadme + "1. **G3** Input that isn't JSON blocks.\n- **g4** It checks file_path.\n- **G5**: It checks notebook_path.\n",
				"guard_test.go":            "// Contract: guard/G1\n// Contract: guard/G2\n",
			},
			[]string{
				fmt.Sprintf(badObligation, 9),
				fmt.Sprintf(badObligation, 10),
				fmt.Sprintf(badObligation, 11),
			},
		},
		{
			"a tag in lowercase",
			map[string]string{"guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n# contract: guard/G1\n"},
			[]string{"guard_test.go:3: " + badTag},
		},
		{
			"a tag naming its component in capitals",
			map[string]string{"guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n// Contract: Guard/G2\n"},
			[]string{"guard_test.go:3: " + badTag},
		},
		{
			"a tag naming two obligations on one line",
			map[string]string{"guard.sh": "# Contract: guard/G1, guard/G2\n"},
			[]string{
				"guard.sh:1: " + badTag,
				"contract/guard/README.md:7: guard/G1 has no test tagged for it",
				"contract/guard/README.md:8: guard/G2 has no test tagged for it",
			},
		},
		{
			"two obligations of one component with the same ID",
			map[string]string{
				"contract/guard/README.md": guardReadme + "- **G1** Exit 1 means crew couldn't run.\n",
				"guard_test.go":            "// Contract: guard/G1\n// Contract: guard/G2\n",
			},
			[]string{"contract/guard/README.md:9: guard/G1 is listed twice, here and at contract/guard/README.md:7"},
		},
		{
			"a component with no README",
			map[string]string{"contract/ledger/ledger_test.go": "package ledger\n", "guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n"},
			[]string{"contract/ledger: a component needs a README.md that lists its obligations"},
		},
		{
			"a component named in capitals",
			map[string]string{"contract/Ledger/README.md": "- **L1** Front matter is flat.\n", "guard_test.go": "// Contract: guard/G1\n// Contract: guard/G2\n"},
			[]string{"contract/Ledger: a component's name is lowercase letters, digits and dashes"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"contract/guard/README.md": guardReadme}
			for name, content := range c.files {
				files[name] = content
			}
			repo := contractRepo(t, files)
			check, err := checkContract(repo)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(check.problems, c.want) {
				t.Errorf("problems:\n%s\nwant\n%s", strings.Join(check.problems, "\n"), strings.Join(c.want, "\n"))
			}
			if code, out := runContract(t, repo); code != 1 {
				t.Errorf("exit %d with\n%s\nwant exit 1", code, out)
			}
		})
	}
}

func TestContractAllowsCodeWithNoContract(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"a repo with no Contract",
			map[string]string{"guard/guard.go": "package guard\n", "guard/guard_test.go": "package guard\n"},
			"No Contract here: no contract/<component>/README.md.\n",
		},
		{
			"a Contract that lists no obligations yet",
			map[string]string{"contract/guard/README.md": "# Guard\n\nObligations to come.\n"},
			"0 of 0 obligations have a test.\n",
		},
		{
			"a repo where only one of two components has a Contract",
			map[string]string{
				"contract/guard/README.md": guardReadme,
				"guard/guard_test.go":      "// Contract: guard/G1\n// Contract: guard/G2\n",
				"ledger/ledger.go":         "package ledger\n",
				"ledger/ledger_test.go":    "package ledger\n",
			},
			"guard/G1  guard/guard_test.go\nguard/G2  guard/guard_test.go\n2 of 2 obligations have a test.\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := runContract(t, contractRepo(t, c.files))
			if code != 0 || out != c.want {
				t.Errorf("exit %d with\n%s\nwant exit 0 with\n%s", code, out, c.want)
			}
		})
	}
}

func TestContractReadsTagsInAnyLanguageButMarkdown(t *testing.T) {
	repo := contractRepo(t, map[string]string{
		"contract/guard/README.md": guardReadme + "- **G3** A path that leads outside blocks.\n- **G4** Input that isn't JSON blocks.\n",
		"guard/guard_test.go":      "package guard\n\n// Contract: guard/G1\nfunc TestGuard(t *testing.T) {}\n",
		"tests/guard.sh":           "#!/bin/sh\n# Contract: guard/G2\n",
		"spec/guard_spec.rb":       "describe Guard do\n  # Contract: guard/G3\n  it 'blocks' do\n  end\nend\n",
		"docs/guard.md":            "# Contract: guard/G4\n\nA heading, not a tag.\n",
	})
	check, err := checkContract(repo)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, o := range check.obligations {
		got[o.id] = o.tests
	}
	want := map[string][]string{
		"guard/G1": {"guard/guard_test.go"},
		"guard/G2": {"tests/guard.sh"},
		"guard/G3": {"spec/guard_spec.rb"},
		"guard/G4": nil,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tests by obligation: %v, want %v", got, want)
	}
}

func TestContractNeedsNoHomeAndWritesNothing(t *testing.T) {
	t.Setenv("CREW_HOME", t.TempDir()) // no config/defaults.env: not a crew home
	repo := contractRepo(t, map[string]string{
		"contract/guard/README.md": guardReadme,
		"guard_test.go":            "// Contract: guard/G1\n// Contract: guard/G2\n",
	})
	addFiles(t, repo, map[string]string{"new_test.go": "// Contract: guard/G1\n"})
	before := treeSnapshot(t, repo)
	if code, out := runContract(t, repo); code != 0 {
		t.Errorf("exit %d with\n%s\nwant exit 0", code, out)
	}
	if after := treeSnapshot(t, repo); after != before {
		t.Errorf("the tree changed:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestContractReadsTheWholeRepoButWhatGitIgnores(t *testing.T) {
	repo := contractRepo(t, map[string]string{
		".gitignore":               "build/\n",
		"contract/guard/README.md": guardReadme,
		"guard/guard.go":           "package guard\n",
	})
	addFiles(t, repo, map[string]string{
		"build/guard_test.go": "// Contract: guard/G1\n", // a build output, ignored
		"guard/new_test.go":   "// Contract: guard/G2\n", // a new test, not committed yet
	})
	code, out := runContract(t, filepath.Join(repo, "guard"))
	want := "guard/G1  no test\n" +
		"guard/G2  guard/new_test.go\n" +
		"1 of 2 obligations have a test.\n" +
		"contract/guard/README.md:7: guard/G1 has no test tagged for it\n" +
		"crew: the Contract isn't kept: 1 problem\n"
	if code != 1 || out != want {
		t.Errorf("exit %d with\n%s\nwant exit 1 with\n%s", code, out, want)
	}
}

func TestContractReadsTheWorkingTreeNotTheIndex(t *testing.T) {
	repo := contractRepo(t, map[string]string{
		"contract/guard/README.md":  guardReadme,
		"contract/ledger/README.md": "- **L1** Front matter is flat.\n",
		"contract/ledger/notes.txt": "Kept for later.\n",
		"contract/Old/README.md":    "- **O1** Gone.\n",
		"guard_test.go":             "// Contract: guard/G1\n",
		"old_test.go":               "// Contract: guard/G2\n// Contract: ledger/L1\n",
		"contract/spare/README.md":  "- **S1** Gone.\n",
		"tools/run.sh":              "# Contract: guard/G1\n",
	})
	for _, f := range []string{"contract/ledger/README.md", "contract/Old/README.md", "old_test.go"} {
		if err := os.Remove(filepath.Join(repo, f)); err != nil {
			t.Fatal(err)
		}
	}
	// Two directories replaced by files of the same name.
	for _, dir := range []string{"contract/spare", "tools"} {
		if err := os.RemoveAll(filepath.Join(repo, dir)); err != nil {
			t.Fatal(err)
		}
	}
	addFiles(t, repo, map[string]string{"contract/spare": "A note.\n", "tools": "A note.\n"})
	want := []string{
		"contract/ledger: a component needs a README.md that lists its obligations",
		"contract/guard/README.md:8: guard/G2 has no test tagged for it",
	}
	for _, staged := range []bool{false, true} {
		if staged {
			runIn(t, repo, "git", "add", "-A")
		}
		check, err := checkContract(repo)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(check.problems, want) {
			t.Errorf("with the deletions staged: %v, problems:\n%s\nwant\n%s", staged, strings.Join(check.problems, "\n"), strings.Join(want, "\n"))
		}
	}
}
