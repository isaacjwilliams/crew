package main

// Background sessions: what `claude agents --json --all` says about them, and
// running claude itself.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
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
// first on PATH and without the caller's CREW_PROJECT or CREW_LEAD_FINGERPRINT.
// A session's own project goes in its settings instead (sessionSettings), and
// only crew lead sets a fingerprint.
func (c *Crew) claude(args ...string) *exec.Cmd {
	cmd := exec.Command("claude", args...)
	env := environWithout("CLAUDECODE", "PATH", "CREW_PROJECT", "CREW_LEAD_FINGERPRINT")
	cmd.Env = append(env, "PATH="+c.path("bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd
}

// execClaude replaces crew with cmd, a claude command, so the terminal and its
// signals go straight to claude. claude keeps crew's current directory; a caller
// that needs another changes into it first. It returns only when the exec fails.
func execClaude(cmd *exec.Cmd) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return fail("'claude' is required but not installed")
	}
	return syscall.Exec(path, cmd.Args, cmd.Env)
}

// environWithout is crew's own environment (os.Environ) without the named
// variables.
func environWithout(names ...string) []string {
	env := []string{}
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if !slices.Contains(names, name) {
			env = append(env, e)
		}
	}
	return env
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
		warn("couldn't remove job %s from agent view; delete it in claude agents", job)
	}
	return true
}

// A launcher starts or resumes the session a ledger file records: a task's
// worker, or a goal's goal lead. launchWorker and launchGoalLead build one, and
// are the only places a worker's or goal lead's launch flags are chosen;
// launchSession builds the command line around them.
type launcher struct {
	p      *Project
	prefix string   // of the file's keys for the session: worker_name, lead_session, …
	flags  []string // a fresh session's launch flags, but for its name and MCP config
	// fingerprint is what a fresh session loads now: its flags, which carry its
	// settings as a JSON string, and its agent files.
	fingerprint string
}

// newLauncher is the launcher of agent's sessions for project p, whose keys
// start with prefix, with these launch flags besides the agent.
func (c *Crew) newLauncher(p *Project, prefix, agent string, flags ...string) launcher {
	flags = append([]string{"--agent", agent}, flags...)
	return launcher{p: p, prefix: prefix, flags: flags, fingerprint: c.fingerprint(flags, c.agentFiles(agent)...)}
}

// sessionRoles are the agents that run as sessions of their own. Every other
// agent is a subagent, which a session may run. The lead's settings deny each of
// them as a subagent, and a test checks the two lists agree.
var sessionRoles = []string{"lead", "goal-lead", "implementer", "gardener"}

// agentFiles are the files a session of agent loads its instructions from: the
// installed copies (installedAgents) of its own agent file and of every
// subagent's that the home ships. Which subagents it runs is said only in its
// instructions, so they all count. The installed copies are what Claude Code
// loads, so an agent the home has but crew install hasn't copied yet doesn't
// count until it has.
func (c *Crew) agentFiles(agent string) []string {
	files := []string{filepath.Join(installedAgents(), agent+".md")}
	shipped, _ := filepath.Glob(c.path("agents", "*.md"))
	for _, f := range shipped {
		if name := strings.TrimSuffix(filepath.Base(f), ".md"); !slices.Contains(sessionRoles, name) {
			files = append(files, filepath.Join(installedAgents(), name+".md"))
		}
	}
	return files
}

