package main

import (
	"os"
	"os/exec"
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
		if got := (Branches{}).retargetReason(task, repo); got != c.want {
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

	if got := idx.retargetReason(parent, repo); got != "PR targets main, not agent/grand" {
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
	if got := idx.retargetReason(child, repo); got != "PR targets agent/grand, not agent/parent" {
		t.Errorf("child's retargetReason = %q", got)
	}
}

// Once a parent merges, a PR belongs on what the parent merged into. GitHub moves
// it there itself when it deletes the parent's branch, and a clone that fetched
// without pruning still has that branch: no retarget back to it.
func TestRetargetAfterAParentMerged(t *testing.T) {
	repo := stackRepo(t, "agent/parent", "agent/child")
	dir := t.TempDir()
	stackTask(t, dir, "001-parent", "status: merged", "base: main", "branch: agent/parent")
	child := stackTask(t, dir, "002-child", "status: pr-open", "base: agent/parent", "branch: agent/child",
		"pr: https://github.com/o/r/pull/2", "pr_state: open", "pr_base: main")
	idx := (&Crew{Home: dir}).branches("p")
	if got := idx.retargetReason(child, repo); got != "" {
		t.Errorf("a PR GitHub moved to main is told to go back: %q", got)
	}
	child.Set("pr_base", "agent/parent")
	if got := idx.retargetReason(child, repo); got != "PR targets agent/parent, not main" {
		t.Errorf("a PR still on the merged parent's branch: retargetReason = %q", got)
	}
}

// A retarget alone suits a branch that needs no rebase: for an unstacked task,
// one that starts on its base. One moved off an abandoned parent still holds
// that parent's commits.
func TestOnBase(t *testing.T) {
	repo := stackRepo(t, "agent/gone")
	runIn(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "the parent's work")
	runIn(t, repo, "git", "update-ref", "refs/remotes/origin/agent/gone", "HEAD")
	gone := gitOut(t, repo, "rev-parse", "HEAD")
	start := gitOut(t, repo, "rev-parse", "refs/remotes/origin/main")
	dir := t.TempDir()
	idx := (&Crew{Home: dir}).branches("p")
	for _, c := range []struct {
		name, baseSHA string
		want          bool
	}{
		{"a branch that starts on its base", start, true},
		{"a branch that still holds an abandoned parent's commits", gone, false},
		{"a task with no base_sha", "", false},
	} {
		task := stackTask(t, dir, "001-a", "status: pr-open", "base: main", "base_sha: "+c.baseSHA)
		if got := idx.onBase(task, repo); got != c.want {
			t.Errorf("%s: onBase = %v, want %v", c.name, got, c.want)
		}
	}
}

// A first hand-over's automated review covers the branch when it read from the
// fork point the branch has on its base now, up to a commit the branch still
// holds. A branch with no fork point on origin's base can't be judged.
func TestStaleReview(t *testing.T) {
	g := gitRepo{t, stackRepo(t)}
	fork := g.git("rev-parse", "HEAD")
	g.git("checkout", "-q", "-b", "agent/t")
	reviewed := g.commit("own work", "own", "own work\n")
	g.commit("a nitpick fixed after the review", "own", "own work, tidied\n")
	g.git("checkout", "-q", "main")
	moved := g.commit("main moves on", "other", "other\n")
	dir := t.TempDir()
	for _, c := range []struct {
		name, base, fork, head string
		stale                  bool
	}{
		{"a review from the fork point, up to an earlier commit of the branch", "main", fork, reviewed, false},
		{"a review from another fork point", "main", moved, reviewed, true},
		{"a review up to a commit the branch no longer holds", "main", fork, moved, true},
		{"a base that isn't on origin", "gone", moved, reviewed, false},
	} {
		task := stackTask(t, dir, "001-t", "branch: agent/t", "base: "+c.base)
		review := "## Automated reviews\n### Automated review 1\n\n- Range: `" + c.fork + ".." + c.head + "`, 1 commits on `agent/t` since `" + c.base + "` (fork point `" + c.fork + "`)\n\n"
		text, err := os.ReadFile(task.Path)
		if err == nil {
			err = os.WriteFile(task.Path, []byte(strings.Replace(string(text), "## Log\n", review+"## Log\n", 1)), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := staleReview(task, g.repo); (got != "") != c.stale {
			t.Errorf("%s: staleReview = %q, want stale %v", c.name, got, c.stale)
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// gitRepo drives a stackRepo the way a worker does, committing as a test user.
type gitRepo struct {
	t    *testing.T
	repo string
}

func (g gitRepo) git(args ...string) string {
	g.t.Helper()
	return gitOut(g.t, g.repo, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
}

// commit writes the files named, each with its text, and commits them all.
func (g gitRepo) commit(msg string, files ...string) string {
	g.t.Helper()
	g.write(files...)
	g.git("commit", "-q", "--allow-empty", "-m", msg)
	return g.git("rev-parse", "HEAD")
}

// amend writes the files named, each with its text, into the last commit.
func (g gitRepo) amend(files ...string) string {
	g.t.Helper()
	g.write(files...)
	g.git("commit", "-q", "--amend", "--no-edit")
	return g.git("rev-parse", "HEAD")
}

func (g gitRepo) write(files ...string) {
	g.t.Helper()
	for i := 0; i < len(files); i += 2 {
		if err := os.WriteFile(filepath.Join(g.repo, files[i]), []byte(files[i+1]), 0o644); err != nil {
			g.t.Fatal(err)
		}
		g.git("add", files[i])
	}
}

// merge merges rev into a new branch from the commit at. It resolves a conflict
// with resolve, git arguments, then commits whatever is in the work tree.
func (g gitRepo) merge(name, at, rev string, resolve ...string) string {
	g.t.Helper()
	g.git("checkout", "-q", "-b", name, at)
	if err := exec.Command("git", "-C", g.repo, "-c", "user.name=t", "-c", "user.email=t@t", "merge", "-q", "--no-edit", rev).Run(); err != nil {
		if len(resolve) == 0 {
			g.t.Fatalf("merging %s into %s: %v", rev, name, err)
		}
		g.git(resolve...)
		g.git("add", "-A")
		g.git("commit", "-q", "--no-edit")
	}
	return g.git("rev-parse", "HEAD")
}

// Evidence is current when the branch's own work, replayed onto where the branch
// starts now, gives exactly the branch's tree: a restack or a merge of the base
// that git made on its own keeps it, and nothing else does.
func TestUnchangedSince(t *testing.T) {
	g := gitRepo{t, stackRepo(t)}
	start := g.commit("start", "shared", "one\ntwo\nthree\nfour\n", "gone", "base's\n")
	g.git("checkout", "-q", "-b", "agent/t")
	first := g.commit("own work", "own", "own work\n", "shared", "one\ntwo\nTHREE\nfour\n")
	g.git("rm", "-q", "gone")
	evidence := g.commit("delete gone")
	g.git("checkout", "-q", "main")
	baseFile := g.commit("main adds a file", "base", "from main\n")
	nearby := g.commit("main changes a line near the branch's", "shared", "ONE\ntwo\nthree\nfour\n")
	modified := g.commit("main changes the file the branch deletes", "gone", "main's\n")
	sameLine := g.commit("main changes the branch's line", "shared", "ONE\ntwo\nthree!\nfour\n")
	g.git("update-ref", "refs/remotes/origin/main", "HEAD")

	clean := g.merge("clean", evidence, baseFile)
	g.merge("edited", evidence, baseFile)
	edited := g.amend("own", "edited in the merge\n")
	resolved := g.merge("resolved", evidence, sameLine, "checkout", "--ours", "shared")
	keptBase := g.merge("kept-base", evidence, modified, "checkout", "--theirs", "gone")
	g.git("checkout", "-q", "-b", "restacked", evidence)
	g.git("rebase", "-q", nearby)
	restacked := g.git("rev-parse", "HEAD")
	g.git("checkout", "-q", "-b", "whitespace", evidence)
	whitespace := g.amend("own", "own  work\n")
	g.git("checkout", "-q", "-b", "more", evidence)
	more := g.commit("more own work", "own", "more\n")

	task := stackTask(t, t.TempDir(), "001-t", "base: main", "branch: agent/t", "base_sha: "+start)
	then, err := workIn(task, g.repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, tip string
		want      bool
	}{
		{"the commit the evidence ran on", evidence, true},
		{"a merge of the base git made on its own", clean, true},
		{"a restack onto a base that changed a nearby line", restacked, true},
		{"a merge edited before it was committed", edited, false},
		{"a merge whose conflict was resolved", resolved, false},
		{"a merge that kept the base's copy of a file the branch deletes", keptBase, false},
		{"an amend that only changes whitespace", whitespace, false},
		{"a commit added since", more, false},
		{"a commit dropped since", first, false},
	} {
		w, err := workIn(task, g.repo, c.tip)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, err := w.unchangedSince(g.repo, then); got != c.want || err != nil {
			t.Errorf("%s: unchangedSince = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := workIn(task, g.repo, "0123abc"); err == nil || !strings.Contains(err.Error(), "0123abc isn't a commit in") {
		t.Errorf("a commit that isn't there: workIn error = %v", err)
	}
}

// A parent rebased with changes of its own leaves the child's evidence on its
// old cut. noteBaseSHA keeps that cut, so the evidence still checks out once
// the child is restacked.
func TestEvidenceFromBeforeAParentWasRebased(t *testing.T) {
	g := gitRepo{t, stackRepo(t)}
	g.git("checkout", "-q", "-b", "agent/parent")
	oldParent := g.commit("parent's work", "parent", "first draft\n")
	g.git("update-ref", "refs/remotes/origin/agent/parent", "HEAD")
	g.git("checkout", "-q", "-b", "agent/child")
	evidence := g.commit("child's work", "child", "child\n")
	dir := t.TempDir()
	parent := stackTask(t, dir, "001-parent", "base: main", "branch: agent/parent")
	child := stackTask(t, dir, "002-child", "base: agent/parent", "branch: agent/child", "base_sha: "+oldParent)

	// Review changes the parent's work, and the parent is rebased onto a newer main.
	g.git("checkout", "-q", "main")
	g.commit("main moves", "base", "from main\n")
	g.git("update-ref", "refs/remotes/origin/main", "HEAD")
	g.git("checkout", "-q", "-b", "rebased-parent")
	newParent := g.commit("parent's work", "parent", "reviewed\n")
	g.git("update-ref", "refs/remotes/origin/agent/parent", "HEAD")
	g.git("checkout", "-q", "agent/child")
	g.git("rebase", "-q", "--onto", newParent, oldParent)

	if !noteBaseSHA(child, parent, g.repo) {
		t.Fatal("noteBaseSHA didn't move base_sha after the restack")
	}
	if got := child.Get("earlier_base_shas"); got != oldParent {
		t.Fatalf("earlier_base_shas = %q, want the cut it moved from, %s", got, oldParent)
	}
	then, err := workIn(child, g.repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if then.since != oldParent {
		t.Errorf("the evidence's work starts at %s, want the old cut %s", then.since, oldParent)
	}
	now, err := workIn(child, g.repo, "refs/heads/agent/child")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := now.unchangedSince(g.repo, then); !got || err != nil {
		t.Errorf("a restack onto a reworked parent: unchangedSince = %v, %v; want true", got, err)
	}
}
