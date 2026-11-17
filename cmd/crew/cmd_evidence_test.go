package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Under its lock, crew evidence pushes right after it fetches, so only a
// publisher crew doesn't know of moves origin in between: this test is that one.
func TestPushEvidenceRebasesOntoOriginWhenItMoved(t *testing.T) {
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	dir := t.TempDir()
	origin, other, clone := filepath.Join(dir, "evidence.git"), filepath.Join(dir, "other"), filepath.Join(dir, "_evidence")
	runIn(t, dir, "git", "init", "-q", "--bare", "-b", "main", origin)
	runIn(t, dir, "git", "clone", "-q", origin, other)
	commitFile(t, other, "README.md")
	runIn(t, other, "git", "push", "-q", "origin", "main")
	runIn(t, dir, "git", "clone", "-q", origin, clone)

	commitFile(t, other, "002-b/b.png")
	runIn(t, other, "git", "push", "-q", "origin", "main")
	commitFile(t, clone, "001-a/a.png")

	if err := pushEvidence(clone, "origin/main", "main"); err != nil {
		t.Fatalf("pushEvidence: %v", err)
	}
	if head, main := gitOut(t, clone, "rev-parse", "HEAD"), gitOut(t, origin, "rev-parse", "main"); head != main {
		t.Errorf("origin's main is %s, want the clone's HEAD %s", main, head)
	}
	if got := gitOut(t, origin, "log", "--format=%s", "main"); got != "001-a/a.png\n002-b/b.png\nREADME.md" {
		t.Errorf("origin's main has commits\n%s\nwant the clone's on top of the other publisher's", got)
	}
}

// commitFile adds a file at path in repo, in a commit named for it.
func commitFile(t *testing.T, repo, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0o755)
	if err := os.WriteFile(filepath.Join(repo, path), []byte(path), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, repo, "git", "add", "--", path)
	runIn(t, repo, "git", "commit", "-qm", path)
}
