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
// with the AUTO_REVIEWS and PRE_PR_REVIEW T's worker reads (the settings a task
// can change).
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
		settings["PRE_PR_REVIEW"] = t.PrePRReview()
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

// A showKind is a kind of ledger file crew show prints and crew path names: its
// type word, the argument after it, if any, whether it takes --project, how to
// find the file, and how crew show prints it, when not as it is on disk. find
// takes both the argument and --project, and each kind uses what it needs.
type showKind struct {
	word, arg string
	project   bool
	find      func(c *Crew, arg, project string) (string, error)
	show      func(path string) (string, error)
}

// showKinds are the kinds crew show takes. The first, task, is what an argument
// with no type word names, as for every command that takes TASK. A type word wins
// over a task whose ID is that word: crew show task goal reaches the task.
var showKinds = []showKind{
	{word: "task", arg: "TASK", find: ledgerKind("tasks"), show: shownTask},
	{word: "proposal", arg: "PROPOSAL", find: ledgerKind("proposals")},
	{word: "goal", arg: "GOAL", find: ledgerKind("goals")},
	{word: "memory", project: true, find: projectFile("memory.md")},
	{word: "memory-suggestions", project: true, find: projectFile("memory-suggestions.md")},
	{word: "file", arg: "NAME", project: true, find: (*Crew).resolveFile},
}

// ledgerKind finds a task, goal or proposal, as resolve does.
func ledgerKind(kind string) func(*Crew, string, string) (string, error) {
	return func(c *Crew, arg, _ string) (string, error) { return c.resolve(kind, arg) }
}

// projectFile finds the project's file of that name, in the project crew memory
// would pick.
func projectFile(name string) func(*Crew, string, string) (string, error) {
	return func(c *Crew, _, project string) (string, error) {
		p, err := c.oneProject(project)
		if err != nil {
			return "", err
		}
		f := c.path("projects", p, name)
		if !isFile(f) {
			return "", fail("%s has no %s yet", p, name)
		}
		return f, nil
	}
}

// synopsis is how to name a file of this kind to crew show or crew path (cmd).
func (k showKind) synopsis(cmd string) string {
	s := "crew " + cmd + " " + k.word
	if k.arg != "" {
		s += " " + k.arg
	}
	if k.project {
		s += " [--project P]"
	}
	return s
}

// crew show [TYPE] ARG: a ledger file as it is on disk, or a task with its
// records (shownTask).
func cmdShow(c *Crew, args []string) error {
	k, path, err := c.ledgerFile("show", args)
	if err != nil {
		return err
	}
	if k.show != nil {
		out, err := k.show(path)
		fmt.Print(out)
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

// crew path [TYPE] ARG: the path of the file crew show prints.
func cmdPath(c *Crew, args []string) error {
	_, path, err := c.ledgerFile("path", args)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// ledgerFile is the file args name for crew show or crew path (cmd), and its
// kind: a type word from showKinds and what follows it, or a task.
func (c *Crew) ledgerFile(cmd string, args []string) (showKind, string, error) {
	if len(args) == 0 {
		var words []string
		for _, k := range showKinds {
			words = append(words, k.word)
		}
		return showKind{}, "", usage("crew " + cmd + " [" + strings.Join(words, "|") + "] … (see 'crew help')")
	}
	k := showKinds[0] // task
	if i := slices.IndexFunc(showKinds, func(kind showKind) bool { return kind.word == args[0] }); i >= 0 {
		k, args = showKinds[i], args[1:]
	}
	var project string
	flags := map[string]*string{}
	if k.project {
		flags["--project"] = &project
	}
	arg, err := parseText(args, flags, k.synopsis(cmd))
	if err != nil {
		return showKind{}, "", err
	}
	if given, takes := arg != "", k.arg != ""; given != takes {
		return showKind{}, "", usage(k.synopsis(cmd))
	}
	path, err := k.find(c, arg, project)
	return k, path, err
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
	// Checked with the lock held, so the task can't change between the checks
	// and the write: a go-ahead is only for a task that is ready.
	if err := c.lock(); err != nil {
		return err
	}
	d := Doc{f}
	if isProposal(f) {
		err = Proposal{d}.CheckSet(key, value)
	} else {
		value, err = c.checkTaskSet(Task{d}, key, value)
	}
	if err != nil {
		return err
	}
	switch {
	case key == "status" && isProposal(f): // a proposal has no go-ahead to end
		err = d.setStatus(value)
	case key == "status":
		err = Task{d}.SetStatus(Status(value))
	default:
		err = d.Set(key, value)
	}
	if err != nil {
		return err
	}
	// A lead hears the statuses crew's sessions set; crew tick reports its own.
	if key == "status" && !isProposal(f) && inWorkerOrGoalLead() {
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
	if key == "go_ahead" && value != "" {
		return c.checkGoAhead(t, value)
	}
	return value, nil
}

// checkGoAhead is the go_ahead crew set records for a ready task outside a
// goal: value names another task in its project and a status, and is recorded
// with that task's ID. It refuses a wait that could never end: a status the
// other task's kind never reaches, or an abandoned task.
func (c *Crew) checkGoAhead(t Task, value string) (string, error) {
	if g := t.Goal(); g != "" {
		return "", fail("%s is one of goal %s's tasks, which crew tick dispatches while the goal runs; a go-ahead is for a task outside a goal", t.ID(), g)
	}
	if st := t.Status(); st != Ready {
		return "", fail("%s is '%s'; a go-ahead is for a ready task", t.ID(), st)
	}
	arg, st, ok := strings.Cut(value, " ")
	if !ok {
		return "", mustBe("go_ahead", "a task and the status it waits for", value)
	}
	f, matches := resolveIn(arg, c.files("tasks", taskProjectDir(t)))
	if f == "" {
		return "", ambiguousFail(arg, matches, "go_ahead: '%s' doesn't match exactly one task in %s", arg, taskProjectDir(t))
	}
	after := Task{Doc{f}}
	switch {
	case after.Path == t.Path:
		return "", fail("a go-ahead waits on another task, not %s itself", t.ID())
	case after.Status() == Abandoned:
		return "", fail("%s is abandoned, so a go-ahead waiting on it would never be met", after.ID())
	}
	g, err := newGoAhead(after, Status(strings.TrimSpace(st)))
	if err != nil {
		return "", fail("%v", err)
	}
	return g.String(), nil
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
	case "effort":
		ok = slices.Contains(efforts, value)
		takes = orList(efforts)
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
