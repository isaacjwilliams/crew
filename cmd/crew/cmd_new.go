package main

// Making tasks: crew new, crew ready, crew promote.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A branch name the lead picks. It also names the task's ID, its worktree and
// (with Worktrunk) the per-worktree databases, so it's short and filesystem-safe.
var branchName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// crew new "<title>" [--branch NAME] [--on TASK] [--goal GOAL] [--project P]
func cmdNew(c *Crew, args []string) error {
	_, err := c.newTask(args, true)
	return err
}

// newTask makes a draft task, prints its path when show is set, and returns it.
func (c *Crew) newTask(args []string, show bool) (Task, error) {
	var title, proj, name, on, goal string
	for len(args) > 0 {
		flag := map[string]*string{"--project": &proj, "--branch": &name, "--on": &on, "--goal": &goal}[args[0]]
		if flag != nil && len(args) > 1 {
			*flag, args = args[1], args[2:]
			continue
		}
		if title == "" {
			title = args[0]
		} else {
			title += " " + args[0]
		}
		args = args[1:]
	}
	if title == "" {
		return Task{}, usage(`crew new "<title>" [--branch NAME] [--on TASK] [--goal GOAL] [--project P]`)
	}
	if name != "" && (!branchName.MatchString(name) || len(name) > 40) {
		return Task{}, fail("--branch '%s': use a few lowercase words joined by hyphens, at most 40 characters, without the prefix (e.g. fix-invoice-rounding)", name)
	}
	if proj == "" {
		var err error
		if proj, err = c.currentProject(); err != nil {
			return Task{}, err
		}
	}
	p, err := c.loadProject(proj)
	if err != nil {
		return Task{}, err
	}
	if err := c.lock(); err != nil {
		return Task{}, err
	}

	base := p.DefaultBranch()
	var parent Task
	var gid string
	if goal != "" {
		f, matches := resolveIn(goal, c.files("goals", proj))
		if f == "" {
			return Task{}, ambiguousFail(goal, matches, "--goal '%s' doesn't match exactly one goal in %s", goal, proj)
		}
		g := Goal{Doc{f}}
		if st := g.Status(); st != GoalDraft && st != GoalRunning {
			return Task{}, fail("goal %s is '%s'; only a draft or running goal takes new tasks", g.ID(), st)
		}
		gid = g.ID()
	}
	if on != "" {
		f, matches := resolveIn(on, c.files("tasks", proj))
		if f == "" {
			return Task{}, ambiguousFail(on, matches, "--on '%s' doesn't match exactly one task in %s", on, proj)
		}
		parent = Task{Doc{f}}
		if st := parent.Status(); st.Finished() {
			return Task{}, fail("%s is '%s', so it has no open branch to stack on", parent.ID(), st)
		}
		base = parent.Branch()
	}

	n := c.nextNumber("tasks", proj)
	s := name
	if s == "" {
		s = strings.TrimSuffix(cut(slugify(title), 40), "-")
	}
	if s == "" {
		s = fmt.Sprintf("task-%d", n)
	}
	id := fmt.Sprintf("%03d-%s", n, s)
	branch := p.Settings["BRANCH_PREFIX"] + s
	if c.branchTaken(p, branch) {
		branch = fmt.Sprintf("%s-%d", branch, n)
	}
	if !gitOK(".", "check-ref-format", "--branch", branch) {
		return Task{}, fail("'%s' isn't a valid branch name; check BRANCH_PREFIX", branch)
	}
	t := Task{Doc{filepath.Join(p.Dir, "tasks", id+".md")}}
	if err := c.fromTemplate("task.md", t.Path, map[string]string{
		"ID": id, "TITLE": strings.ReplaceAll(title, `"`, "'"), "PROJECT": proj, "BASE": base, "BRANCH": branch,
		"PRE_PR_REVIEW": p.Settings["PRE_PR_REVIEW"], "PR_MODE": p.Settings["PR_MODE"], "NOW": now(),
	}); err != nil {
		return Task{}, err
	}
	msg := "created"
	if parent.Path != "" {
		t.Set("depends_on", parent.ID())
		msg += ", stacked on " + parent.ID()
	}
	if gid != "" {
		t.Set("goal", gid)
		msg += ", for goal " + gid
	}
	t.Log(msg)
	c.commit("new " + proj + "/" + id)
	if show {
		fmt.Println(t.Path)
	}
	return t, nil
}

