package main

// Background sessions: what `claude agents --json --all` says about them, and
// running claude itself.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Agent is one entry of `claude agents --json --all`.
type Agent struct {
	ID         string  `json:"id"`
	State      string  `json:"state"`
	PID        *int64  `json:"pid"`
	Status     string  `json:"status"`
	WaitingFor *string `json:"waitingFor"`
	SessionID  string  `json:"sessionId"`
	Name       string  `json:"name"`
	// TurnEnded: the session's transcript says its last turn has ended, though its
	// state may still be working (see busy).
	TurnEnded bool `json:"-"`
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
func (c *Crew) agents() Agents {
	out, err := c.claude("agents", "--json", "--all").Output()
	var as Agents
	if err != nil || json.Unmarshal(out, &as) != nil {
		return Agents{}
	}
	for i, a := range as {
		if a.State == "working" && a.PID != nil && a.SessionID != "" {
			as[i].TurnEnded = turnEnded(a.SessionID)
		}
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
		if e.Type == "user" || e.Type == "assistant" || e.Subtype == "turn_duration" {
			return e.Type == "system"
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
func (a Agent) open() bool { return a.PID != nil }

// state says what a job is doing, for status lines: its state, and the prompt
// it's waiting on, if any. Nothing for a job that isn't listed.
func (as Agents) state(job string) string {
	a := as.find(job)
	switch {
	case a == nil:
		return ""
	case a.TurnEnded:
		return "idle, background task running"
	case a.WaitingFor != nil:
		return a.State + " (" + *a.WaitingFor + ")"
	}
	return a.State
}

func (as Agents) busy(job string) bool {
	a := as.find(job)
	return a != nil && a.busy()
}

// listed: claude agents on this machine knows the job.
func (as Agents) listed(job string) bool { return as.find(job) != nil }

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
