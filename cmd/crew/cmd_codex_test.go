package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Codex that starts a thread and then goes quiet is stopped once the stall
// limit passes: crew codex exits 124 and still prints the thread last. The other
// behaviour of crew codex is tested in tests/crew-e2e.sh, with tests/stubs/codex.
func TestCodexStopsAStalledCodex(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho '{\"type\":\"thread.started\",\"thread_id\":\"0199a3b2-1c2d-7e3f-8a9b-0c1d2e3f4a5b\"}'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CREW_PROJECT", allProjects)
	prompt := filepath.Join(dir, "prompt.md")
	os.WriteFile(prompt, []byte("Review this.\n"), 0o644)
	c := &Crew{Home: dir, Settings: Settings{"CODEX_MODEL": "gpt-test", "CODEX_MODEL_EFFORT": "high"}}

	defer func(limit time.Duration) { codexStallLimit = limit }(codexStallLimit)
	codexStallLimit = 500 * time.Millisecond
	began := time.Now()
	var err error
	out := stdoutOf(t, func() { err = cmdCodex(c, []string{"start", dir, prompt, filepath.Join(dir, "out", "round-0")}) })
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("took %s to stop a Codex that went quiet; want about the stall limit", took)
	}
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != exitCodexStalled {
		t.Errorf("crew codex returned %v; want exit %d for a stall", err, exitCodexStalled)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := lines[len(lines)-1]; last != "thread: 0199a3b2-1c2d-7e3f-8a9b-0c1d2e3f4a5b" {
		t.Errorf("last line %q; want the thread Codex started before it stalled", last)
	}
}

// stdoutOf is what f prints to stdout.
func stdoutOf(t *testing.T, f func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = stdout
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}
