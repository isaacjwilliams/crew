package main

// Background sessions: what `claude agents --json --all` says about them, and
// running claude itself.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Agent is one entry of `claude agents --json --all`.
type Agent struct {
	ID, State, Status, SessionID, Name string
	WaitingFor                         string // the prompt it's waiting on, if any
	PID                                bool   // whether it has a process: pid isn't null
	// TurnEnded: the session's transcript says its last turn has ended, though its
	// state may still be working (see busy).
	TurnEnded bool
}

type Agents []Agent

// claude is the claude CLI, run outside any parent Claude Code session's
// environment (so it can be launched from inside one), with this home's bin/
// first on PATH, so the sessions it starts run this home's crew.
func (c *Crew) claude(args ...string) *exec.Cmd {
	cmd := exec.Command("claude", args...)
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "PATH=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "PATH="+c.path("bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd
}

// agents is what `claude agents --json --all` lists, or nothing when it fails.
// Each field is read on its own, as a string or not at all, so a field whose
// type changes in a later Claude Code loses only that field, not every entry.
func (c *Crew) agents() Agents {
	out, err := c.claude("agents", "--json", "--all").Output()
	var entries []map[string]json.RawMessage
	if err != nil || json.Unmarshal(out, &entries) != nil {
		return Agents{}
	}
	text := func(e map[string]json.RawMessage, k string) string {
		var s string
		json.Unmarshal(e[k], &s)
		return s
	}
	as := make(Agents, 0, len(entries))
	for _, e := range entries {
		a := Agent{ID: text(e, "id"), State: text(e, "state"), Status: text(e, "status"), SessionID: text(e, "sessionId"),
			Name: text(e, "name"), WaitingFor: text(e, "waitingFor"), PID: len(e["pid"]) > 0 && string(e["pid"]) != "null"}
		if a.State == "working" && a.PID && a.SessionID != "" {
			a.TurnEnded = turnEnded(a.SessionID)
		}
		as = append(as, a)
	}
	return as
}

// turnEnded reads a session's transcript: Claude Code writes a turn_duration
// entry when a turn ends, and a prompt or a reply once the next one starts. It's
// false when there's no transcript to read.
func turnEnded(session string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", session+".jsonl"))
	if len(matches) == 0 {
		return false
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return false
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		var e struct{ Type, Subtype string }
		if json.Unmarshal([]byte(lines[i]), &e) != nil {
			return false
		}
		switch {
		case e.Subtype == "turn_duration":
			return true
		case e.Type == "user" || e.Type == "assistant":
			return false
		}
	}
	return false
}

func (as Agents) find(job string) *Agent {
	if job == "" {
		return nil
	}
	for i := range as {
		if as[i].ID == job {
			return &as[i]
		}
	}
	return nil
}

// busy: a turn is running, or the live process holds an open prompt, such as a
// permission dialog (status waiting). Anything else can take a followup, a
// blocked session too: it ended its turn on a question for the user, and the
// answer is what the followup sends. A background shell the session started,
// such as a dev server, keeps its state working after the turn ends, so the turn
// counts as running only until its transcript says it ended.
func (a Agent) busy() bool {
	return (a.State == "working" && !a.TurnEnded) || a.Status == "waiting"
}

// open: its process is running.
func (a Agent) open() bool { return a.PID }

// jobState says what a job is doing, for status lines: its state, and the
// prompt it's waiting on, if any. Nothing for a job that isn't listed.
func (as Agents) jobState(job string) string {
	a := as.find(job)
	switch {
	case a == nil:
		return ""
	case a.TurnEnded:
		return "idle, background task running"
	case a.WaitingFor != "":
		return a.State + " (" + a.WaitingFor + ")"
	}
	return a.State
}

// jobBusy: the job is listed, and busy.
func (as Agents) jobBusy(job string) bool {
	a := as.find(job)
	return a != nil && a.busy()
}

// jobListed: claude agents on this machine knows the job.
func (as Agents) jobListed(job string) bool { return as.find(job) != nil }

