package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A Codex that starts a thread and then goes quiet is stopped once the stall
// limit passes, and its thread is still read from what it wrote. The other
// behaviour of crew codex is tested in tests/crew-e2e.sh, with tests/stubs/codex.
func TestRunCodexStopsAStalledCodex(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho '{\"type\":\"thread.started\",\"thread_id\":\"0199a3b2-1c2d-7e3f-8a9b-0c1d2e3f4a5b\"}'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prompt := filepath.Join(dir, "prompt.md")
	os.WriteFile(prompt, []byte("Review this.\n"), 0o644)
	events := filepath.Join(dir, "out", "round-0.events.jsonl")

	defer func(limit time.Duration) { codexStallLimit = limit }(codexStallLimit)
	codexStallLimit = 500 * time.Millisecond
	began := time.Now()
	status, stalled, err := runCodex([]string{"exec", "-"}, dir, prompt, events)
	if err != nil || !stalled || status == 0 {
		t.Fatalf("got status %d, stalled %v, err %v; want a stopped, stalled run", status, stalled, err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("took %s to stop a Codex that went quiet; want about the stall limit", took)
	}
	if got := startedThread(events); got != "0199a3b2-1c2d-7e3f-8a9b-0c1d2e3f4a5b" {
		t.Errorf("startedThread = %q; want the thread from the events Codex wrote before it stalled", got)
	}
}
