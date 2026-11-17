package main

// crew codex: one turn of an automated review by Codex, always read-only. It's
// the only way crew runs Codex.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// codexStallLimit is how long Codex may go without writing an event before crew
// stops it. Tests shorten it.
var codexStallLimit = 15 * time.Minute

// exitCodexStalled is crew codex's exit code when it stopped a stalled Codex.
const exitCodexStalled = 124

// codexReadOnly goes on every call: Codex reviews and never changes anything.
var codexReadOnly = []string{"-c", `sandbox_mode="read-only"`, "-c", `approval_policy="never"`}

// crew codex ROOT PROMPT_FILE OUT_PREFIX
//
// Runs `codex exec` in ROOT with the prompt in PROMPT_FILE, in a new session
// each time. Codex's final message goes to OUT_PREFIX.md and its events to
// OUT_PREFIX.events.jsonl. It prints what the run cost, however it ended, and
// exits with Codex's status, or 124 when Codex wrote no event for
// codexStallLimit and was stopped.
func cmdCodex(c *Crew, args []string) error {
	if len(args) != 3 {
		return usage("crew codex ROOT PROMPT_FILE OUT_PREFIX")
	}
	root, prompt, out := args[0], args[1], args[2]
	if !isDir(root) {
		return fail("no such directory: %s", root)
	}
	if !isFile(prompt) {
		return fail("no such prompt file: %s", prompt)
	}
	// Codex runs in root, so make the paths absolute while they still mean what
	// the caller meant.
	for _, p := range []*string{&root, &prompt, &out} {
		*p, _ = filepath.Abs(*p)
	}
	project, err := c.defaultProject()
	if err != nil {
		return err
	}
	settings := c.Settings
	if project != "" {
		if settings, err = c.projectSettings(project); err != nil {
			return err
		}
	}

	argv := append([]string{"exec"}, codexReadOnly...)
	argv = append(argv, "-m", settings["CODEX_MODEL"], "-c", fmt.Sprintf("model_reasoning_effort=%q", settings["CODEX_MODEL_EFFORT"]),
		"--skip-git-repo-check", "--json", "-o", out+".md", "-C", root, "-")
	events := out + ".events.jsonl"
	began := time.Now()
	status, stalled, err := runCodex(argv, root, prompt, events)
	if err != nil {
		return err
	}
	cost := runCost{took: time.Since(began), tokens: codexTokens(events)}
	if m := settings["CODEX_MODEL"]; m != "" {
		cost.models = []string{m}
	}
	if e := settings["CODEX_MODEL_EFFORT"]; e != "" {
		cost.efforts = []string{e}
	}
	switch {
	case stalled:
		warn("Codex wrote nothing for %s and was stopped", codexStallLimit)
		status = exitCodexStalled
	case status != 0:
		warn("codex exited with status %d; see %s", status, events)
	}
	fmt.Printf("final message: %s.md\ncost: %s\n", out, cost)
	if status != 0 {
		return exitQuietly(status)
	}
	return nil
}

// runCodex runs codex with argv in root, the prompt file on its stdin and its
// events written to the events file, and returns its exit status. It stops
// Codex, and says it stalled, when codexStallLimit passes without an event.
func runCodex(argv []string, root, prompt, events string) (status int, stalled bool, err error) {
	in, err := os.Open(prompt)
	if err != nil {
		return 0, false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(events), 0o755); err != nil {
		return 0, false, err
	}
	log, err := os.Create(events)
	if err != nil {
		return 0, false, err
	}
	defer log.Close()

	wrote := make(chan struct{}, 1)
	cmd := exec.Command("codex", argv...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = root, in, notifyingWriter{log, wrote}, os.Stderr
	// Codex isn't part of the Claude Code session that runs crew: a claude that
	// Codex ran would otherwise take itself for a nested session and refuse.
	cmd.Env = environWithout("CLAUDECODE")
	// Once Codex has exited, don't wait long for a process it started that still
	// holds its output open.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return 0, false, fail("can't run codex: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	quiet := time.NewTimer(codexStallLimit)
	defer quiet.Stop()
	for {
		select {
		case <-wrote:
			quiet.Reset(codexStallLimit)
		case <-quiet.C:
			stalled = true
			cmd.Process.Kill()
		case err := <-done:
			status, err := exitStatus(err)
			return status, stalled, err
		}
	}
}

// codexUsage is the usage Codex reports for a turn. Its input includes the
// tokens it read from cache.
type codexUsage struct {
	Input     int `json:"input_tokens"`
	CacheRead int `json:"cached_input_tokens"`
	Output    int `json:"output_tokens"`
}

func (u codexUsage) total() int { return u.Input + u.Output }

func (u codexUsage) String() string {
	return fmt.Sprintf("%s input including %s cache read, %s output", thousands(u.Input), thousands(u.CacheRead), thousands(u.Output))
}

// codexTokens is the usage of every turn in a Codex events file, or nil when
// none reported any. Codex reports usage only when a turn completes, so a run
// it was stopped in reports none.
func codexTokens(events string) tokenUsage {
	b, _ := os.ReadFile(events)
	var sum *codexUsage
	for _, line := range strings.Split(string(b), "\n") {
		var e struct {
			Type  string
			Usage *codexUsage
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Type != "turn.completed" || e.Usage == nil {
			continue
		}
		if sum == nil {
			sum = &codexUsage{}
		}
		sum.Input += e.Usage.Input
		sum.CacheRead += e.Usage.CacheRead
		sum.Output += e.Usage.Output
	}
	if sum == nil {
		return nil
	}
	return *sum
}

// exitStatus is the status a finished command exited with, given what Wait
// returned: 1 for one ended by a signal, which has no status of its own.
func exitStatus(err error) (int, error) {
	var exit *exec.ExitError
	switch {
	case err == nil || errors.Is(err, exec.ErrWaitDelay):
		return 0, nil
	case !errors.As(err, &exit):
		return 0, err
	case exit.ExitCode() < 0:
		return 1, nil
	}
	return exit.ExitCode(), nil
}

// notifyingWriter writes to w and signals each write on wrote, without waiting
// for anyone to take the signal.
type notifyingWriter struct {
	w     io.Writer
	wrote chan struct{}
}

func (n notifyingWriter) Write(p []byte) (int, error) {
	select {
	case n.wrote <- struct{}{}:
	default:
	}
	return n.w.Write(p)
}

// codexSignedIn: Codex is signed in, so a review can run.
func codexSignedIn() bool {
	return exec.Command("codex", "login", "status").Run() == nil
}