// runningWorkers counts the busy jobs the task files record as their worker. It
// doesn't go by name: a session Claude Code copies on resume gets a name of its own.
func (c *Crew) runningWorkers(as Agents) int {
	jobs := map[string]bool{}
	for _, t := range c.tasks("") {
		jobs[t.WorkerJob()] = true
	}
	n := 0
	for _, a := range as {
		if jobs[a.ID] && a.busy() {
			n++
		}
	}
	return n
}

// stopJob runs `claude stop` and waits up to 10 seconds for the job's process to
// close. Its conversation is saved, and background shells it started, such as a
// dev server, keep running for its next process. It reports whether the process
// closed.
func (c *Crew) stopJob(job string) bool {
	c.claude("stop", job).Run()
	for range 20 {
		if a := c.agents().find(job); a == nil || !a.open() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// stopIdleSession stops the open process of a session that isn't busy. A
// background session that ended its turn keeps its process open for about an
// hour, and resuming it then starts a copy under a new session ("already running
// in the background"), so it's stopped first, and the resume continues it in place.
func (c *Crew) stopIdleSession(as Agents, session string) {
	if session == "" {
		return
	}
	for _, a := range as {
		if a.SessionID == session && a.open() && !a.busy() {
			c.stopJob(a.ID)
		}
	}
}

// removeSession takes a session out of agent view, stopping it first: `claude rm`
// isn't documented to end a process that's still open. It fails only if the
// process won't close.
func (c *Crew) removeSession(job string) bool {
	a := c.agents().find(job)
	if a == nil {
		return true
	}
	if a.open() && !c.stopJob(job) {
		return false
	}
	if c.claude("rm", job).Run() != nil {
		warn("couldn't remove worker %s from agent view; delete it in claude agents", job)
	}
	return true
}

// A started (or resumed) background session.
type launch struct {
	job, session string
	mode         string // "fresh" or "resume"
}

// errNotStarted: claude started no session, so starting a fresh one instead is safe.
var errNotStarted = &exitError{1, ""}

var (
	ansiCode = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	jobID    = regexp.MustCompile(`^[[:alnum:]_-]+$`)
)

// launchSession starts a background session for a ledger file in wt, or resumes
// the one it records as <role>_session, and confirms it started. A fresh session
// gets the launch flags, the file's <role>_name and the ledger as an extra
// directory. A resume passes only the session and the prompt: a background
// session keeps the options it was started with (agent, permission mode,
// settings, --add-dir), and passing any launch flag again makes Claude Code start
// a copy under a new session instead of continuing this one.
func (c *Crew) launchSession(d Doc, role, wt, prompt string, resume bool, flags ...string) (launch, error) {
	session := d.Get(role + "_session")
	var cmd *exec.Cmd
	if resume {
		// So does a process that's still open.
		c.stopIdleSession(c.agents(), session)
		cmd = c.claude("--resume", session, "--bg", prompt)
	} else {
		// --add-dir takes more than one value, so it must never be the last flag
		// before the prompt.
		args := append([]string{"--name", d.Get(role + "_name"), "--add-dir", c.Home, "--bg"}, flags...)
		cmd = c.claude(append(args, prompt)...)
	}
	cmd.Dir = wt
	raw, _ := cmd.CombinedOutput()
	// The job ID can come wrapped in color codes, which would never match `claude agents`.
	out := ansiCode.ReplaceAllString(string(raw), "")
	job := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "backgrounded") {
			if f := strings.Fields(l); len(f) >= 3 {
				job = f[2]
			}
			break
		}
	}
	if job == "" {
		fmt.Fprintln(os.Stderr, strings.TrimRight(out, "\n"))
		return launch{}, errNotStarted
	}
	// A session did start, so don't fall back to starting another one.
	if !jobID.MatchString(job) {
		return launch{}, fail("claude started a session but crew can't read its job ID from this output; find it in 'claude agents' and record it with 'crew set':\n%s", strings.TrimRight(out, "\n"))
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.ToLower(l), "note:") {
			fmt.Fprintln(os.Stderr, l)
		}
	}
	started, ok := c.waitForLaunch(job)
	switch {
	case !ok:
		warn("claude started job %s, but it failed straight away (see 'claude agents')", job)
		return launch{}, errNotStarted
	case started == "":
		warn("job %s hasn't reported its session after 30s, so crew can't confirm it started or record its session; check 'claude agents', then record its session as %s_session in %s", job, role, d.Path)
	case resume && started != session:
		warn("claude started a copy of session %s as %s; later followups resume the copy", session, started)
	}
	mode := "fresh"
	if resume {
		mode = "resume"
	}
	return launch{job: job, session: started, mode: mode}, nil
}

