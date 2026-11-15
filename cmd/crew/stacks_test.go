package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stackRepo is a clone whose origin, as last fetched, has main and the branches
// named, all at its one commit, which every local branch named also holds.
func stackRepo(t *testing.T, branches ...string) string {
	t.Helper()
	repo := t.TempDir()
	runIn(t, repo, "git", "init", "-q", "-b", "main")
	runIn(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	runIn(t, repo, "git", "update-ref", "refs/remotes/origin/main", "HEAD")
	for _, b := range branches {
		runIn(t, repo, "git", "branch", b)
		runIn(t, repo, "git", "update-ref", "refs/remotes/origin/"+b, "HEAD")
	}
	return repo
}

// stackTask writes a task file in project p of a ledger at dir, from front matter
// lines.
func stackTask(t *testing.T, dir, name string, fields ...string) Task {
	t.Helper()
	p := filepath.Join(dir, "projects", "p", "tasks", name+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: " + name + "\n" + strings.Join(fields, "\n") + "\n---\n# " + name + "\n\n## Log\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Task{Doc{p}}
}

func TestRetargetReason(t *testing.T) {
	repo := stackRepo(t, "agent/parent")
	pr := []string{"status: pr-open", "pr: https://github.com/o/r/pull/7", "pr_state: open"}
	for _, c := range []struct {
		name   string
		fields []string
		want   string
	}{
		{"a stacked task whose PR targets another branch", slices.Concat(pr, []string{"base: agent/parent", "pr_base: main"}), "PR targets main, not agent/parent"},
		{"an unstacked task whose PR targets another branch", slices.Concat(pr, []string{"base: main", "pr_base: agent/gone"}), "PR targets agent/gone, not main"},
		{"a PR that targets the task's base", slices.Concat(pr, []string{"base: agent/parent", "pr_base: agent/parent"}), ""},
		{"a PR whose base crew sync hasn't read", slices.Concat(pr, []string{"base: agent/parent", "pr_base:"}), ""},
		{"a base that isn't on origin yet", slices.Concat(pr, []string{"base: agent/new", "pr_base: main"}), ""},
		{"no PR", []string{"status: working", "pr:", "base: agent/parent", "pr_base: main"}, ""},
		{"a PR closed unmerged", []string{"status: pr-open", "pr: https://github.com/o/r/pull/7", "pr_state: closed", "base: agent/parent", "pr_base: main"}, ""},
		{"a task that isn't in flight", []string{"status: merged", "pr: https://github.com/o/r/pull/7", "pr_state: merged", "base: agent/parent", "pr_base: main"}, ""},
	} {
		task := stackTask(t, t.TempDir(), "001-a", c.fields...)
		if got := retargetReason(task, repo); got != c.want {
			t.Errorf("%s: retargetReason = %q, want %q", c.name, got, c.want)
		}
	}
}

// A retarget and a rebase are separate questions: a task can need both, and a
// parent that only needs a retarget doesn't hold back its child's rebase.
func TestRetargetIsNotARestack(t *testing.T) {
	repo := stackRepo(t, "agent/grand", "agent/parent", "agent/child")
	dir := t.TempDir()
	stackTask(t, dir, "001-grand", "status: pr-open", "base: main", "branch: agent/grand")
	parent := stackTask(t, dir, "002-parent", "status: pr-open", "base: agent/grand", "branch: agent/parent",
		"pr: https://github.com/o/r/pull/2", "pr_state: open", "pr_base: main")
	child := stackTask(t, dir, "003-child", "status: pr-open", "base: agent/parent", "branch: agent/child",
		"pr: https://github.com/o/r/pull/3", "pr_state: open", "pr_base: agent/grand")
	idx := (&Crew{Home: dir}).branches("p")

	if got := retargetReason(parent, repo); got != "PR targets main, not agent/grand" {
		t.Fatalf("parent's retargetReason = %q", got)
	}
	if got := idx.restackReason(parent, repo); got != "" {
		t.Errorf("a retarget alone is no rebase, but restackReason = %q", got)
	}
	if idx.parentNeedsRestack(child, repo) {
		t.Error("a parent that only needs a retarget holds back its child")
	}

	// The parent's branch moves: the child needs a rebase and a retarget.
	runIn(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "more")
	runIn(t, repo, "git", "update-ref", "refs/remotes/origin/agent/parent", "HEAD")
	if got := idx.restackReason(child, repo); got != "002-parent moved" {
		t.Errorf("child's restackReason = %q, want 002-parent moved", got)
	}
	if got := retargetReason(child, repo); got != "PR targets agent/grand, not agent/parent" {
		t.Errorf("child's retargetReason = %q", got)
	}
}
