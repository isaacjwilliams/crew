package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A session's turn has ended when its transcript, found under
// CLAUDE_CONFIG_DIR, ends with a turn_duration entry; a prompt or a reply after
// it means the next turn is running.
func TestTurnEndedReadsTheSessionTranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	transcript := filepath.Join(dir, "projects", "-home-me-work", "s1.jsonl")
	os.MkdirAll(filepath.Dir(transcript), 0o755)
	ended := `{"type":"assistant"}` + "\n" + `{"type":"system","subtype":"turn_duration"}` + "\n\n"
	for _, c := range []struct {
		name, transcript string
		want             bool
	}{
		{"turn_duration last", ended, true},
		{"a prompt after it", ended + `{"type":"user"}` + "\n", false},
		{"a reply with no turn_duration", `{"type":"assistant"}` + "\n", false},
	} {
		os.WriteFile(transcript, []byte(c.transcript), 0o644)
		if got := turnEnded("s1"); got != c.want {
			t.Errorf("%s: turnEnded = %v; want %v", c.name, got, c.want)
		}
	}
	if turnEnded("no-such-session") {
		t.Error("turnEnded is true for a session with no transcript")
	}
}
