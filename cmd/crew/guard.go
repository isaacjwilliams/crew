package main

// crew guard is the PreToolUse hook that keeps each crew agent's file edits in
// bounds: `crew guard <lead|worker|gardener>` on Edit, Write and NotebookEdit,
// which bin/crew-guard runs for the agents' settings and frontmatter. It reads the
// hook JSON on stdin. Exit 0 allows the call; exit 2 blocks it, and the message on
// stderr goes back to the agent. Whenever it can't work out the path, it blocks.
//
// It checks only the file tools, whose target path is structured input, so no
// shell parsing is involved. Bash isn't checked: shell parsing can never be
// complete. Workers run it in auto mode, the gardener only with its read-only
// allow list, and the agent prompts say what they may do on GitHub.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type guard struct {
	role       string
	home       string   // the crew home; only its state paths take edits
	scratch    []string // directories every role may write in
	projectDir string   // where a worker's session was started: CLAUDE_PROJECT_DIR
}

func cmdGuard(args []string) {
	role := ""
	if len(args) > 0 {
		role = args[0]
	}
	block := func(msg string) {
		fmt.Fprintf(os.Stderr, "Blocked by crew-guard (%s): %s\n", role, msg)
		os.Exit(2)
	}
	if len(args) != 1 || (role != "lead" && role != "worker" && role != "gardener") {
		block("usage: crew-guard <lead|worker|gardener>")
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		block("can't read the hook's input: " + err.Error())
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	userHome, _ := os.UserHomeDir()
	g := guard{
		role:       role,
		home:       crewHome(),
		scratch:    []string{tmp, "/tmp", filepath.Join(userHome, ".claude/jobs"), filepath.Join(userHome, ".claude/plans"), os.Getenv("CLAUDE_JOB_DIR")},
		projectDir: os.Getenv("CLAUDE_PROJECT_DIR"),
	}
	if err := g.check(input); err != nil {
		block(err.Error())
	}
}

// check returns nil when the edit the hook input describes is allowed, and why
// not otherwise.
func (g guard) check(input []byte) error {
	var hook struct {
		Cwd       string `json:"cwd"`
		ToolInput struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &hook); err != nil {
		return fmt.Errorf("can't read the hook's input as JSON, so this action can't be checked")
	}
	cwd := hook.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	p := hook.ToolInput.FilePath
	if p == "" {
		p = hook.ToolInput.NotebookPath
	}
	if p == "" {
		return nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	path, err := canonical(p)
	if err != nil {
		return fmt.Errorf("can't work out where %s leads (%v), so it can't be checked", p, err)
	}

	home, err := canonical(g.home)
	if err != nil {
		return fmt.Errorf("can't work out where the crew home %s is (%v)", g.home, err)
	}
	if under(path, home) {
		for _, s := range statePaths {
			if under(path, filepath.Join(home, s)) {
				return nil
			}
		}
		return fmt.Errorf("%s is part of crew itself. In the crew home only the ledger takes edits: %s/projects and %s/reports.", path, home, home)
	}
	for _, dir := range g.scratch {
		if dir == "" {
			continue
		}
		if d, err := canonical(dir); err == nil && under(path, d) {
			return nil
		}
	}

	switch g.role {
	case "worker":
		// The session's project dir is where it was launched (its worktree), even if
		// it has since cd'd elsewhere.
		base := g.projectDir
		if base == "" {
			base = cwd
		}
		top, _ := git(base, "rev-parse", "--show-toplevel")
		if top == "" {
			return errors.New("not inside a git worktree; workers only edit files in their own worktree.")
		}
		topDir, err := canonical(top)
		if err != nil {
			return fmt.Errorf("can't work out where the worktree %s is (%v)", top, err)
		}
		if common, _ := git(base, "rev-parse", "--path-format=absolute", "--git-common-dir"); common != "" {
			if main, err := canonical(filepath.Dir(common)); err == nil && main == topDir {
				return fmt.Errorf("this session is running in the main checkout (%s), not a crew worktree. Workers must be started with 'crew dispatch'.", top)
			}
		}
		if under(path, topDir) {
			return nil
		}
		return fmt.Errorf("%s is outside your worktree (%s). Only edit files in your worktree, or the ledger via crew.", path, top)
	case "lead":
		return fmt.Errorf("the lead only writes to the ledger (%s/projects, %s/reports). Put code changes in a task and dispatch it.", home, home)
	default:
		return fmt.Errorf("the gardener only writes proposals into the ledger (%s/projects).", home)
	}
}

// under reports whether path is root or inside it. Both must be canonical.
func under(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

// canonical returns p as an absolute path with every symlink in it resolved, as
// `realpath -m` does: the part that doesn't exist yet is taken as written. It
// fails when it can't tell where p leads, a symlink loop say.
func canonical(p string) (string, error) {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = filepath.Join(wd, p)
	}
	out := "/"
	rest := strings.Split(p, "/")
	links := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			out = filepath.Dir(out)
			continue
		}
		next := filepath.Join(out, c)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			out = next
			continue
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			out = next
			continue
		}
		if links++; links > 255 {
			return "", fmt.Errorf("too many symlinks at %s", next)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			out = "/"
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return out, nil
}