// ambiguousFail lists the matches of an ambiguous argument, if there are any,
// then fails with the message.
func ambiguousFail(arg string, matches []string, format string, a ...any) error {
	if len(matches) > 1 {
		fmt.Fprintln(os.Stderr, "crew: "+ambiguous(arg, matches))
	}
	return fail(format, a...)
}

// fromTemplate writes templates/<name> to path, with each {{KEY}} replaced.
func (c *Crew) fromTemplate(name, path string, vals map[string]string) error {
	b, err := os.ReadFile(c.path("templates", name))
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(b), "\n")
	for k, v := range vals {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text+"\n"), 0o644)
}

// nextNumber is one more than the highest NNN- among a project's tasks or goals.
func (c *Crew) nextNumber(kind, proj string) int {
	max := 0
	for _, f := range c.files(kind, proj) {
		lead, _, _ := strings.Cut(filepath.Base(f), "-")
		if n, err := strconv.Atoi(lead); err == nil && allDigits.MatchString(lead) && n > max {
			max = n
		}
	}
	return max + 1
}

// branchTaken: the branch is in the repo, on origin, or another task's.
func (c *Crew) branchTaken(p *Project, branch string) bool {
	if gitOK(p.Repo(), "show-ref", "--verify", "--quiet", "refs/heads/"+branch) ||
		gitOK(p.Repo(), "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch) {
		return true
	}
	for _, t := range c.tasks(p.Name) {
		if t.Branch() == branch {
			return true
		}
	}
	return false
}

// crew ready TASK: the brief is complete, so the task can be dispatched.
func cmdReady(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew ready TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	var missing []string
	for _, h := range []string{"## Goal", "## Structure", "## Plan", "## Acceptance criteria"} {
		if t.Section(h) == "" {
			missing = append(missing, strings.TrimPrefix(h, "## "))
		}
	}
	if len(missing) > 0 {
		return fail("brief is incomplete, fill in: %s", strings.Join(missing, " "))
	}
	if st := t.Status(); st != Draft && st != Ready {
		return fail("task is '%s', not draft", st)
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.Set("status", string(Ready))
	t.Touch()
	t.Log("marked ready")
	c.commit("ready " + t.ID())
	fmt.Println("ready: " + t.ID())
	return nil
}

// crew promote PROPOSAL [--branch NAME]: a gardener proposal becomes a draft task.
func cmdPromote(c *Crew, args []string) error {
	const use = "crew promote PROPOSAL [--branch NAME]"
	if len(args) == 0 {
		return usage(use)
	}
	f, err := c.resolve("proposals", args[0])
	if err != nil {
		return err
	}
	pr := Proposal{Doc{f}}
	var branch []string
	for args = args[1:]; len(args) > 0; args = args[2:] {
		if args[0] != "--branch" || len(args) < 2 {
			return usage(use)
		}
		branch = []string{"--branch", args[1]}
	}
	if st := pr.Get("status"); st != "open" && st != "deferred" {
		return fail("proposal is '%s' (task: %s); only open or deferred proposals can be promoted", st, pr.Get("task"))
	}
	proj := pr.Get("project")
	if proj == "" || !isDir(c.path("projects", proj)) {
		proj = filepath.Base(filepath.Dir(filepath.Dir(f)))
	}
	t, err := c.newTask(append([]string{pr.Get("title"), "--project", proj}, branch...), false)
	if err != nil {
		return err
	}
	context := fmt.Sprintf("From gardener proposal `%s` (%s). Run normal intake: confirm the plan with the user.\n\n%s\nFirst step suggested by the gardener:\n%s",
		filepath.Base(f), f, withNewline(pr.Section("## Proposal")), withNewline(pr.Section("## First step")))
	if err := c.lock(); err != nil {
		return err
	}
	t.ReplaceSection("## Context", context)
	pr.Set("status", "accepted")
	pr.Set("task", t.ID())
	pr.Touch()
	pr.Log("promoted to task " + t.ID())
	c.commit("promote " + pr.ID() + " → " + t.ID())
	fmt.Println(t.Path)
	return nil
}

// withNewline is text as a command prints it: each line, the last one too, ends
// in a newline. Text without lines prints nothing.
func withNewline(text string) string {
	if text == "" {
		return ""
	}
	return text + "\n"
}
