package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// claudeListing puts a claude on PATH whose `claude agents` lists jobs (JSON
// entries), and writes each session's transcript under CLAUDE_CONFIG_DIR.
func claudeListing(t *testing.T, jobs string, transcripts map[string]string) {
	t.Helper()
	bin, config := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho '["+jobs+"]'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-home-me-work")
	os.MkdirAll(dir, 0o755)
	for session, content := range transcripts {
		os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte(content), 0o644)
	}
}

// claude agents can list a job as working after its turn has ended: a shell it
// started keeps it working, and the state can stay working once its process has
// exited.
func TestAWorkingJobWhoseTurnEnded(t *testing.T) {
	ended := `{"type":"system","subtype":"turn_duration"}` + "\n"
	claudeListing(t, `{"id":"live","state":"working","pid":4242,"status":"busy","sessionId":"live-s"},
{"id":"gone","state":"working","pid":null,"sessionId":"gone-s"}`, map[string]string{"live-s": ended, "gone-s": ended})
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

// bashCall is a Bash call and its result as a transcript holds them: one run in
// the background (more is `"run_in_background":true,`) or moved there at its
// timeout, with the background task ID id.
func bashCall(id, input, more string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_%s","name":"Bash","input":{%s%s}}]}}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_%s","type":"tool_result","content":"Command running in background with ID: %s."}]},"toolUseResult":{"stdout":"","backgroundTaskId":%q}}
`, id, more, input, id, id, id)
}

// background is a Bash call run in the background, described.
func background(id, description string) string {
	return bashCall(id, fmt.Sprintf(`"command":"bin/%s","description":%q`, id, description), `"run_in_background":true,`)
}

// notice is the task notification Claude Code queues when background task id
// finishes with status.
func notice(id, status string) string {
	return queued(fmt.Sprintf(`<task-id>%s</task-id>\n<status>%s</status>`, id, status))
}

// queued is a task notification with body, as Claude Code queues it.
func queued(body string) string {
	return `{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\n` + body + `\n</task-notification>"}` + "\n"
}

// taskStop is a TaskStop call that stopped background task id, and its result.
func taskStop(id string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_stop_%s","name":"TaskStop","input":{"task_id":%q}}]}}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_stop_%s","type":"tool_result","content":"stopped"}]},"toolUseResult":{"message":"Successfully stopped task: %s","task_id":%q,"task_type":"local_bash"}}
`, id, id, id, id, id)
}

const turnEnd = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Waiting."}]}}
{"type":"system","subtype":"turn_duration"}
`

// A transcript names each background command still running, by its
// description, or its command without one; a finished or stopped one isn't
// named. When a line doesn't read, a start can't be named, or a notice has no
// task ID or gives one of its commands a status crew doesn't know, it names none.
func TestBackgroundCommandsNamesTheOnesStillRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	server, checks := background("b1", "bin/rails server"), background("b2", "Run the clone checks")
	for _, c := range []struct {
		name, transcript string
		want             []string
	}{
		{"one running", server + turnEnd, []string{"bin/rails server"}},
		{"two running, in the order they started", server + checks + turnEnd, []string{"bin/rails server", "Run the clone checks"}},
		{"one started and finished", checks + turnEnd + notice("b2", "completed") + turnEnd, nil},
		{"one of two finished", server + checks + notice("b2", "failed") + turnEnd, []string{"bin/rails server"}},
		{"one killed", checks + notice("b2", "killed") + turnEnd, nil},
		{"one that finished before its start was written", server + notice("b2", "failed") + checks + turnEnd, []string{"bin/rails server"}},
		{"one stopped with TaskStop, which sends no notice", server + taskStop("b1") + turnEnd, nil},
		{"one moved to the background at its timeout", bashCall("b3", `"command":"bin/rake db:restore","description":"Restore the backup"`, "") + turnEnd, []string{"Restore the backup"}},
		{"one with no description", bashCall("b4", `"command":"npm run dev"`, `"run_in_background":true,`) + turnEnd, []string{"npm run dev"}},
		{"a subagent's notice and a monitor's event", checks + notice("a17", "completed") +
			queued(`<task-id>m1</task-id>\n<event>ready</event>`) + turnEnd, []string{"Run the clone checks"}},
		{"nothing in the background", turnEnd, nil},
		{"a subagent's own background command", strings.ReplaceAll(server, `{"type":`, `{"isSidechain":true,"type":`) + turnEnd, nil},
		{"a line that doesn't read", server + `{"type":"assistant","mess` + "\n" + turnEnd, nil},
		{"a start whose call isn't in the transcript", checks + strings.SplitAfter(server, "\n")[1] + turnEnd, nil},
		{"a start with neither a description nor a command", checks + bashCall("b5", `"timeout":600000`, `"run_in_background":true,`) + turnEnd, nil},
		{"a notice with no task ID", server + queued(`<status>completed</status>`) + turnEnd, nil},
		{"a notice of its own with a status crew doesn't know", server + checks + notice("b1", "paused") + turnEnd, nil},
		{"a notice of its own, before its start, with a status crew doesn't know", notice("b1", "paused") + server + checks + turnEnd, nil},
		{"a notice of its own with no status", server + checks + queued(`<task-id>b1</task-id>`) + turnEnd, nil},
	} {
		os.WriteFile(path, []byte(c.transcript), 0o644)
		if got := backgroundCommands(path); !slices.Equal(got, c.want) {
			t.Errorf("%s: backgroundCommands = %q; want %q", c.name, got, c.want)
		}
	}
	if got := backgroundCommands(filepath.Join(dir, "none.jsonl")); got != nil {
		t.Errorf("with no transcript: backgroundCommands = %q; want none", got)
	}
}