// waitForLaunch waits for a job's own process to report the session it loaded,
// and returns it, or nothing after 30 seconds. A job's ID is the first 8
// characters of its session ID. A process that has just started can report a new
// session ID for a moment, before it loads the one it resumes, so only a session
// ID that starts with the job ID counts. A resume of a session that can't be
// found shows up as a job that ends in failed: then ok is false.
func (c *Crew) waitForLaunch(job string) (session string, ok bool) {
	for range 60 {
		if a := c.agents().find(job); a != nil {
			if a.State == "failed" {
				return "", false
			}
			if a.open() && strings.HasPrefix(a.SessionID, job) {
				return a.SessionID, true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", true
}

// A launcher starts or resumes the session of a ledger file.
type launcher func(d Doc, wt, prompt string, resume bool) (launch, error)

// relaunch resumes the session a file records as <role>_session, or starts a
// fresh one when it records none, when fresh is set, or when the resume fails. A
// fresh session gets freshPrompt and then prompt.
func (c *Crew) relaunch(start launcher, role string, d Doc, wt, prompt, freshPrompt string, fresh bool) (launch, error) {
	session := d.Get(role + "_session")
	if !fresh && session != "" {
		l, err := start(d, wt, prompt, true)
		if err != errNotStarted {
			return l, err
		}
		warn("couldn't resume session %s; starting a fresh session instead", session)
	}
	l, err := start(d, wt, freshPrompt+"\n"+prompt, false)
	if err == errNotStarted {
		return l, fail("could not start a session (see output above)")
	}
	return l, err
}

var autocompactWindow = regexp.MustCompile(`^(auto|[0-9]+[kKmM]?)$`)

// workerSettings is what workers run with: config/worker-settings.json, plus the
// project's projects/<p>/worker-settings.json when it has one (allow rules or env
// for that project's test commands, say). The project file can only add: objects
// are merged and arrays appended, but any value the shipped file sets wins, so a
// project can't drop a deny rule. It's a path, or the merged settings as JSON:
// `claude --settings` takes either. It also checks WORKER_AUTOCOMPACT_WINDOW, so
// a bad value stops a dispatch before its worktree exists.
func (c *Crew) workerSettings(p *Project) (string, error) {
	if w := p.Settings["WORKER_AUTOCOMPACT_WINDOW"]; !autocompactWindow.MatchString(w) {
		return "", fail("WORKER_AUTOCOMPACT_WINDOW must be auto or a token count like 400k, not '%s'", w)
	}
	base, extra := c.path("config/worker-settings.json"), filepath.Join(p.Dir, "worker-settings.json")
	if !isFile(extra) {
		return base, nil
	}
	var vals [2]any
	for i, f := range []string{base, extra} {
		b, err := os.ReadFile(f)
		if err == nil {
			vals[i], err = parseJSON(b)
		}
		if err != nil {
			return "", fail("%s isn't valid JSON", extra)
		}
	}
	return encodeJSON(addOnly(vals[0], vals[1]), false), nil
}

// launchWorker starts or resumes a task's worker in its worktree. It's the only
// place a worker's command line is built.
func (c *Crew) launchWorker(p *Project) launcher {
	return func(d Doc, wt, prompt string, resume bool) (launch, error) {
		settings := ""
		if !resume {
			var err error
			if settings, err = c.workerSettings(p); err != nil {
				return launch{}, err
			}
		}
		return c.launchSession(d, "worker", wt, prompt, resume, "--agent", "implementer",
			"--permission-mode", p.Settings["WORKER_PERMISSION_MODE"],
			"--autocompact", p.Settings["WORKER_AUTOCOMPACT_WINDOW"], "--settings", settings)
	}
}
