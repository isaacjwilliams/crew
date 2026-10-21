package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func hookInput(cwd, path string) []byte {
	b, _ := json.Marshal(map[string]any{"cwd": cwd, "tool_input": map[string]string{"file_path": path}})
	return b
}

func TestGuard(t *testing.T) {
	sb := t.TempDir()
	home := filepath.Join(sb, "crew")
	main, wt := filepath.Join(sb, "main"), filepath.Join(sb, "wt")
	scratch := filepath.Join(sb, "tmp")
	for _, d := range []string{home + "/projects", home + "/bin", main, scratch, sb + "/elsewhere"} {
		os.MkdirAll(d, 0o755)
	}
	run(t, main, "git", "init", "-q")
	run(t, main, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	run(t, main, "git", "worktree", "add", "-q", "-b", "agent/x", wt)
	os.Symlink(filepath.Join(home, "bin"), filepath.Join(wt, "into-home"))
	os.Symlink(filepath.Join(home, "projects"), filepath.Join(sb, "elsewhere", "ledger"))
	os.Symlink("loop", filepath.Join(wt, "loop"))

	cases := []struct {
		allow      bool
		role, cwd  string
		path       string
		projectDir string
	}{
		{true, "worker", wt, wt + "/src/a.ts", ""},
		{true, "worker", wt, "src/rel.ts", ""},
		{false, "worker", wt, main + "/src/a.ts", ""},
		{false, "worker", wt, wt + "/../main/x", ""},
		{true, "worker", wt, home + "/projects/p/tasks/001-x.md", ""},
		{true, "worker", home, wt + "/src/a.ts", wt},
		{false, "worker", main, main + "/a", ""},
		{false, "worker", sb, sb + "/elsewhere/a", ""},
		{false, "lead", main, main + "/a", ""},
		{true, "lead", main, home + "/projects/p/tasks/001-x.md", ""},
		{true, "lead", main, home + "/reports/x.md", ""},
		{false, "lead", main, home + "/bin/crew", ""},
		{false, "lead", main, home, ""},
		{false, "gardener", wt, wt + "/a", ""},
		{true, "gardener", wt, home + "/projects/p/proposals/x.md", ""},
		{true, "lead", main, scratch + "/notes.md", ""},
		// Judged by where a symlink leads, both ways.
		{false, "worker", wt, wt + "/into-home/crew", ""},
		{true, "lead", main, sb + "/elsewhere/ledger/p/tasks/1.md", ""},
		// A path that can't be worked out is blocked.
		{false, "worker", wt, wt + "/loop/a", ""},
	}
	for _, c := range cases {
		g := guard{role: c.role, home: home, scratch: []string{scratch}, projectDir: c.projectDir}
		err := g.check(hookInput(c.cwd, c.path))
		if (err == nil) != c.allow {
			t.Errorf("%s in %s editing %s: allowed=%v, want %v (%v)", c.role, c.cwd, c.path, err == nil, c.allow, err)
		}
	}

	g := guard{role: "lead", home: home}
	if g.check([]byte("{not json")) == nil {
		t.Error("malformed hook input was allowed")
	}
	if err := g.check([]byte(`{"cwd": "/", "tool_input": {}}`)); err != nil {
		t.Errorf("a tool call without a path was blocked: %v", err)
	}
}

func TestCanonical(t *testing.T) {
	sb := t.TempDir()
	sb, _ = filepath.EvalSymlinks(sb)
	os.MkdirAll(sb+"/a/b", 0o755)
	os.Symlink(sb+"/a/b", sb+"/link")
	os.Symlink("b", sb+"/a/rel")
	for in, want := range map[string]string{
		sb + "/a/b/c":             sb + "/a/b/c",
		sb + "/link/c":            sb + "/a/b/c",
		sb + "/link/../x":         sb + "/a/x",
		sb + "/a/rel/new/file":    sb + "/a/b/new/file",
		sb + "/missing/../a/rel":  sb + "/a/b",
		sb + "//a/./b/":           sb + "/a/b",
		sb + "/missing/deeper/..": sb + "/missing",
	} {
		if got, err := canonical(in); err != nil || got != want {
			t.Errorf("canonical(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
}
