package main

// Making tasks, and the checked moves of a task's status: crew new, crew ready
// and crew hand-over.

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

// crew new "<title>" [--kind KIND] [--informs TASK] [--branch NAME] [--on TASK] [--goal GOAL] [--project P]
func cmdNew(c *Crew, args []string) error {
	_, err := c.newTask(args, true)
	return err
}

// newTask makes a draft task, prints its path when show is set, and returns it.
func (c *Crew) newTask(args []string, show bool) (Task, error) {
	const use = `crew new "<title>" [--kind KIND] [--informs TASK] [--branch NAME] [--on TASK] [--goal GOAL] [--project P]`
	var proj, name, on, goal, kindArg, informs string
	title, err := parseText(args, map[string]*string{"--project": &proj, "--branch": &name, "--on": &on, "--goal": &goal,
		"--kind": &kindArg, "--informs": &informs}, use)
	if err != nil {
		return Task{}, err
	}
	if title == "" {
		return Task{}, usage(use)
	}
	kind := KindBuild
	if kindArg != "" {
		kind = Kind(kindArg)
	}
	rules, ok := kind.rules()
	if !ok {
		return Task{}, fail("--kind must be %s, not '%s'", kindNames(), kindArg)
	}
	if informs != "" && rules.link != "informs" {
		return Task{}, fail("a %s task doesn't take --informs", kind)
	}
	if name != "" && (!branchName.MatchString(name) || len(name) > 40) {
		return Task{}, fail("--branch '%s': use a few lowercase words joined by hyphens, at most 40 characters, without the prefix (e.g. fix-invoice-rounding)", name)
	}
	if proj, err = c.oneProject(proj); err != nil {
		return Task{}, err
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
	var linked Task
	if informs != "" {
		f, matches := resolveIn(informs, c.files("tasks", proj))
		if f == "" {
			return Task{}, ambiguousFail(informs, matches, "--informs '%s' doesn't match exactly one task in %s", informs, proj)
		}
		linked = Task{Doc{f}}
		if st := linked.Status(); st != rules.linkTo {
			return Task{}, fail("%s is '%s'; --informs takes a %s task", linked.ID(), st, rules.linkTo)
		}
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
		"ID": id, "TITLE": strings.ReplaceAll(title, `"`, "'"), "PROJECT": proj, "KIND": string(kind), "BASE": base, "BRANCH": branch,
		"PRE_PR_REVIEW": p.Settings["PRE_PR_REVIEW"], "PR_MODE": p.Settings["PR_MODE"], "NOW": now(),
	}); err != nil {
		return Task{}, err
	}
	msg := "created"
	if kind != KindBuild {
		msg += " as a " + string(kind)
	}
	if linked.Path != "" {
		t.Set(rules.link, linked.ID())
		msg += ", " + rules.link + " " + linked.ID()
	}
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
		if n, ok := fileNumber(f); ok && n > max {
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
	rules, err := t.rules()
	if err != nil {
		return fail("%v", err)
	}
	var missing []string
	for _, h := range rules.brief {
		if t.Section(h) == "" {
			missing = append(missing, strings.TrimPrefix(h, "## "))
		}
	}
	if len(missing) > 0 {
		return fail("brief is incomplete, fill in: %s", strings.Join(missing, " "))
	}
	// A build's worker shows evidence for each criterion by its number, so each
	// is a "- " item, with any detail indented under it. Every kind's brief is
	// written the same way.
	for _, l := range strings.Split(t.Section("## Acceptance criteria"), "\n") {
		if !strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			return fail("write each acceptance criterion as a '- ' item, any detail indented under it; this line isn't one: %s", l)
		}
	}
	if n := t.StillToLearn(); n > 0 {
		return fail("%d item(s) under Still to learn are open: answer each, with a spike where it needs trying (crew new --kind spike --informs %s), then tick it", n, t.ID())
	}
	if rules.risk && !t.Risk().Valid() {
		return fail("set its risk: crew set %s risk low|medium|high", t.ID())
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

// crew hand-over TASK: the worker's work is ready for whoever reads it next.
// The task's kind says what that needs (handOverPR, handOverReport) and which
// status it moves to. Workers run it once, at the end of their first run; a
// review round may have started by then. Their later moves back to pr-open are
// plain crew set, since the PR was handed over already.
func cmdHandOver(c *Crew, args []string) error {
	if len(args) == 0 {
		return usage("crew hand-over TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	rules, err := t.rules()
	if err != nil {
		return fail("%v", err)
	}
	// Check under the lock: crew sync may be marking the task merged, or the
	// lead abandoning it, at the same moment.
	if err := c.lock(); err != nil {
		return err
	}
	if st := t.Status(); !st.InFlight() || st == PROpen {
		return fail("task is '%s': only a task in flight that isn't pr-open yet can be handed over", st)
	}
	if err := rules.handOver(c, t); err != nil {
		return err
	}
	t.Set("status", string(rules.handedOver))
	t.Touch()
	t.Log("handed over")
	c.commit("hand over " + t.ID())
	fmt.Printf("handed over: %s (%s)\n", t.ID(), rules.handedOver)
	return nil
}

// handOverPR checks a build is ready for the user: its PR has evidence for every
// acceptance criterion, and a security review if it's high risk, which a branch
// that touches a sensitive path makes it. Call it with the lock held.
func handOverPR(c *Crew, t Task) error {
	if t.PR() == "" && t.Get("pr_mode") != "none" { // with pr_mode none, the worker opens no PR
		return fail("task has no PR: record it with crew set %s pr <url>", t.ID())
	}
	criteria := t.AcceptanceCriteria()
	if len(criteria) == 0 {
		return fail("## Acceptance criteria has no '- ' items to show evidence for: ask for them to be written that way")
	}
	if gaps := t.EvidenceGaps(); len(gaps) > 0 {
		nums := make([]string, len(gaps))
		for i, n := range gaps {
			nums[i] = strconv.Itoa(n)
		}
		return fail("evidence missing for acceptance criteria %s of %d: add a '### <n>. …' entry for each under ## Evidence (just before ## Automated reviews), with the command and its output",
			strings.Join(nums, ", "), len(criteria))
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	// The branch on origin may have commits from elsewhere, a suggestion applied
	// on GitHub say, that raiseRisk has to see. crew sync fetches under the lock
	// too.
	if strings.TrimSpace(p.Settings["SENSITIVE_PATHS"]) != "" && !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
		return fail("couldn't check SENSITIVE_PATHS: git fetch origin failed in %s", p.Repo())
	}
	// A raise stands even when the hand-over is refused below: the branch touches
	// a sensitive path whatever happens next.
	raised, err := raiseRisk(t, p)
	if err != nil {
		return fail("%v", err)
	}
	if raised != "" {
		fmt.Println(raised)
		c.commit("raise risk " + t.ID())
	}
	if t.AwaitsSecurityReview() {
		return fail("high risk: run /security-review, act on its findings, record them under ## Evidence as '%s', then hand over again", securityReview)
	}
	return nil
}

// handOverReport checks a spike's report is in, so whoever reads it next finds
// an answer.
func handOverReport(_ *Crew, t Task) error {
	if t.Answer() == "" {
		return fail("## Report must start with a line 'Answer: <the answer, in a line>'; write the report, then hand over again")
	}
	return nil
}