// fingerprint is a short hash of what a session loads when it starts: args, its
// launch flags, and files, each by its path and what it holds. Skills don't
// count: a session loads them from the home each time it uses one. A missing
// file counts as missing, so a home without it still works.
func (c *Crew) fingerprint(args []string, files ...string) string {
	h := sha256.New()
	// Each part goes in with its length, so no two lists of parts hash alike.
	add := func(s string) { fmt.Fprintf(h, "%d:%s", len(s), s) }
	for _, a := range args {
		add(a)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			add(f)
			add(string(b))
		} else {
			add(f + " is missing")
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// A started (or resumed) background session.
type launch struct {
	job, session string
	mode         string   // "resume", "fresh", or "fresh: <why>" when crew chose it
	by           launcher // what started it
}

// record notes the session in d, the ledger file it was launched for: its job
// and session, and for a fresh session, the fingerprint of what it loaded. It's
// the only place a session's keys are set.
func (l launch) record(d Doc) {
	d.Set(l.by.prefix+"_job", l.job)
	d.Set(l.by.prefix+"_session", l.session)
	if l.mode != "resume" {
		d.Set(l.by.prefix+"_fingerprint", l.by.fingerprint)
	}
}

// errNotStarted: claude started no session, so starting a fresh one instead is safe.
var errNotStarted = &exitError{1, ""}

var (
	ansiCode = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	jobID    = regexp.MustCompile(`^[[:alnum:]_-]+$`)
)

// launchSession starts a background session for a ledger file in wt, or resumes
// the one it records as <prefix>_session, and confirms it started. A fresh
// session gets the launcher's flags, the file's <prefix>_name and the ledger as
// an extra directory. A resume passes only the session and the prompt: a
// background session keeps the options it was started with (agent, permission
// mode, settings, --add-dir), and passing any launch flag again makes Claude Code
// start a copy under a new session instead of continuing this one. A fresh
// session takes the one it replaces out of agent view once it has started. Both
// refresh the project's MCP config first (refreshMCPConfig).
func (c *Crew) launchSession(start launcher, d Doc, wt, prompt string, resume bool) (launch, error) {
	mcp, err := c.refreshMCPConfig(start.p)
	if err != nil {
		return launch{}, err
	}
	session := d.Get(start.prefix + "_session")
	var cmd *exec.Cmd
	if resume {
		// A process that's still open would also make Claude Code start a copy.
		c.stopIdleSession(c.agents(), session)
		cmd = c.claude("--resume", session, "--bg", prompt)
	} else {
		// --add-dir takes more than one value, so it must never be the last flag
		// before the prompt. The MCP flags go first: see refreshMCPConfig.
		cmd = c.claude(slices.Concat([]string{"--name", d.Get(start.prefix + "_name"), "--add-dir", c.Home, "--bg"}, mcp, start.flags, []string{prompt})...)
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
		warn("job %s hasn't reported its session after 30s, so crew can't confirm it started or record its session; check 'claude agents', then record its session as %s_session in %s", job, start.prefix, d.Path)
	case resume && started != session:
		warn("claude started a copy of session %s as %s; later followups resume the copy", session, started)
	}
	// A fresh session replaces the one the file records, so take that one out of
	// agent view, unless someone is using it.
	if replaced := d.Get(start.prefix + "_job"); !resume && replaced != "" && !c.agents().jobBusy(replaced) {
		c.removeSession(replaced)
	}
	l := launch{job: job, session: started, mode: "fresh", by: start}
	if resume {
		l.mode = "resume"
		// A resume that never reported its session continues the recorded one.
		if started == "" {
			l.session = session
		}
	}
	return l, nil
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

// relaunch resumes the session a file records as <prefix>_session, or starts a
// fresh one when it records none, when fresh is set, when the resume fails, or
// when the session would run on old instructions or settings: its recorded
// <prefix>_fingerprint isn't the launcher's, or it has none. A fresh session gets
// freshPrompt and then prompt.
func (c *Crew) relaunch(start launcher, d Doc, wt, prompt, freshPrompt string, fresh bool) (launch, error) {
	session := d.Get(start.prefix + "_session")
	resume, why := !fresh && session != "", ""
	if resume && d.Get(start.prefix+"_fingerprint") != start.fingerprint {
		resume, why = false, "its instructions or settings changed"
	}
	if resume {
		l, err := c.launchSession(start, d, wt, prompt, true)
		if err != errNotStarted {
			return l, err
		}
		warn("couldn't resume session %s; starting a fresh session instead", session)
	}
	l, err := c.launchSession(start, d, wt, freshPrompt+"\n"+prompt, false)
	if err == errNotStarted {
		return l, fail("could not start a session (see output above)")
	}
	if why != "" {
		l.mode = "fresh: " + why
	}
	return l, err
}

var autocompactWindow = regexp.MustCompile(`^(auto|[0-9]+[kKmM]?)$`)

// readSettings is the settings file f, which must hold a JSON object.
func readSettings(f string) (*object, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(b)
	o, ok := v.(*object)
	if err != nil || !ok {
		return nil, fail("%s isn't a JSON object of settings", f)
	}
	return o, nil
}

// sessionSettings is the --settings value of a session crew starts for project
// p: settings, with CREW_PROJECT and CREW_HOME set in its env, so the session's
// crew commands and hooks work on p in this home. It changes the settings it's
// given. Every session crew starts for a project gets them this way, and never
// through the environment crew runs claude with: a background session gets the
// environment of Claude Code's daemon, which serves every project at once.
// CREW_LEAD_FINGERPRINT is set empty for the same reason: a daemon a lead's own
// shell started would hand every session the lead's (see crew lead --hook).
func (c *Crew) sessionSettings(settings *object, p *Project) string {
	env := settings.child("env")
	env.set("CREW_PROJECT", p.Name)
	env.set("CREW_HOME", c.Home)
	env.set("CREW_LEAD_FINGERPRINT", "")
	return encodeJSON(settings, false)
}

// workerSettings is the --settings value workers run with:
// config/worker-settings.json, plus the project's projects/<p>/worker-settings.json
// when it has one (allow rules or env for that project's test commands, say), as
// sessionSettings. The project file can only add: objects are merged and arrays
// appended, but any value the shipped file sets wins, so a project can't drop a
// deny rule. It also checks WORKER_AUTOCOMPACT_WINDOW, so a bad value stops a
// dispatch before its worktree exists.
func (c *Crew) workerSettings(p *Project) (string, error) {
	if w := p.Settings["WORKER_AUTOCOMPACT_WINDOW"]; !autocompactWindow.MatchString(w) {
		return "", fail("WORKER_AUTOCOMPACT_WINDOW must be auto or a token count like 400k, not '%s'", w)
	}
	settings, err := readSettings(c.path("config/worker-settings.json"))
	if err != nil {
		return "", err
	}
	if extra := filepath.Join(p.Dir, "worker-settings.json"); isFile(extra) {
		more, err := readSettings(extra)
		if err != nil {
			return "", err
		}
		settings = addOnly(settings, more).(*object)
	}
	return c.sessionSettings(settings, p), nil
}

// goalLeadSettings is the --settings value goal leads run with: the lead's
// settings, lead/.claude/settings.json, all but their "agent", as
// sessionSettings. The lead's session takes its agent from that file, a goal lead
// from --agent. Not the lead's settings.local.json: a goal lead works in its own
// checkout.
func (c *Crew) goalLeadSettings(p *Project) (string, error) {
	lead, err := readSettings(c.path("lead/.claude/settings.json"))
	if err != nil {
		return "", err
	}
	lead.del("agent")
	return c.sessionSettings(lead, p), nil
}

// launchWorker is the launcher of project p's workers. It fails when their
// settings do (workerSettings).
func (c *Crew) launchWorker(p *Project) (launcher, error) {
	settings, err := c.workerSettings(p)
	if err != nil {
		return launcher{}, err
	}
	return c.newLauncher(p, "worker", "implementer", "--permission-mode", p.Settings["WORKER_PERMISSION_MODE"],
		"--autocompact", p.Settings["WORKER_AUTOCOMPACT_WINDOW"], "--settings", settings), nil
}
