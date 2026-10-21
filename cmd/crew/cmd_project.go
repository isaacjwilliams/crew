package main

// Projects and their checkouts: crew project add, crew checkout, crew observe.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func cmdProject(c *Crew, args []string) error {
	if len(args) > 0 && args[0] == "add" {
		return c.projectAdd(args[1:])
	}
	return usage("crew project add [PATH]")
}

// projectAdd registers a repo: project.env records what every machine shares (its
// remote and default branch), and local.env this machine's clone. On a machine
// where the project is registered already, it records only the clone.
func (c *Crew) projectAdd(args []string) error {
	if err := need("git"); err != nil {
		return err
	}
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	root, ok := repoRoot(dir)
	if !ok {
		return fail("not inside a git repository")
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return fail("%s has no origin remote; crew works through origin", root)
	}
	slug := slugFor(root)
	pdir := c.path("projects", slug)
	if err := c.lock(); err != nil {
		return err
	}
	for _, d := range []string{filepath.Join(pdir, "tasks"), filepath.Join(pdir, "proposals"), filepath.Join(c.Settings["WORK_ROOT"], slug)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	def := defaultBranch(root)
	if !isFile(filepath.Join(pdir, "project.env")) {
		env := fmt.Sprintf(`# crew project settings for %s. Any key from config/defaults.env can be overridden here.
REMOTE=%s
DEFAULT_BRANCH=%s
# BRANCH_PREFIX="agent/"
# WORKTREE_COPY=".env.local"
# WORKTREE_TOOL="worktrunk"   # create/remove task worktrees with wt, so the repo's Worktrunk hooks run
# PR_MODE="draft"
# Extra worker settings for this project only (e.g. allow rules, env) go in
# worker-settings.json next to this file; see SETUP.md.
`, slug, envQuote(remote), envQuote(def))
		if err := os.WriteFile(filepath.Join(pdir, "project.env"), []byte(env), 0o644); err != nil {
			return err
		}
		fmt.Printf("Registered %s (%s, default branch %s).\n", slug, remote, def)
	} else {
		fmt.Printf("%s is already registered; left project.env as is.\n", slug)
	}
	// Written every time: this clone is the one this machine uses from now on.
	local := fmt.Sprintf("# This machine's clone of %s. Not committed: each machine has its own.\nREPO=%s\n", slug, envQuote(root))
	if err := os.WriteFile(filepath.Join(pdir, "local.env"), []byte(local), 0o644); err != nil {
		return err
	}
	fmt.Println("This machine's clone: " + root)
	common, _ := git(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err := addLines(filepath.Join(common, "info", "exclude"), ".claude/worktrees/", ".claude/settings.local.json"); err != nil {
		return err
	}
	agents := isFile(filepath.Join(root, "AGENTS.md"))
	if agents {
		fmt.Println("Found AGENTS.md: agents will load it automatically.")
	}
	if isFile(filepath.Join(root, "CLAUDE.md")) || isFile(filepath.Join(root, ".claude", "CLAUDE.md")) {
		if agents {
			fmt.Println("Note: this repo has both CLAUDE.md and AGENTS.md. Claude Code reads only CLAUDE.md by default;")
			fmt.Println("      set Project instructions to claude-md-and-agents-md (see SETUP.md) to load both.")
		}
	} else if !agents {
		fmt.Println("Note: no AGENTS.md or CLAUDE.md found. Agents will have no project conventions to follow.")
	}
	c.commit("project add " + slug)
	return nil
}

// defaultBranch is origin's default branch, as the clone knows it, else main.
func defaultBranch(root string) string {
	head := func() string {
		ref, _ := git(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		return strings.TrimPrefix(ref, "origin/")
	}
	def := head()
	if def == "" {
		gitOK(root, "remote", "set-head", "origin", "--auto")
		def = head()
	}
	if def == "" {
		def = "main"
	}
	return def
}

// envQuote writes a value for a settings file, in double quotes.
func envQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(v) + `"`
}

// addLines adds each line to the file that it doesn't have yet.
func addLines(path string, lines ...string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	have := map[string]bool{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			have[sc.Text()] = true
		}
		f.Close()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, l := range lines {
		if !have[l] {
			if _, err := f.WriteString(l + "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// crew checkout PROJECT: the lead's reading copy of a project, a detached checkout
// of a freshly fetched origin/DEFAULT_BRANCH beside the task worktrees. Prints its
// path.
func cmdCheckout(c *Crew, args []string) error {
	if len(args) != 1 {
		return usage("crew checkout PROJECT")
	}
	p, err := c.loadProject(args[0])
	if err != nil {
		return err
	}
	co := filepath.Join(p.WorkRoot(), p.Name, "_lead")
	if err := p.detachedCheckout(co); err != nil {
		return err
	}
	fmt.Println(co)
	return nil
}

// detachedCheckout makes or moves a read-only checkout of origin/DEFAULT_BRANCH
// for a session that reads the code but never changes it (the gardener, a goal
// lead, the lead). Plain git, never Worktrunk: nothing runs there, so it needs
// none of the repo's per-worktree setup.
func (p *Project) detachedCheckout(path string) error {
	def := p.DefaultBranch()
	if !gitOK(p.Repo(), "fetch", "--quiet", "origin", def) {
		warn("%s: fetch failed; using local origin/%s", p.Name, def)
	}
	if isDir(path) {
		return gitDo(path, "checkout", "--quiet", "--detach", "origin/"+def)
	}
	p.pruneWorktrees()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return gitDo(p.Repo(), "worktree", "add", "--quiet", "--detach", path, "origin/"+def)
}

// pruneWorktrees: a worktree or checkout deleted by hand (rm -rf) stays registered
// with git, which then refuses to make another at its path.
func (p *Project) pruneWorktrees() { gitOK(p.Repo(), "worktree", "prune") }

// gitDo runs git in dir, its errors shown, and fails as a bash crew would have
// under set -e: with git's exit code and nothing more to say.
func gitDo(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return quietly(cmd.Run())
}

// quietly turns a command's failure into crew exiting with its code.
func quietly(err error) error {
	var x *exec.ExitError
	if errors.As(err, &x) {
		return exitQuietly(x.ExitCode())
	}
	return err
}

func need(tool string) error {
	if _, err := exec.LookPath(tool); err != nil {
		return fail("'%s' is required but not installed", tool)
	}
	return nil
}

// crew observe [TASK|PATH] [--base REF]: the casual observer, headless, on a
// task's worktree, a directory, or the current one.
func cmdObserve(c *Crew, args []string) error {
	if err := need("claude"); err != nil {
		return err
	}
	var arg, base string
	for len(args) > 0 {
		if args[0] == "--base" && len(args) > 1 {
			base, args = args[1], args[2:]
		} else {
			arg, args = args[0], args[1:]
		}
	}
	var dir string
	switch {
	case arg != "" && isDir(arg):
		abs, err := filepath.Abs(arg)
		if err == nil {
			dir, err = filepath.EvalSymlinks(abs)
		}
		if err != nil {
			return err
		}
	case arg != "":
		t, err := c.resolveTask(arg)
		if err != nil {
			return err
		}
		dir = t.Worktree()
		if dir == "" {
			return fail("task has no worktree")
		}
		if !isDir(dir) {
			fmt.Printf("nothing to observe: %s's worktree isn't on this machine\n", t.ID())
			return nil
		}
		if base == "" {
			base = "origin/" + t.Base()
			// A merged parent's branch may be gone from origin; base_sha still marks
			// where this task's own commits start.
			if sha := t.Get("base_sha"); !gitOK(dir, "rev-parse", "--verify", "--quiet", base+"^{commit}") && sha != "" {
				base = sha
			}
		}
	default:
		dir, _ = os.Getwd()
	}
	if base == "" {
		base = "origin/HEAD"
		if name, ok := c.projectFromDir(dir); ok {
			p, err := c.loadProject(name)
			if err != nil {
				return err
			}
			base = "origin/" + p.DefaultBranch()
		}
	}
	cmd := c.claude("-p", "--agent", "casual-observer", "--permission-mode", "default", "--permission-prompts", "none",
		"--settings", c.path("config/readonly-settings.json"), "Review the change "+base+"...HEAD in this repository.")
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	return quietly(cmd.Run())
}