// A job whose turn ended while a background command it started still runs
// shows as idle with what's running, on a task's row or a goal's, and isn't
// busy: it isn't counted against MAX_WORKERS. One that can't be read so
// shows as it did before: with no process, listed as anything but working, or
// with no transcript.
func TestAJobWaitingOnItsBackgroundCommand(t *testing.T) {
	waiting := background("b1", "bin/rails server") + turnEnd
	claudeListing(t, `{"id":"waits","state":"working","pid":4242,"status":"busy","sessionId":"waits-s"},
{"id":"two","state":"working","pid":4242,"status":"busy","sessionId":"two-s"},
{"id":"gone","state":"working","pid":null,"sessionId":"gone-s"},
{"id":"done","state":"done","pid":4242,"status":"idle","sessionId":"done-s"},
{"id":"asks","state":"blocked","pid":4242,"status":"busy","sessionId":"asks-s"},
{"id":"mid","state":"working","pid":4242,"status":"busy","sessionId":"mid-s"},
{"id":"shell","state":"working","pid":4242,"status":"busy","sessionId":"shell-s"},
{"id":"none","state":"working","pid":4242,"status":"busy","sessionId":"none-s"}`, map[string]string{
		"waits-s": waiting, "two-s": background("b1", "bin/rails server") + background("b2", "npm run dev") + turnEnd,
		"gone-s": waiting, "done-s": waiting, "asks-s": waiting,
		"mid-s":   waiting + `{"type":"user","message":{"role":"user","content":"New input"}}` + "\n",
		"shell-s": `{"type":"system","subtype":"turn_duration"}` + "\n",
	})
	c := &Crew{Home: t.TempDir()}
	for _, job := range []string{"waits", "mid"} {
		writeFile(t, c, "", "projects", "p", "project.env")
		reportTask(t, c, "p", "001-"+job, false, "status: working", "worker_job: "+job)
	}
	as := c.agents()
	if n := c.runningWorkers(as); n != 1 {
		t.Errorf("runningWorkers = %d; want 1, the worker mid-turn and not the one waiting", n)
	}
	for job, want := range map[string]string{
		"waits": "idle (bin/rails server)", "two": "idle (bin/rails server, npm run dev)",
		"gone": "idle", "done": "done", "asks": "blocked", "mid": "working", "shell": "idle, background task running", "none": "working",
	} {
		if got := as.jobState(job); got != want {
			t.Errorf("%s: state %q; want %q", job, got, want)
		}
	}
	if as.jobBusy("waits") || !as.jobBusy("mid") || !as.jobBusy("none") {
		t.Errorf("busy: waiting %v, mid-turn %v, with no transcript %v; want false, true, true", as.jobBusy("waits"), as.jobBusy("mid"), as.jobBusy("none"))
	}
	if got := goalFields(goalReport{Goal: Goal{docWith(t, "---\nstatus: running\n---\n")}, Lead: *as.find("waits")})[workerField].text; got != "idle (bin/rails server)" {
		t.Errorf("a goal lead waiting on its background command shows as %q", got)
	}
	for _, job := range []string{"gone", "done", "asks", "shell"} {
		if b := as.find(job).Background; b != nil {
			t.Errorf("%s: Background = %q; want none", job, b)
		}
	}
}
