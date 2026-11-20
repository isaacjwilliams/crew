package main

// The commands that read and write ledger files directly.

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// The settings crew config lists, in order.
var configKeys = []string{"MAX_WORKERS", "GOAL_MAX_IN_FLIGHT", "BRANCH_PREFIX", "WORK_ROOT", "WORKER_PERMISSION_MODE",
	"WORKER_AUTOCOMPACT_WINDOW", "LEAD_PERMISSION_MODE", "PRE_PR_REVIEW", "PR_MODE", "REPLY_TO_THREADS", "MAX_REVIEW_ROUNDS", "MAX_CI_FIXES", "AUTO_REVIEWS",
	"MAX_PR_LINES", "CODEX_REVIEW", "CODEX_MODEL", "CODEX_MODEL_EFFORT", "AUTO_REVIEW_REBUTTALS",
	"SENSITIVE_PATHS", "WORKTREE_COPY", "WORKTREE_TOOL", "EVIDENCE_REPO", "GARDENER_LOOKBACK", "GARDENER_MAX_TURNS", "REMOTE", "REPO", "DEFAULT_BRANCH"}

// crew config [KEY] [--project P|--task T]: the effective settings, with the
// overrides of P or the default project (defaultProject), or of T's project,
// with the AUTO_REVIEWS T's worker reads (the one setting a task can change).
func cmdConfig(c *Crew, args []string) error {
	key, name, task := "", "", ""
	for len(args) > 0 {
		switch {
		case args[0] == "--project" && len(args) > 1:
			name, args = args[1], args[2:]
		case args[0] == "--task" && len(args) > 1:
			task, args = args[1], args[2:]
		default:
			key, args = args[0], args[1:]
		}
	}
	var t Task
	if task != "" {
		var err error
		if t, err = c.resolveTask(task); err != nil {
			return err
		}
		name = t.Project()
	}
	settings := c.Settings
	name, err := c.scope(name, false)
	if err != nil {
		return err
	}
	if name != "" {
		p, err := c.loadProject(name)
		if err != nil {
			return err
		}
		settings = p.Settings
	}
	if task != "" {
		settings = settings.clone()
		settings["AUTO_REVIEWS"] = t.AutoReviews(settings["AUTO_REVIEWS"])
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

// crew list [--all|--project P] [--status S]
func cmdList(c *Crew, args []string) error {
	all, proj, want := false, "", ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		case args[0] == "--status" && len(args) > 1:
			want, args = args[1], args[1:]
		default:
			return usage("crew list [--all|--project P] [--status S]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
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

// crew set TASK KEY VALUE...: TASK can also be a proposal's file, by its path,
// since resolve finds a proposal by ID only when asked for proposals. Each kind
// runs its own checks.
func cmdSet(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew set TASK KEY VALUE")
	}
	f, err := c.resolve("tasks", args[0])
	if err != nil {
		return err
	}
	if len(args) < 2 || args[1] == "" {
		return fail("key required")
	}
	// Trimmed once, so the checks see what Lookup will read back.
	key, value := args[1], strings.TrimSpace(strings.Join(args[2:], " "))
	d := Doc{f}
	if isProposal(f) {
		err = Proposal{d}.CheckSet(key, value)
	} else {
		value, err = c.checkTaskSet(Task{d}, key, value)
	}
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	if key == "status" {
		err = d.setStatus(value)
	} else {
		err = d.Set(key, value)
	}
	if err != nil {
		return err
	}
	// A lead hears the statuses crew's sessions set; crew tick reports its own.
	if key == "status" && !isProposal(f) && inCrewSession() {
		c.recordNews(taskProjectDir(Task{d}), d.ID()+": "+statusLogged+value)
	}
	d.Touch()
	c.commit("set " + d.ID() + " " + key)
	return nil
}

// checkTaskSet is the value crew set records for a task's key, or a refusal.
func (c *Crew) checkTaskSet(t Task, key, value string) (string, error) {
	// An empty value clears the key. crew ready refuses a task whose kind needs
	// one it cleared.
	if value != "" {
		var err error
		if value, err = c.checkValue(t.Project(), key, value); err != nil {
			return "", err
		}
	}
	// done means a spike's report is in, which crew hand-over checks.
	if key == "status" && Status(value) == Done {
		return "", fail("only crew hand-over sets done, once the report is in: crew hand-over %s", t.ID())
	}
	if key == "status" && Status(value) == PROpen {
		return "", fail("use crew hand-over %s: it checks the evidence, the risk tier and the security review before the PR goes to the user", t.ID())
	}
	if err := c.checkStacks(t, key, value); err != nil {
		return "", err
	}
	return value, nil
}

// checkStacks refuses a base or kind that would put a task in a stack its kind
// can't take part in (stackRefusal), as crew new --on does.
func (c *Crew) checkStacks(t Task, key, value string) error {
	if key != "base" && key != "kind" {
		return nil
	}
	idx := c.branches(taskProjectDir(t))
	if key == "base" {
		if parent, ok := idx.parentWith(t, value); ok {
			return stackRefusal(t.Kind(), parent, parent.Kind())
		}
		return nil
	}
	if parent, ok := idx.parentOf(t); ok {
		if err := stackRefusal(Kind(value), parent, parent.Kind()); err != nil {
			return err
		}
	}
	for _, child := range idx.childrenOf(t) {
		if err := stackRefusal(child.Kind(), t, Kind(value)); err != nil {
			return err
		}
	}
	return nil
}

// checkValue is the value crew set records for key, or a refusal that says what
// the key takes. A key that links to another task (informs, fixes) names it, so
// it's resolved as crew new resolves it, and recorded as that task's ID. Keys
// crew doesn't check take any value. crew ready checks the keys a task's kind
// needs with it too.
func (c *Crew) checkValue(proj, key, value string) (string, error) {
	var ok bool
	var takes string
	switch key {
	case "priority":
		_, ok = parsePriority(value)
		takes = "a whole number"
	case "risk":
		ok = Risk(value).Valid()
		takes = orList(risks)
	case "kind":
		ok = Kind(value).Valid()
		takes = kindNames()
	case "missed_by":
		ok = slices.Contains(missedBy, value)
		takes = orList(missedBy)
	case "model":
		ok = slices.Contains(models, value)
		takes = orList(models)
	case "auto_review":
		ok = value == autoReviewOff
		takes = autoReviewOff
	default:
		if r, ok := linkRules(key); ok {
			return c.resolveLink(proj, r, value)
		}
		return value, nil
	}
	if !ok {
		return "", mustBe(key, takes, value)
	}
	return value, nil
}

// mustBe refuses a value for key, saying what it takes.
func mustBe(key, takes, value string) error {
	if value == "" {
		return fail("%s must be %s", key, takes)
	}
	return fail("%s must be %s, not '%s'", key, takes, value)
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
