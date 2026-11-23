package main

import (
	"os"
	"path/filepath"
	"slices"
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

// claude agents can list a job as working after its turn has ended: a shell it
// started keeps it working, and the state can stay working once its process has
// exited.
func TestAWorkingJobWhoseTurnEnded(t *testing.T) {
	bin, config := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte(`#!/bin/sh
echo '[{"id":"live","state":"working","pid":4242,"status":"busy","sessionId":"live-s"},
{"id":"gone","state":"working","pid":null,"sessionId":"gone-s"}]'
`), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-home-me-work")
	os.MkdirAll(dir, 0o755)
	for _, s := range []string{"live-s", "gone-s"} {
		os.WriteFile(filepath.Join(dir, s+".jsonl"), []byte(`{"type":"system","subtype":"turn_duration"}`+"\n"), 0o644)
	}
	as := (&Crew{Home: t.TempDir()}).agents()
	if as.jobBusy("live") || as.jobState("live") != "idle, background task running" {
		t.Errorf("with its process running: busy %v, state %q", as.jobBusy("live"), as.jobState("live"))
	}
	if as.jobBusy("gone") || as.jobState("gone") != "idle" {
		t.Errorf("with its process gone: busy %v, state %q", as.jobBusy("gone"), as.jobState("gone"))
	}
}

func TestAWorkerWithoutAModelOrEffortLaunchesAsBefore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &Crew{Home: t.TempDir()}
	if err := os.MkdirAll(c.path("config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path("config/worker-settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Project{Name: "p", Dir: t.TempDir(), Settings: Settings{"WORKER_PERMISSION_MODE": "auto", "WORKER_AUTOCOMPACT_WINDOW": "400k"}}
	unset, err := c.launchWorker(p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sonnet, err := c.launchWorker(p, "sonnet", "medium")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(unset.flags, "--model") || unset.model != "" {
		t.Errorf("a worker without a model got one: %q, model %q", unset.flags, unset.model)
	}
	if want := c.fingerprint(unset.flags, c.agentFiles("implementer")...); unset.fingerprint != want {
		t.Errorf("fingerprint = %s, want %s: only the flags and agent files count", unset.fingerprint, want)
	}
	// The model and effort are recorded and compared on their own, so they change neither.
	if !slices.Equal(sonnet.flags, unset.flags) || sonnet.fingerprint != unset.fingerprint || sonnet.model != "sonnet" || sonnet.effort != "medium" {
		t.Errorf("a model or effort changed the flags or fingerprint: %q %s, want %q %s", sonnet.flags, sonnet.fingerprint, unset.flags, unset.fingerprint)
	}
}
