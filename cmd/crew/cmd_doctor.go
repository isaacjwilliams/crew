package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// crewVersion is the version of crew's code in this home, which comes with each
// upgrade.
func (c *Crew) crewVersion() string {
	b, err := os.ReadFile(c.path("VERSION"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

type doctor struct {
	ok bool
}

func (d *doctor) check(desc string, ok bool, hint string) {
	if ok {
		fmt.Printf("  ok   %s\n", desc)
		return
	}
	if hint != "" {
		desc += " — " + hint
	}
	fmt.Printf("  FAIL %s\n", desc)
	d.ok = false
}

// crew doctor: dependencies, versions, trust and settings.
func cmdDoctor(c *Crew, args []string) error {
	d := &doctor{ok: true}
	home, _ := os.UserHomeDir()
	fmt.Printf("crew %s at %s\n", c.crewVersion(), c.Home)
	fmt.Println("Dependencies")
	for _, t := range runtimeTools {
		_, err := exec.LookPath(t)
		d.check(t+" installed", err == nil, "")
	}
	if v, ok := claudeVersion(); ok {
		d.check(fmt.Sprintf("claude %s >= %s", v, minClaude), versionAtLeast(v, minClaude), "run 'claude update'")
	}
	d.check("gh authenticated", exec.Command("gh", "auth", "status").Run() == nil, "run 'gh auth login'")
	d.check(fmt.Sprintf("crew's binary is version %s, as VERSION says", version), version == c.crewVersion(), "run crew install, or remove bin/release/ so bin/crew fetches the right one")
	fmt.Println("Agents")
	for _, a := range []string{"lead", "implementer", "casual-observer", "plan-reviewer", "gardener", "goal-lead"} {
		d.check(a, isFile(filepath.Join(home, ".claude/agents", a+".md")), "run crew install")
	}
	fmt.Println("Crew home")
	d.check(c.Home+" is a git repo", gitOK(c.Home, "rev-parse"), "")
	fi, err := os.Stat(c.path("bin/crew-guard"))
	d.check("crew-guard executable", err == nil && fi.Mode()&0o111 != 0, "")
	// The settings hook is what guards the subagents a lead or worker spawns. An
	// edited settings file keeps its old contents on re-install, so it can be missing.
	for _, s := range []struct{ file, role string }{{"lead/.claude/settings.json", "lead"}, {"config/worker-settings.json", "worker"}} {
		d.check(s.file+" runs crew-guard", c.hookEndsWith(s.file, "PreToolUse", `crew-guard" `+s.role), "restore its hook: git -C "+c.Home+" diff "+s.file)
	}
	// Without it, an agent's own edits to the ledger wait for crew's next commit.
	for _, f := range []string{"lead/.claude/settings.json", "config/worker-settings.json"} {
		d.check(f+" commits ledger edits (crew commit --hook)", c.hookEndsWith(f, "PostToolUse", `bin/crew" commit --hook`), "restore its hook: git -C "+c.Home+" diff "+f)
	}
	d.check("lead/.claude/settings.local.json written for this machine", isFile(c.path("lead/.claude/settings.local.json")), "run crew install")
	envs, _ := filepath.Glob(c.path("projects", "*", "project.env"))
	for _, env := range envs {
		pd := filepath.Dir(env)
		p := filepath.Base(pd)
		own := c.Settings.clone()
		if err := readEnvFile(env, own); err != nil {
			d.check(p+": project.env reads", false, err.Error())
		}
		switch tool := own["WORKTREE_TOOL"]; tool {
		case "", "git":
		case "worktrunk":
			_, err := exec.LookPath("wt")
			d.check(p+": wt installed (WORKTREE_TOOL=worktrunk)", err == nil, "install Worktrunk: https://worktrunk.dev")
		default:
			d.check(fmt.Sprintf("%s: WORKTREE_TOOL is git or worktrunk (got '%s')", p, tool), false, "fix "+pd+"/project.env")
		}
		local := Settings{}
		err := readEnvFile(filepath.Join(pd, "local.env"), local)
		d.check(p+": cloned on this machine (local.env)", err == nil && local["REPO"] != "" && isDir(local["REPO"]), "run 'crew project add' in your clone of it")
		if ws := filepath.Join(pd, "worker-settings.json"); isFile(ws) {
			b, _ := os.ReadFile(ws)
			_, err := parseJSON(b)
			d.check(p+": worker-settings.json is valid JSON", err == nil, "fix it; dispatch refuses to start workers until then")
		}
	}
	fmt.Println("Trust (workers start in worktrees under WORK_ROOT; the lead in the home's lead/)")
	for _, dir := range []string{c.Settings["WORK_ROOT"], c.path("lead")} {
		d.check(dir+" trusted", trusted(home, dir), "cd "+dir+" && claude, accept, /exit")
	}
	// A service runs crew with the PATH it sets, not your shell's, so a tool your
	// shell finds through its config (mise, nvm, ...) can be missing there.
	fmt.Println("Enabled services")
	for _, s := range crewServices {
		if !serviceEnabled(s) {
			continue
		}
		service, path, fix := c.servicePath(s)
		for _, t := range runtimeTools {
			d.check(service+" finds "+t, onPath(t, path), fix)
		}
	}
	fmt.Println("AGENTS.md loading")
	required := d.ok
	d.check("Project instructions = claude-md-and-agents-md (optional)", agentsMDOn(home), "only matters for repos that have both CLAUDE.md and AGENTS.md")
	if !required {
		fmt.Println("Some required checks failed.")
		return exitQuietly(1)
	}
	fmt.Println("Required checks passed.")
	return nil
}

// hookEndsWith: one of the settings file's hooks for event runs a command that
// ends with suffix.
func (c *Crew) hookEndsWith(file, event, suffix string) bool {
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile(c.path(file))
	if err != nil || json.Unmarshal(b, &settings) != nil {
		return false
	}
	for _, group := range settings.Hooks[event] {
		for _, h := range group.Hooks {
			if strings.HasSuffix(h.Command, suffix) {
				return true
			}
		}
	}
	return false
}

// onPath: an executable named tool is in one of path's directories.
func onPath(tool, path string) bool {
	for _, dir := range filepath.SplitList(path) {
		if fi, err := os.Stat(filepath.Join(dir, tool)); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// trusted: Claude Code trusts dir. Trust accepted in a folder covers the folders
// below it, and inside a git repo Claude Code records it for the repo's top level
// (the home, for lead/), so a folder above dir counts too.
func trusted(home, dir string) bool {
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	for {
		if cfg.Projects[dir].HasTrustDialogAccepted {
			return true
		}
		if dir == "/" || dir == "." || dir == "" {
			return false
		}
		dir = filepath.Dir(dir)
	}
}
