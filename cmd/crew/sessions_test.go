package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
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

// reply is a transcript entry of an assistant reply written at at, whose
// request sent tokens of context: a few as input and cache write, the rest
// read from cache. Its message ID is the time.
func reply(at time.Time, tokens int, more string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,%s"message":{"id":"msg_%d","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":1000,"cache_read_input_tokens":%d,"output_tokens":500}}}`,
		at.Format(time.RFC3339Nano), more, at.Unix(), tokens-1002) + "\n"
}

// A session whose last request was over an hour ago, with a context of 100k
// tokens or more, costs more to resume than to start fresh. Below either
// threshold, or when its transcript can't say, it resumes.
func TestColdResumeTakesTheLastRequestPastAnHourWithABigContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join("projects", "-home-me-work", "s1.jsonl")
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		idle   time.Duration
		tokens int
		want   string
	}{
		{"cold, with a big context", 9 * time.Hour, 143_201, "idle 9h, with 143k tokens of context"},
		{"just past the hour, at 100k", cacheLife + time.Second, coldContext, "idle 1h, with 100k tokens of context"},
		{"cold, with a small context", 9 * time.Hour, coldContext - 1, ""},
		{"an hour to the second, with a big context", cacheLife, 143_201, ""},
		{"recent, with a big context", 5 * time.Minute, 143_201, ""},
	} {
		writeTranscript(t, dir, path, `{"type":"user"}`+"\n", reply(at, c.tokens, ""), `{"type":"user"}`+"\n")
		if got := coldResume("s1", at.Add(c.idle)); got != c.want {
			t.Errorf("%s: coldResume = %q; want %q", c.name, got, c.want)
		}
	}
	if got := coldResume("no-such-session", at.Add(9*time.Hour)); got != "" {
		t.Errorf("a session with no transcript: coldResume = %q; want it to resume", got)
	}
	writeTranscript(t, dir, path, `{"type":"user"}`+"\n", `{"type":"system","subtype":"turn_duration"}`+"\n")
	if got := coldResume("s1", at.Add(9*time.Hour)); got != "" {
		t.Errorf("a transcript with no request: coldResume = %q; want it to resume", got)
	}
}

// The last request is the session's own last reply, by its timestamp, with
// its fullest usage. Entries written after it, a copy of it with its counts
// zeroed, the session's subagents' requests and Claude Code's own messages
// don't move it.
func TestLastRequestIsTheSessionsOwnLastReply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	later := at.Add(3 * time.Hour)
	own := reply(at.Add(-time.Second), 120_000, "") + reply(at, 143_201, "")
	for _, c := range []struct{ name, transcript string }{
		{"its last reply ends the transcript", own},
		{"entries written after it", own + `{"type":"user"}` + "\n" + fmt.Sprintf(`{"type":"last-prompt","timestamp":%q}`, later.Format(time.RFC3339)) + "\n\n"},
		{"a subagent's reply after it", own + reply(later, 20_000, `"isSidechain":true,`)},
		{"an API error after it", own + fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`, later.Format(time.RFC3339)) + "\n"},
		{"a copy of it with its counts zeroed", own + fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":"msg_%d","model":"claude-opus-5-5","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`, later.Format(time.RFC3339), at.Unix()) + "\n"},
	} {
		writeTranscript(t, dir, "s1.jsonl", c.transcript)
		gotAt, tokens, ok := lastRequest(path)
		if !ok || !gotAt.Equal(at) || tokens != 143_201 {
			t.Errorf("%s: lastRequest = %v, %d, %v; want %v, 143201, true", c.name, gotAt, tokens, ok, at)
		}
	}
	writeTranscript(t, dir, "s1.jsonl", `{"type":"user"}`+"\n")
	if _, _, ok := lastRequest(path); ok {
		t.Error("lastRequest found a request in a transcript without one")
	}
	// A reply cut off as it was written may be a later request than the one
	// before it. Claude Code writes its type after its message.
	for _, cut := range []string{
		`{"parentUuid":"p1","isSidechain":false,"message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":2,`,
		`{"type":"assistant","timestamp":`,
		"{not json",
	} {
		writeTranscript(t, dir, "s1.jsonl", own+cut+"\n")
		if _, _, ok := lastRequest(path); ok {
			t.Errorf("lastRequest gave the last reply it could read, though a line after it doesn't read: %s", cut)
		}
	}
}
