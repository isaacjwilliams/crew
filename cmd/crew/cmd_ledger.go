package main

// The commands that read and write ledger files directly.

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// The settings crew config lists, in order.
var configKeys = []string{"MAX_WORKERS", "MAX_IN_FLIGHT", "BRANCH_PREFIX", "WORK_ROOT", "WORKER_PERMISSION_MODE",
	"WORKER_AUTOCOMPACT_WINDOW", "LEAD_PERMISSION_MODE", "PRE_PR_REVIEW", "PR_MODE", "REPLY_TO_THREADS", "MAX_REVIEW_ROUNDS",
	"WORKTREE_COPY", "WORKTREE_TOOL", "GARDENER_LOOKBACK", "GARDENER_MAX_TURNS", "REMOTE", "REPO", "DEFAULT_BRANCH"}

// crew config [KEY] [--project P]: the effective settings, with the project's
// overrides (the current repo's, or P's).
func cmdConfig(c *Crew, args []string) error {
	key, name := "", ""
	for len(args) > 0 {
		if args[0] == "--project" && len(args) > 1 {
			name, args = args[1], args[2:]
		} else {
			key, args = args[0], args[1:]
		}
	}
	settings := c.Settings
	if name == "" {
		name, _ = c.projectFromCwd()
	}
	if name != "" {
		p, err := c.loadProject(name)
		if err != nil {
			return err
		}
		settings = p.Settings
	}
	value := func(k string) string {
		switch k {
		case "CREW_HOME":
			return c.Home
		case "PROJECT":
			return name
		}
		if v, ok := settings[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	if key != "" {
		fmt.Println(value(key))
		return nil
	}
	fmt.Println("CREW_HOME=" + c.Home)
	if name != "" {
		fmt.Println("PROJECT=" + name)
	}
	for _, k := range configKeys {
		fmt.Printf("%s=%s\n", k, value(k))
	}
	return nil
}

// crew list [--all] [--status S]
func cmdList(c *Crew, args []string) error {
	all, want := false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all = true
		case args[0] == "--status" && len(args) > 1:
			want, args = args[1], args[1:]
		}
		args = args[1:]
	}
	scope := ""
	if !all {
		scope, _ = c.projectFromCwd()
	}
	for _, t := range c.tasks(scope) {
		st := t.Get("status")
		if want != "" && st != want {
			continue
		}
		fmt.Printf("%-14s %-34s %-20s %s\n", cut(t.Project(), 14), cut(t.ID(), 34), st, t.Get("title"))
	}
	return nil
}

// cut is s cut to n characters.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func cmdShow(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew show TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func cmdPath(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew path TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	fmt.Println(t.Path)
	return nil
}

func cmdFind(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew find TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	fmt.Println(t.ID())
	return nil
}

func cmdGet(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew get TASK KEY")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	if len(args) < 2 || args[1] == "" {
		return fail("key required")
	}
	if v, ok := t.Lookup(args[1]); ok {
		fmt.Println(v)
	}
	return nil
}

// crew set TASK KEY VALUE...
func cmdSet(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew set TASK KEY VALUE")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	if len(args) < 2 || args[1] == "" {
		return fail("key required")
	}
	key, value := args[1], strings.Join(args[2:], " ")
	if err := c.lock(); err != nil {
		return err
	}
	if err := t.Set(key, value); err != nil {
		return err
	}
	t.Touch()
	if key == "status" {
		t.Log("status → " + value)
	}
	c.commit("set " + t.ID() + " " + key)
	return nil
}

// crew log TASK MESSAGE...
func cmdLog(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew log TASK MESSAGE")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	msg := strings.Join(args[1:], " ")
	if msg == "" {
		return fail("message required")
	}
	if err := c.lock(); err != nil {
		return err
	}
	if err := t.Log(msg); err != nil {
		return err
	}
	t.Touch()
	c.commit("log " + t.ID())
	return nil
}

// crew commit [MESSAGE] | crew commit --hook
func cmdCommit(c *Crew, args []string) error {
	if len(args) > 0 && args[0] == "--hook" {
		// A hook must never fail the tool call, and lock can refuse (on another
		// machine, say).
		c.commitHook(os.Stdin)
		return nil
	}
	if err := c.lock(); err != nil {
		return err
	}
	msg := strings.Join(args, " ")
	if msg == "" {
		msg = "manual edits"
	}
	c.commit(msg)
	fmt.Println("ledger committed")
	return nil
}

// commitHook is a PostToolUse hook, after an agent's Edit, Write or Bash call:
// whatever the call changed in the ledger is committed (and so pushed) at once,
// as crew's own changes are. Agents often edit the ledger with a script run
// through Bash, so the hook asks git what changed rather than reading a path
// from the hook JSON on in.
func (c *Crew) commitHook(in io.Reader) {
	io.Copy(io.Discard, in)
	if len(c.ledgerChanges()) == 0 {
		return
	}
	if c.lock() != nil {
		return
	}
	switch changed := c.ledgerChanges(); len(changed) {
	case 0: // a crew command committed them meanwhile
	case 1:
		c.commit("edit " + changed[0])
	default:
		c.commit(fmt.Sprintf("edit %d files", len(changed)))
	}
}

// ledgerChanges lists the files under the state paths that differ from the
// last commit, relative to the home.
func (c *Crew) ledgerChanges() []string {
	out, err := git(c.Home, append([]string{"status", "--porcelain", "-z", "-uall", "--no-renames", "--"}, statePaths...)...)
	if err != nil {
		return nil
	}
	var changed []string
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) > 3 {
			changed = append(changed, entry[3:])
		}
	}
	return changed
}

// crew proposals [--all]
func cmdProposals(c *Crew, args []string) error {
	all := len(args) > 0 && args[0] == "--all"
	p, _ := c.projectFromCwd()
	n := 0
	for _, f := range c.files("proposals", p) {
		d := Doc{f}
		st := d.Get("status")
		if !all && st != "open" {
			continue
		}
		fmt.Printf("%-44s %-9s %-3s %s\n", cut(d.ID(), 44), st, d.Get("size"), d.Get("title"))
		n++
	}
	if n == 0 {
		if all {
			fmt.Fprintln(os.Stderr, "No proposals.")
		} else {
			fmt.Fprintln(os.Stderr, "No open proposals.")
		}
	}
	return nil
}
