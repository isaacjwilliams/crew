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
// review has run, while it's on for the task. At the first hand-over, the
// branch and the PR are also where the task belongs (refuseOffBase), and the
// automated review read the branch's own commits as they are now
// (staleReview). It reads the branch and the PR first, before crew hand-over
// takes the lock, so a slow network doesn't hold up other crew commands.
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
	first := !t.HandedOver()
	var onGitHub *gitHubPR // the PR as GitHub has it now, which a first hand-over places
	if first && t.PR() != "" {
		read, err := readPR(p.Repo(), t.PR())
		if err != nil {
			return nil, fail("couldn't read the PR: gh pr view %s failed", t.PR())
		}
		onGitHub = &read
	}
	return func() error {
		// Before the PR check: a worker whose parent merged can't open its PR on
		// the parent's branch once GitHub has deleted it.
		if first {
			if err := c.refuseOffBase(p, t, onGitHub); err != nil {
				return err
			}
		}
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
		// Any rebase before the first hand-over, onto a parent that moved or onto
		// a base the branch conflicted with, leaves the last review on commits the
		// PR no longer holds. Later hand-overs skip this: a review round's commits
		// aren't automated review's to read, and a restack changes no work of the
		// task's own.
		if first && atoiOr0(t.AutoReviews(p.Settings["AUTO_REVIEWS"])) > 0 {
			if why := staleReview(t, p.Repo()); why != "" {
				return fail("automated review %d no longer covers your branch: %s. Run a pass of automated review, then hand over again", t.NewestReview(), why)
			}
		}
		return nil
	}, nil
}

// refuseOffBase refuses a task's first hand-over while its branch or its PR
// isn't where the task belongs now, and tells the worker how to move them, as
// crew restack would (rebaseFor, afterPush). A stacked task's branch belongs on
// its parent's latest commit, or on what the parent merged into, and every
// task's PR targets its base. Before the first hand-over the worker's own
// session is still making the branch, so it moves them itself; after it, crew
// tick restacks the task. onGitHub is the task's PR as GitHub has it now, or
// nil when it has none. Call it with the lock held.
func (c *Crew) refuseOffBase(p *Project, t Task, onGitHub *gitHubPR) error {
	idx, repo := c.branches(p.Name), p.Repo()
	parent, stacked := idx.parentOf(t)
	// crew sync may not have looked since the worker opened or moved its PR, or
	// since an earlier refusal's rebase moved the branch off base_sha.
	changed := onGitHub != nil && onGitHub.record(t)
	if stacked && noteBaseSHA(t, parent, repo) {
		changed = true
	}
	if changed {
		c.commit("sync " + p.Name + "/" + t.ID())
	}
	if stacked && parent.Status() == Abandoned {
		return fail("%s, the task your branch is stacked on, was abandoned, so your branch has nowhere to move before its first hand-over: ask what its base should be", parent.ID())
	}
	retarget := idx.retargetReason(t, repo)
	if retarget != "" && idx.onBase(t, repo) {
		return fail("your PR has to move before its first hand-over (%s): run `gh pr edit --base %s`, then hand over again", retarget, t.Base())
	}
	if !stacked {
		if retarget != "" {
			return fail("your PR has to move before its first hand-over (%s), but your branch doesn't start on %s, so the PR would bring other commits there: ask what to do", retarget, t.Base())
		}
		return nil
	}
	r, err := idx.rebaseFor(t, parent, repo)
	if err != nil || r.reason == "" {
		return err
	}
	then, _ := r.afterPush(t, retarget)
	return fail("your branch has to move before its first hand-over (%s): %s%s", r.reason, r.steps(`follow "Stacked tasks" in your instructions`), then)
}

// staleReview says why the task's newest automated review didn't read its
// branch's own commits as they are now, or nothing: it read from another fork
// point than the branch has on its base now, or up to a commit the branch no
// longer holds. A rebase after the review does both. A review whose range crew
// can't read, or a branch with no fork point on origin's base, isn't judged.
func staleReview(t Task, repo string) string {
	reviews := t.AutomatedReviews()
	if len(reviews) == 0 {
		return ""
	}
	r, branch := reviews[len(reviews)-1], "refs/heads/"+t.Branch()
	if r.Fork == "" {
		return ""
	}
	fork, _ := git(repo, "merge-base", "refs/remotes/origin/"+t.Base(), branch)
	if fork == "" {
		return ""
	}
	if read, _ := git(repo, "rev-parse", "--verify", "--quiet", r.Fork+"^{commit}"); read == "" || read != fork {
		return fmt.Sprintf("it read from %s, and your branch now leaves origin/%s at %s", cut(r.Fork, 12), t.Base(), cut(fork, 12))
	}
	if !gitOK(repo, "merge-base", "--is-ancestor", r.Head, branch) {
		return fmt.Sprintf("your branch no longer holds %s, where it ended", cut(r.Head, 12))
	}
	return ""
}

// checkEvidence refuses evidence that can't reach the user: an acceptance
// criterion with no item in the task's ## Evidence or in the PR body (pr, nil
// without a PR), an item that doesn't name the commit it ran on, or one that
// ran on other work than the branch has now. A restack or a merge of the base
// that git made on its own keeps evidence current (branchWork.unchangedSince).
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
		for _, sha := range e {
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
	var places []string
	if items := task.Naming(stale); items != "" {
		places = append(places, "## Evidence: "+items)
	}
	if items := pr.Naming(stale); items != "" {
		places = append(places, "the PR body: "+items)
	}
	if len(places) > 0 && !staleHere && slices.ContainsFunc(work, branchWork.here) {
		return fail("evidence ran on work the branch on origin doesn't have yet, in %s. Push the branch, then hand over again", strings.Join(places, "; "))
	}
	if len(places) > 0 {
		return fail("evidence ran on other work than the branch has now, here and on origin, in %s. Push the branch, run the evidence again, and record it with the commit it ran on", strings.Join(places, "; "))
	}
	return nil
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
