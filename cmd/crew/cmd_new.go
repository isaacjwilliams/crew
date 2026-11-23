package main

// Making tasks, and the checked moves of a task's status: crew new, crew ready
// and crew hand-over.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A branch name the lead picks. It also names the task's ID, its worktree and
// (with Worktrunk) the per-worktree databases, so it's short and filesystem-safe.
var branchName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// crew new "<title>" [--kind KIND] [--informs TASK] [--fixes TASK] [--branch NAME] [--on TASK] [--goal GOAL] [--project P]
func cmdNew(c *Crew, args []string) error {
	_, err := c.newTask(args, true)
	return err
}

// newTask makes a draft task, prints its path when show is set, and returns it.
func (c *Crew) newTask(args []string, show bool) (Task, error) {
	const use = `crew new "<title>" [--kind KIND] [--informs TASK] [--fixes TASK] [--branch NAME] [--on TASK] [--goal GOAL] [--project P]`
	var proj, name, on, goal, kindArg, informs, fixes string
	title, err := parseText(args, map[string]*string{"--project": &proj, "--branch": &name, "--on": &on, "--goal": &goal,
		"--kind": &kindArg, "--informs": &informs, "--fixes": &fixes}, use)
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
	links := map[string]string{"informs": informs, "fixes": fixes}
	for key, value := range links {
		if value != "" && key != rules.link {
			return Task{}, fail("a %s task doesn't take --%s", kind, key)
		}
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
		if err := stackRefusal(kind, parent, parent.Kind()); err != nil {
			return Task{}, err
		}
		base = parent.Branch()
	}
	link := links[rules.link]
	if link != "" {
		if link, err = c.resolveLink(proj, rules, link); err != nil {
			return Task{}, err
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
	msg := "created as a " + string(kind)
	if link != "" {
		t.Set(rules.link, link)
		msg += ", " + rules.link + " " + link
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

// resolveLink is what a task of a kind with rules r records as its link: the ID
// of the one task in proj that value names, which must have the status r.linkTo,
// or "unknown" where r takes that. A fix's "12" is recorded as "012-<slug>".
func (c *Crew) resolveLink(proj string, r kindRules, value string) (string, error) {
	takes := fmt.Sprintf("a %s task in %s", r.linkTo, proj)
	if r.linkUnknown {
		takes += ", or unknown"
	}
	if value == "" {
		return "", mustBe(r.link, takes, value)
	}
	if r.linkUnknown && value == "unknown" {
		return value, nil
	}
	f, matches := resolveIn(value, c.files("tasks", proj))
	if f == "" {
		return "", ambiguousFail(value, matches, "%s must be %s; '%s' doesn't match exactly one task", r.link, takes, value)
	}
	linked := Task{Doc{f}}
	if st := linked.Status(); st != r.linkTo {
		return "", fail("%s must be %s; %s is '%s'", r.link, takes, linked.ID(), st)
	}
	return linked.ID(), nil
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
	// Before the sections it misses, which an old brief's rewrite fills in.
	if err := t.DesignInBrief(); err != nil {
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
	if rules.pr && !t.Risk().Valid() {
		return fail("set its risk: crew set %s risk low|medium|high", t.ID())
	}
	for _, key := range rules.keys {
		if _, err := c.checkValue(t.Project(), key, t.Get(key)); err != nil {
			return fail("%v: crew set %s %s <value>", err, t.ID(), key)
		}
	}
	if st := t.Status(); st != Draft && st != Ready {
		return fail("task is '%s', not draft", st)
	}
	if err := c.lock(); err != nil {
		return err
	}
	t.SetStatus(Ready)
	t.Touch()
	c.commit("ready " + t.ID())
	fmt.Println("ready: " + t.ID())
	return nil
}

// crew hand-over TASK: the worker's work is ready for whoever reads it next.
// The task's kind says what that needs (handOverPR, handOverReport) and which
// status it moves to. It's the only way into pr-open (crew set refuses it), so
// every return of a PR to the user, after a review round or a restack, checks
// the branch as it is then.
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
	// Read what the checks need from outside the ledger (git, GitHub) first, so
	// a slow network doesn't hold up other crew commands; check it under the lock.
	check, err := rules.handOver(c, t)
	if err != nil {
		return err
	}
	// Check under the lock: crew sync may be marking the task merged, or the
	// lead abandoning it, at the same moment.
	if err := c.lock(); err != nil {
		return err
	}
	if st := t.Status(); !st.InFlight() || st == PROpen {
		return fail("task is '%s': only a task in flight, and not in pr-open, can be handed over", st)
	}
	if err := check(); err != nil {
		return err
	}
	t.SetStatus(rules.handedOver)
	t.Touch()
	t.Log("handed over")
	if inWorkerOrGoalLead() {
		c.recordNews(taskProjectDir(t), fmt.Sprintf("%s: handed over (%s)", t.ID(), rules.handedOver))
	}
	c.commit("hand over " + t.ID())
	fmt.Printf("handed over: %s (%s)\n", t.ID(), rules.handedOver)
	return nil
}

// handOverPR checks a build is ready for the user: the evidence they read, in
// the task and the PR body, shows every acceptance criterion holding on the
// code they would merge (checkEvidence), a high-risk task has a security
// review (which a branch that touches a sensitive path needs), and automated
// review has run, while it's on for the task. It reads the branch and the PR
// body first, before crew hand-over takes the lock, so a slow network doesn't
// hold up other crew commands.
func handOverPR(c *Crew, t Task) (func() error, error) {
	p, err := c.loadProject(t.Project())
	if err != nil {
		return nil, err
	}
	// The PR's branch on origin may have commits from elsewhere, a suggestion
	// applied on GitHub say, that the evidence and raiseRisk have to see.
	if !gitOK(p.Repo(), "fetch", "--quiet", "origin") {
		return nil, fail("couldn't check the branch as origin has it: git fetch origin failed in %s", p.Repo())
	}
	var pr Evidence // nil without a PR: with pr_mode none, the worker opens none
	if t.PR() != "" {
		body, err := gh(p.Repo(), "pr", "view", t.PR(), "--json", "body", "--jq", ".body")
		if err != nil {
			return nil, fail("couldn't read the PR body: gh pr view %s failed", t.PR())
		}
		pr = PREvidence(string(body))
	}
	return func() error {
		if t.PR() == "" && t.Get("pr_mode") != "none" {
			return fail("task has no PR: record it with crew set %s pr <url>", t.ID())
		}
		if err := checkEvidence(t, p.Repo(), pr); err != nil {
			return err
		}
		// A raise stands even when the hand-over is refused below: the branch
		// touches a sensitive path whatever happens next.
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
		if t.AwaitsAutomatedReview(p.Settings["AUTO_REVIEWS"]) {
			return fail("automated review hasn't run for this task: run a pass of automated review, then hand over again")
		}
		return nil
	}, nil
}

// checkEvidence refuses evidence that can't reach the user. In the task's
// ## Evidence or the PR body (pr, nil without a PR), it refuses:
//   - an acceptance criterion with no item;
//   - an item that doesn't name the commit it ran on;
//   - one that says its check wasn't run, unless it's a user check (Task.UserChecks);
//   - one that ran on other work than the branch has now. A restack, or a merge
//     of the base that git made on its own, keeps evidence current
//     (branchWork.unchangedSince).
func checkEvidence(t Task, repo string, pr Evidence) error {
	n := len(t.AcceptanceCriteria())
	if n == 0 {
		return fail("## Acceptance criteria has no '- ' items to show evidence for: ask for them to be written that way")
	}
	task := t.Evidence().Within(n)
	pr = pr.Within(n)
	if gaps := task.Gaps(n); len(gaps) > 0 {
		return fail("evidence missing for acceptance criteria %s of %d: add a '### <n>. … (on <sha>)' entry for each under ## Evidence (just before ## Automated reviews), with the command and its output",
			numberList(gaps), n)
	}
	if unnamed := task.Unnamed(); len(unnamed) > 0 {
		return fail("evidence for acceptance criteria %s doesn't name the commit it ran on: end each entry's heading with '(on <sha>)'", numberList(unnamed))
	}
	if pr != nil {
		if gaps := pr.Gaps(n); len(gaps) > 0 {
			return fail("the PR body's Evidence has no item for acceptance criteria %s of %d: add a '<n>. … (on <sha>)' item for each, as templates/pr-body.md has them, with gh pr edit",
				numberList(gaps), n)
		}
		if unnamed := pr.Unnamed(); len(unnamed) > 0 {
			return fail("the PR body's Evidence for acceptance criteria %s doesn't name the commit it ran on: put '(on <sha>)' after each item's criterion, with gh pr edit", numberList(unnamed))
		}
	}
	userChecks := t.UserChecks()
	if notRun := evidencePlaces(task.NotRun(userChecks), pr.NotRun(userChecks)); notRun != "" {
		return fail("evidence says its check wasn't run, in %s. Run each check the ways the project's AGENTS.md, docs and memory give; if none works, ask under ## Open questions, with what you tried, and set status needs-input. Only a user check may be left to the user: one the brief starts 'User check:', or a Decision of the user's makes theirs", notRun)
	}
	work, err := ownWork(t, repo)
	if err != nil {
		return fail("couldn't check the evidence: %v", err)
	}
	// A commit the evidence names is stale when its work isn't the branch's at
	// either tip: the user may merge either one. When the branch here has the
	// work and only origin lacks it, a push is all that's missing.
	stale := map[string]bool{}
	staleHere := false
	for _, e := range []Evidence{task, pr} {
		for _, item := range e {
			sha := item.Commit
			if _, checked := stale[sha]; checked {
				continue
			}
			then, err := workIn(t, repo, sha)
			if err != nil {
				return fail("couldn't check the evidence: %v", err)
			}
			stale[sha] = false
			for _, w := range work {
				unchanged, err := w.unchangedSince(repo, then)
				if err != nil {
					return fail("couldn't check the evidence: %v", err)
				}
				if !unchanged {
					stale[sha] = true
					staleHere = staleHere || w.here()
				}
			}
		}
	}
	places := evidencePlaces(task.Naming(stale), pr.Naming(stale))
	if places != "" && !staleHere && slices.ContainsFunc(work, branchWork.here) {
		return fail("evidence ran on work the branch on origin doesn't have yet, in %s. Push the branch, then hand over again", places)
	}
	if places != "" {
		return fail("evidence ran on other work than the branch has now, here and on origin, in %s. Push the branch, run the evidence again, and record it with the commit it ran on", places)
	}
	return nil
}

// evidencePlaces says where the items a check found are, in the task's
// ## Evidence and in the PR body, or "" when it found none.
func evidencePlaces(taskItems, prItems string) string {
	var places []string
	if taskItems != "" {
		places = append(places, "## Evidence: "+taskItems)
	}
	if prItems != "" {
		places = append(places, "the PR body: "+prItems)
	}
	return strings.Join(places, "; ")
}

// numberList is the numbers ns, comma-separated.
func numberList(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// handOverReport checks a spike's report is in, so whoever reads it next finds
// an answer, and that its worktree is clean, so crew cleanup can remove it.
func handOverReport(_ *Crew, t Task) (func() error, error) {
	return func() error {
		if t.Answer() == "" {
			return fail("## Report must start with a line 'Answer: <the answer, in a line>'; write the report, then hand over again")
		}
		if wt := t.Worktree(); wt != "" && isDir(wt) {
			if out, _ := git(wt, "status", "--porcelain"); out != "" {
				return fail("%s has changes that aren't committed: commit or delete them, so it can be cleaned up, then hand over again", wt)
			}
		}
		return nil
	}, nil
}
