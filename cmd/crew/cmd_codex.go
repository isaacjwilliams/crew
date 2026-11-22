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
	"slices"
	"strconv"
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
// OUT_PREFIX.events.jsonl. What the run cost, however it ended, goes to
// OUT_PREFIX.cost, for pr-review to record, and to stdout. It exits with
// Codex's status, or 124 when Codex wrote no event for codexStallLimit and was
// stopped.
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
	home, err := newReviewCodexHome()
	if err != nil {
		return err
	}
	defer home.remove()
	status, stalled, err := runCodex(argv, home, root, prompt, events)
	if err != nil {
		return err
	}
	cost := runCost{took: time.Since(began), tokens: codexTokens(events),
		models: []string{settings["CODEX_MODEL"]}, efforts: []string{settings["CODEX_MODEL_EFFORT"]}}
	// The review is Codex's report and status: a cost that can't be kept
	// mustn't change them.
	if err := os.WriteFile(out+".cost", []byte(cost.String()+"\n"), 0o644); err != nil {
		warn("can't keep what the run cost: %v", err)
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
func runCodex(argv []string, home reviewCodexHome, root, prompt, events string) (status int, stalled bool, err error) {
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
	cmd.Env = home.environ("CLAUDECODE")
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

func (u codexUsage) add(v codexUsage) codexUsage {
	return codexUsage{u.Input + v.Input, u.CacheRead + v.CacheRead, u.Output + v.Output}
}

func (u codexUsage) String() string {
	return fmt.Sprintf("%s input including %s cache read, %s output", thousands(u.Input), thousands(u.CacheRead), thousands(u.Output))
}

// codexTokens is the usage of every turn in a Codex events file, or nil when
// none reported any. Codex reports usage only when a turn completes, so a run
// it was stopped in reports none.
func codexTokens(events string) tokenUsage {
	b, _ := os.ReadFile(events)
	var sum codexUsage
	turns := 0
	for _, line := range strings.Split(string(b), "\n") {
		var e struct {
			Type  string
			Usage *codexUsage
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Type != "turn.completed" || e.Usage == nil {
			continue
		}
		sum = sum.add(*e.Usage)
		turns++
	}
	if turns == 0 {
		return nil
	}
	return sum
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

// codexSignedIn: Codex is signed in the way a review runs it, so a review can run.
// It uses a review's home, not the user's, so it checks the sign-in as that home
// sees it, through the link to auth.json.
func codexSignedIn() bool {
	home, err := newReviewCodexHome()
	if err != nil {
		return false
	}
	defer home.remove()
	cmd := exec.Command("codex", "login", "status")
	cmd.Env = home.environ()
	return cmd.Run() == nil
}

// reviewCodexHome is the Codex home a review runs in: the user's own with no
// MCP server in it, so a server that can't start never stalls a review. Codex
// merges its config layers and `-c` can't empty a table, so crew leaves the
// servers out of the config it hands Codex. It also leaves out the `projects`
// trust entries, so a repo's own `.codex/config.toml` isn't loaded, and
// everything else in the user's home (plugins, skills, AGENTS.md, memories),
// since a plugin can declare servers too. The sign-in and the rest of the
// config, the model provider's among it, are the user's.
type reviewCodexHome struct {
	dir, real string
}

func newReviewCodexHome() (reviewCodexHome, error) {
	real := os.Getenv("CODEX_HOME")
	if real == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return reviewCodexHome{}, err
		}
		real = filepath.Join(user, ".codex")
	}
	dir, err := os.MkdirTemp("", "crew-codex-home-")
	if err != nil {
		return reviewCodexHome{}, err
	}
	home := reviewCodexHome{dir, real}
	if err := home.fill(); err != nil {
		home.remove()
		return reviewCodexHome{}, fmt.Errorf("can't set up a Codex home for the review: %w", err)
	}
	return home, nil
}

// fill writes the filtered config, and links the sign-in when there is one. A
// user with no config.toml gets an empty one.
func (h reviewCodexHome) fill() error {
	config, err := os.ReadFile(filepath.Join(h.real, "config.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(filepath.Join(h.dir, "config.toml"), withoutMCPServers(string(config)), 0o600); err != nil {
		return err
	}
	if !isFile(filepath.Join(h.real, "auth.json")) {
		return nil
	}
	return os.Symlink(filepath.Join(h.real, "auth.json"), filepath.Join(h.dir, "auth.json"))
}

// environ is crew's environment, without the named variables, with this home
// as the Codex home.
func (h reviewCodexHome) environ(without ...string) []string {
	return append(environWithout(slices.Concat(without, []string{"CODEX_HOME"})...), "CODEX_HOME="+h.dir)
}

// remove deletes the home, after saving the sign-in.
func (h reviewCodexHome) remove() {
	h.saveSignIn()
	os.RemoveAll(h.dir)
}

// saveSignIn puts a refreshed token back in the user's home. Codex may replace
// the link to auth.json with a file of its own when it refreshes the token, and
// the old token may stop working. It's best effort: a token that can't be saved
// leaves the user to run `codex login` again.
func (h reviewCodexHome) saveSignIn() {
	mine := filepath.Join(h.dir, "auth.json")
	if info, err := os.Lstat(mine); err != nil || !info.Mode().IsRegular() {
		return
	}
	if b, err := os.ReadFile(mine); err == nil {
		os.WriteFile(filepath.Join(h.real, "auth.json"), b, 0o600)
	}
}

// withoutMCPServers is a Codex config without its `mcp_servers` and `projects`
// tables and keys. It reads TOML only as far as it needs to: it splits the file
// into statements (a table header, or a key with its value, which may run over
// lines), and names each one by its first key. A key is only looked for before
// the first table, where Codex users write it.
func withoutMCPServers(config string) []byte {
	var kept, statement []string
	root, dropping := true, false
	var scan tomlScan
	for _, line := range strings.Split(config, "\n") {
		if text := strings.TrimSpace(line); len(statement) == 0 && (text == "" || strings.HasPrefix(text, "#")) {
			if !dropping {
				kept = append(kept, line)
			}
			continue
		}
		statement = append(statement, line)
		if scan.line(line); scan.unfinished() {
			continue
		}
		text := strings.Join(statement, "\n")
		statement, scan = nil, tomlScan{}
		drop := dropping
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			root = false
			dropping = isDroppedName(firstKey(text))
			drop = dropping
		} else if root && isDroppedName(firstKey(text)) {
			drop = true
		}
		if !drop {
			kept = append(kept, text)
		}
	}
	if len(statement) > 0 && !dropping {
		kept = append(kept, statement...)
	}
	return []byte(strings.Join(kept, "\n"))
}

// tomlScan follows a statement over its lines: the brackets and braces it has
// open, and the multi-line string it's in, if any. Brackets inside strings and
// comments don't count.
type tomlScan struct {
	depth int
	multi string // `"""` or `'''` while inside a multi-line string
}

func (t *tomlScan) unfinished() bool { return t.depth > 0 || t.multi != "" }

func (t *tomlScan) line(line string) {
	for i := 0; i < len(line); i++ {
		rest := line[i:]
		switch {
		case t.multi != "":
			if t.multi == `"""` && line[i] == '\\' {
				i++
			} else if strings.HasPrefix(rest, t.multi) {
				// The string's own last one or two characters may be quotes too.
				t.multi = ""
				i += 2
				for extra := 0; extra < 2 && i+1 < len(line) && line[i+1] == rest[0]; extra++ {
					i++
				}
			}
		case line[i] == '#':
			return
		case strings.HasPrefix(rest, `"""`), strings.HasPrefix(rest, `'''`):
			t.multi = rest[:3]
			i += 2
		case line[i] == '"' || line[i] == '\'':
			i += quotedLength(rest) - 1
		case line[i] == '[' || line[i] == '{':
			t.depth++
		case line[i] == ']' || line[i] == '}':
			t.depth--
		}
	}
}

// quotedLength is the length of the one-line string rest starts with, or all of
// rest when the string isn't closed.
func quotedLength(rest string) int {
	for i := 1; i < len(rest); i++ {
		switch {
		case rest[0] == '"' && rest[i] == '\\':
			i++
		case rest[i] == rest[0]:
			return i + 1
		}
	}
	return len(rest)
}

// firstKey is the first part of the key a statement sets or the table it opens,
// without its quotes or escapes: `mcp_servers` in `["mcp_servers".x] # a comment`.
func firstKey(statement string) string {
	text := strings.TrimLeft(strings.TrimSpace(statement), "[ \t")
	if text != "" && (text[0] == '"' || text[0] == '\'') {
		quoted := text[:quotedLength(text)]
		if len(quoted) < 2 || quoted[len(quoted)-1] != text[0] {
			return quoted // not closed: a broken config, which Codex will refuse
		}
		if text[0] == '"' {
			if decoded, err := strconv.Unquote(quoted); err == nil {
				return decoded
			}
		}
		return quoted[1 : len(quoted)-1]
	}
	end := strings.IndexAny(text, ". =\t]#")
	if end < 0 {
		return text
	}
	return text[:end]
}

// isDroppedName: a key is one of the names withoutMCPServers leaves out.
func isDroppedName(key string) bool {
	return key == "mcp_servers" || key == "projects"
}
