package main

// crew review-eval: reruns the reviewer on what automated review passed, where
// the user's comment or a later fix then found something it missed, and
// reports which of those misses the reviewer finds now. The user runs it after
// changing the rubric, the reviewer's prompt or its model.

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const reviewEvalUsage = "crew review-eval [--project P] [--task T] [--limit N] [--rubric PATH]"

// crew review-eval [--project P] [--task T] [--limit N] [--rubric PATH]: every
// case of the project's test set, or only T's, or the first N. Each costs a
// review and a judgement, so crew never runs it on its own.
func cmdReviewEval(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	proj, task, limit := "", "", 0
	rubric := c.path(".claude/skills/pr-review/rubric.md")
	for len(args) > 1 { // every flag takes a value: one left alone is a usage error, below
		switch args[0] {
		case "--project":
			proj = args[1]
		case "--task":
			task = args[1]
		case "--limit":
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				return fail("--limit takes a number of cases, not '%s'", args[1])
			}
			limit = n
		case "--rubric":
			abs, err := filepath.Abs(args[1])
			if err != nil || !isFile(abs) {
				return fail("--rubric: no file at %s", args[1])
			}
			rubric = abs
		default:
			return usage(reviewEvalUsage)
		}
		args = args[2:]
	}
	if len(args) > 0 {
		return usage(reviewEvalUsage)
	}
	if task != "" && proj == "" {
		t, err := c.resolveTask(task) // only to learn its project
		if err != nil {
			return err
		}
		proj = t.Project()
	}
	proj, err := c.oneProject(proj)
	if err != nil {
		return err
	}
	p, err := c.loadProject(proj)
	if err != nil {
		return err
	}
	if err := refuseIfPaused(p); err != nil {
		return err
	}

	cases := evalCases(c.tasks(p.Name))
	var selection []string // the flags that narrowed the run, for its report
	if task != "" {
		f, matches := resolveIn(task, c.files("tasks", p.Name))
		if f == "" {
			return ambiguousFail(task, matches, "--task '%s' doesn't match exactly one task in %s", task, p.Name)
		}
		var ofTask []evalCase
		for _, ec := range cases {
			if sameFile(ec.task.Path, f) {
				ofTask = append(ofTask, ec)
			}
		}
		cases, selection = ofTask, append(selection, "--task "+Task{Doc{f}}.ID())
	}
	if limit > 0 && len(cases) > limit {
		cases, selection = cases[:limit], append(selection, "--limit "+strconv.Itoa(limit))
	}
	if len(cases) == 0 {
		fmt.Printf("%s: no cases yet: none of its merged tasks has a comment of yours after an automated review, or a fix naming it\n", p.Name)
		return nil
	}

	// One run of a project at a time: they'd move each other's checkout.
	lock, err := flock(c.path(".crew-eval-"+p.Name+".lock"), 0)
	if errors.Is(err, errLocked) {
		return fail("a review eval of %s is running already", p.Name)
	} else if err != nil {
		return err
	}
	defer lock.Close()
	run, err := startEvalRun(c, p, rubric, strings.Join(selection, " "))
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d case(s); briefings, reviews and judgements go in %s\n", p.Name, len(cases), run.logs)
	return run.record(run.all(cases))
}

// A case of the review eval: something the user found on a merged task's PR
// after automated review had passed it, and the review that missed it.
type evalCase struct {
	// id names the case the same way in every run: <task>/round-<n>/comment-<id>
	// (or review-<id>, for a review summary) for a comment, <task>/fix-<fix task>
	// for a fix.
	id     string
	task   Task
	review AutomatedReview // the review that missed it: the case reruns its Fork..Head
	miss   string          // what the user found, as the judge reads it
}

// evalCases is the review eval's test set, derived from the ledger. For each
// merged task, in the order given: every comment the user made after an
// automated review, then every fix task naming it, against its last review. A
// case reruns its review's range, so a review whose Range line crew can't read
// gives none. A fix that was abandoned may not have been a defect, so it gives
// none either.
func evalCases(tasks []Task) []evalCase {
	fixes := map[string][]Task{}
	for _, f := range tasks {
		if f.Kind() == KindFix && f.Status() != Abandoned {
			fixes[f.Fixes()] = append(fixes[f.Fixes()], f)
		}
	}
	var cases []evalCase
	for _, t := range tasks {
		if t.Status() != Merged {
			continue
		}
		for _, cm := range t.CommentsAfterReview() {
			if cm.Review.Head != "" {
				id := fmt.Sprintf("%s/round-%d/%s", t.ID(), cm.Round, caseCommentID(cm.Comment.Item))
				cases = append(cases, evalCase{id, t, cm.Review, commentMiss(cm.Comment)})
			}
		}
		if last, ok := lastReviewedRange(t); ok {
			for _, f := range fixes[t.ID()] {
				miss := f.Get("title") + "\n\n" + f.Section("## Goal")
				cases = append(cases, evalCase{t.ID() + "/fix-" + f.ID(), t, last, miss})
			}
		}
	}
	return cases
}

// caseCommentID names a comment by its kind and GitHub ID, from its item in a
// review round: "review 7 (changes_requested)" is review-7, and "comment 10 —
// …" and "`a.go:3` — comment 9 — …" are comment-10 and comment-9.
func caseCommentID(item string) string {
	if rest, ok := strings.CutPrefix(item, "review "); ok {
		n, _, _ := strings.Cut(rest, " ")
		return "review-" + n
	}
	rest, ok := strings.CutPrefix(item, "comment ")
	if !ok {
		_, rest, _ = strings.Cut(item, " — comment ")
	}
	n, _, _ := strings.Cut(rest, " ")
	return "comment-" + n
}

// commentMiss is a comment as the judge reads it: its text, after the place
// it's about for an inline comment ("On `a.go:3`: why?"), since a short inline
// comment means little without it.
func commentMiss(cm RoundComment) string {
	if where, _, ok := strings.Cut(cm.Item, " — comment "); ok && !strings.HasPrefix(cm.Item, "comment ") {
		return "On " + where + ": " + cm.Text
	}
	return cm.Text
}

// lastReviewedRange is the task's newest automated review with a range crew
// can read.
func lastReviewedRange(t Task) (AutomatedReview, bool) {
	reviews := t.AutomatedReviews()
	for i := len(reviews) - 1; i >= 0; i-- {
		if reviews[i].Head != "" {
			return reviews[i], true
		}
	}
	return AutomatedReview{}, false
}

// briefingHeadings are the headings of the pr-review skill's briefing, in the
// order its SKILL.md step 3 gives them; tests/guard.sh checks they match.
var briefingHeadings = []string{"## Scope", "## How to verify", "## Hard constraints", "## Stated intent"}

// evalBriefing is the briefing for rerunning review r of task t in root: the
// skill's first line and headings, holding only what existed when r ran. Its
// stated intent is the user's request, their decisions dated before r's day
// (a decision has no time) and their comments in rounds before r, then the
// commits, as git log prints them. Nothing later goes in, since it could hold
// the miss itself. The PR's text isn't recorded as it was then, so it isn't in
// either.
func evalBriefing(t Task, r AutomatedReview, root string, count int, commits string) string {
	scope := fmt.Sprintf("- The review root: `%s`\n"+
		"- The change: `%s..HEAD`, %d commits from fork point `%s` on `%s` to HEAD `%s`.\n"+
		"- The new commits: this review reads the whole PR, so they are the change.\n"+
		"- The working tree is clean, so only the commits are in scope.",
		root, r.Fork, count, r.Fork, t.Base(), r.Head)
	verify := "The repo's AGENTS.md (or CLAUDE.md), at the review root, names the test, lint and run commands."
	stated := append(userWordsBefore(t, r), "### Commits\n\n"+demoteHeadings(commits))

	var b strings.Builder
	b.WriteString("Code review\n")
	for i, body := range []string{scope, verify, "None recorded.", strings.Join(stated, "\n\n")} {
		fmt.Fprintf(&b, "\n%s\n\n%s\n", briefingHeadings[i], body)
	}
	return b.String()
}

// userWordsBefore is what the user had said on the task when review r ran,
// as the briefing's subsections: their request (the Goal's quote), their
// decisions dated before r's day, and their comments in rounds before r.
func userWordsBefore(t Task, r AutomatedReview) []string {
	var stated []string
	if request := quotedLines(t.Section("## Goal")); request != "" {
		stated = append(stated, "### The user's request\n\n"+request)
	}
	if decisions := userDecisionsBefore(t, r.At); len(decisions) > 0 {
		stated = append(stated, "### The user's decisions\n\n"+strings.Join(decisions, "\n"))
	}
	var rounds []string
	for _, round := range t.ReviewRounds() {
		if round.At.IsZero() || !round.At.Before(r.At) || len(round.Comments) == 0 {
			continue
		}
		var quotes []string
		for _, cm := range round.Comments {
			quotes = append(quotes, "> "+strings.ReplaceAll(cm.Text, "\n", "\n> "))
		}
		rounds = append(rounds, fmt.Sprintf("#### Round %d\n\n%s", round.Number, strings.Join(quotes, "\n\n")))
	}
	if len(rounds) > 0 {
		stated = append(stated, "### The user's review comments\n\n"+strings.Join(rounds, "\n\n"))
	}
	return stated
}

// briefSections are the parts of a task's brief that can settle a point, as
// agents/misses.md reads a brief, besides the Goal and the decisions.
var briefSections = []string{"Structure", "Plan", "Constraints and conventions", "Out of scope"}

// briefBefore is what the judge needs to tell a miss from a point the brief
// settled or a change of mind: the brief's sections, then the user's words
// before review r. A brief is written before the work starts, so the task
// file's is the one r had. The reviewer's briefing leaves these sections out,
// as the pr-review skill's does: they're the plan's reasoning, not the user's
// intent.
func briefBefore(t Task, r AutomatedReview) []string {
	var brief []string
	for _, name := range briefSections {
		if text := strings.TrimSpace(t.Section("## " + name)); text != "" {
			brief = append(brief, "### "+name+"\n\n"+text)
		}
	}
	return append(brief, userWordsBefore(t, r)...)
}

// quotedLines is the lines of text that are a quote, "> …".
func quotedLines(text string) string {
	var quote []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, ">") {
			quote = append(quote, l)
		}
	}
	return strings.Join(quote, "\n")
}

// userDecisionsBefore is the task's decisions that are the user's own, the
// lines ending "(user)", dated before the day of at. A decision starts with
// its date, "- 2026-11-14 — …", and most have no time, so one made on at's day
// may have come after it: only earlier days are sure.
func userDecisionsBefore(t Task, at time.Time) []string {
	y, m, d := at.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, at.Location())
	var lines []string
	for _, l := range strings.Split(t.Section("## Decisions"), "\n") {
		item, ok := strings.CutPrefix(l, "- ")
		if !ok || !strings.HasSuffix(strings.TrimRight(l, " "), "(user)") {
			continue
		}
		stamp, _, _ := strings.Cut(item, " ")
		if date, err := time.ParseInLocation("2006-01-02", stamp, at.Location()); err == nil && date.Before(day) {
			lines = append(lines, l)
		}
	}
	return lines
}

// demoteHeadings turns each "#" or "##" heading in text into a "####" one, so
// a commit message can't start a section of the briefing.
func demoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		for _, h := range []string{"# ", "## "} {
			if rest, ok := strings.CutPrefix(l, h); ok {
				lines[i] = "#### " + rest
			}
		}
	}
	return strings.Join(lines, "\n")
}

// What a review eval tested with: the rubric and the reviewer's prompt, each by
// path and a short hash of its text, and Claude Code's version.
type evalSetup struct {
	rubric, rubricHash     string
	reviewer, reviewerHash string
	claude                 string
	// selection is how the run came to judge only some cases: the flags that
	// narrowed it ("--task 042 --limit 2", say) and why it was cut short, if it
	// was. It's "" for a full run.
	selection string
}

// shortHash is the start of the SHA-256 of the file's content, or "missing".
func shortHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// One run of crew review-eval over a project: when it started, what it tests
// with, its checkout, the directory for what each session was given and said,
// and why it stopped starting sessions, if it has.
type evalRun struct {
	c        *Crew
	p        *Project
	started  time.Time
	tested   evalSetup
	checkout string
	logs     string
	stopped  string
}

// startEvalRun gets a run over p ready: it notes what the run tests with, and
// makes its directory and checkout.
func startEvalRun(c *Crew, p *Project, rubric, selection string) (*evalRun, error) {
	now := time.Now()
	reviewer := filepath.Join(installedAgents(), "reviewer.md")
	version := "unknown"
	if v, ok := claudeVersion(); ok && v != "" {
		version = v
	}
	e := &evalRun{
		c:        c,
		p:        p,
		started:  now,
		tested:   evalSetup{rubric, shortHash(rubric), reviewer, shortHash(reviewer), version, selection},
		checkout: filepath.Join(p.WorkRoot(), p.Name, "_eval"),
		logs:     c.path("logs", "review-eval-"+p.Name+"-"+now.Format("20060102-150405")),
	}
	if err := os.MkdirAll(e.logs, 0o755); err != nil {
		return nil, err
	}
	return e, p.detachedCheckout(e.checkout)
}

// record writes the run's report into the project's review-eval directory,
// compared with the newest full run's there, and commits it.
func (e *evalRun) record(results []evalResult) error {
	dir := e.c.path("projects", e.p.Name, "review-eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tested := e.tested
	if e.stopped != "" {
		tested.selection = cmp.Or(tested.selection, "all") + ", cut short: " + e.stopped
	}
	comparison := ""
	if prev, setup, verdicts, ok := lastFullEval(dir); ok {
		comparison = compareEvals(filepath.Base(prev), setup, verdicts, tested, results)
	}
	file := filepath.Join(dir, e.started.Format("2006-01-02-150405")+".md")
	text := evalReport(e.p.Name, e.started, tested, e.logs, results, comparison)
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %s Report: %s\n", e.p.Name, evalTotals(results), file)
	if comparison != "" {
		fmt.Println(comparison)
	}
	if err := e.c.lock(); err != nil {
		return err
	}
	e.c.commit(fmt.Sprintf("review eval %s (%d cases)", e.p.Name, len(results)))
	e.c.unlock()
	return nil
}

// A verdict on a case: did the reviewer's report raise the miss?
type verdict string

const (
	verdictFound       verdict = "found"
	verdictMissed      verdict = "missed"
	verdictNotAFinding verdict = "not a review finding"
	verdictNotRun      verdict = "not run" // the review or the judge failed
)

// What a case came to: its verdict, and the finding the judge quoted for it,
// or why the case didn't run.
type evalResult struct {
	evalCase
	verdict verdict
	detail  string
}

// all runs the cases one at a time. Cases on the same review share one rerun
// of it: the reviewer gets the same briefing for each.
func (e *evalRun) all(cases []evalCase) []evalResult {
	type review struct {
		report string
		err    error
	}
	reviews := map[string]review{}
	var results []evalResult
	for i, ec := range cases {
		fmt.Printf("%s: case %d of %d, %s: ", e.p.Name, i+1, len(cases), ec.id)
		if !e.goOn() {
			fmt.Println(verdictNotRun)
			results = append(results, evalResult{ec, verdictNotRun, e.stopped})
			continue
		}
		key := fmt.Sprintf("%s-review-%d", ec.task.ID(), ec.review.Number)
		rv, done := reviews[key]
		if !done {
			rv.report, rv.err = e.rerun(ec.task, ec.review, filepath.Join(e.logs, key))
			reviews[key] = rv
		}
		res := evalResult{evalCase: ec, verdict: verdictNotRun}
		if rv.err != nil {
			res.detail = rv.err.Error()
		} else if v, finding, err := e.judge(ec, rv.report); err != nil {
			res.detail = err.Error()
		} else {
			res.verdict, res.detail = v, finding
		}
		fmt.Println(res.verdict)
		results = append(results, res)
	}
	return results
}

// goOn says whether the run may start another session. Once the project is
// paused, or the rubric or the reviewer's prompt has changed since the run
// started, it stops for good: its report would name what it no longer tests.
// crew upgrade replaces both, and its pause can start and end inside one
// review, so a check for the pause alone could miss it.
func (e *evalRun) goOn() bool {
	if e.stopped == "" {
		switch {
		case e.p.Paused():
			e.stopped = "the project was paused"
		case shortHash(e.tested.rubric) != e.tested.rubricHash:
			e.stopped = "the rubric changed"
		case shortHash(e.tested.reviewer) != e.tested.reviewerHash:
			e.stopped = "the reviewer's prompt changed"
		}
	}
	return e.stopped == ""
}

// session runs cmd, one of the run's claude -p sessions, as printSession does,
// unless the run has stopped.
func (e *evalRun) session(cmd *exec.Cmd, out, who string) (string, error) {
	if !e.goOn() {
		return "", errors.New(e.stopped)
	}
	return printSession(cmd, out, who)
}

// rerun reviews r's range again, from a checkout of its head, and returns the
// reviewer's report. What the reviewer was given and said go in files
// starting with base.
func (e *evalRun) rerun(t Task, r AutomatedReview, base string) (string, error) {
	if err := e.checkOut(t, r.Head); err != nil {
		return "", err
	}
	rng := r.Fork + ".." + r.Head
	count, err := git(e.checkout, "rev-list", "--count", rng)
	if err != nil {
		return "", fmt.Errorf("git can't list %s", rng)
	}
	commits, _ := git(e.checkout, "log", "--format=----- %H%n%B", rng)
	briefing := base + ".briefing.md"
	if err := os.WriteFile(briefing, []byte(evalBriefing(t, r, e.checkout, atoiOr0(count), commits)), 0o644); err != nil {
		return "", err
	}
	// The pr-review skill's prompt for the Claude reviewer, SKILL.md step 4;
	// tests/guard.sh checks they match.
	prompt := fmt.Sprintf("Review `%s..HEAD` in `%s`. Use the scope exactly as given; don't widen it or reconstruct it. Follow the rubric at `%s` and the briefing at `%s`.",
		r.Fork, e.checkout, e.tested.rubric, briefing)
	cmd := e.c.claude("-p", "--agent", "reviewer", "--add-dir", e.logs, "--add-dir", filepath.Dir(e.tested.rubric),
		"--permission-mode", "auto", "--permission-prompts", "none", "--settings", e.c.path("config/readonly-settings.json"), prompt)
	cmd.Dir = e.checkout
	return e.session(cmd, base+".report.md", "the reviewer")
}

// checkOut puts the eval's checkout at head. When the clone doesn't have it,
// it fetches the task's PR head from origin: a squash-merged PR's commits
// aren't on the default branch.
func (e *evalRun) checkOut(t Task, head string) error {
	has := func() bool { return gitOK(e.p.Repo(), "cat-file", "-e", head+"^{commit}") }
	pr := t.PRNumber()
	if !has() && pr != "" {
		gitOK(e.p.Repo(), "fetch", "--quiet", "origin", "refs/pull/"+pr+"/head")
	}
	if !has() {
		return fmt.Errorf("commit %s is neither in the clone nor on PR #%s", head, pr)
	}
	if !gitOK(e.checkout, "checkout", "--quiet", "--detach", head) {
		return fmt.Errorf("couldn't check out %s in %s", head, e.checkout)
	}
	return nil
}

// notAFinding is what the judge counts as not a review finding: the kinds of
// comment that agents/misses.md says aren't a miss, in its words. The judge
// tells the last two from the brief and the user's words before the review.
// misses.md's last kind, a point the review already raised, needs the review's
// record, which the judge doesn't see.
var notAFinding = []string{
	"a question, or praise",
	"a change of mind: the user asking for something other than what they approved",
	"something the brief or the user's decisions settled, such as a structure the brief chose",
}

// judgePrompt asks whether report raised miss. brief is the task's brief and
// what the user had said on it before the review, which tell a change of mind
// or a settled point from a miss.
func judgePrompt(miss string, brief []string, report string) string {
	if len(brief) == 0 {
		brief = []string{"Nothing recorded."}
	}
	return "You judge one case of a review eval. A code reviewer reviewed a pull request; its report is below. " +
		"After that review, the user found the miss below on the same pull request.\n\n" +
		"Did the report raise the miss? It did when one of its findings is about the same problem, in any words and at any severity. " +
		"A finding that only touches the same code doesn't count.\n\n" +
		"Answer on the first line with exactly one of: found, missed, not a review finding. " +
		"The miss is not a review finding when it is " + strings.Join(notAFinding, "; or ") + ". " +
		"The task's brief, and what the user had asked for and decided before the review, are below the miss.\n\n" +
		"On the next line, quote the finding that raised it, word for word: its title line is enough. " +
		"Write none if no finding did.\n\n" +
		"## The miss\n\n" + miss + "\n\n## The brief, and what the user had said before the review\n\n" + strings.Join(brief, "\n\n") +
		"\n\n## The report\n\n" + report + "\n"
}

// judge asks a fresh session whether the report raised the case's miss.
func (e *evalRun) judge(ec evalCase, report string) (verdict, string, error) {
	out := filepath.Join(e.logs, strings.ReplaceAll(ec.id, "/", "-")+".judge.md")
	// --tools takes a list: the flag after it ends the list, so the prompt
	// isn't taken for a tool.
	cmd := e.c.claude("-p", "--tools", "", "--permission-prompts", "none", judgePrompt(ec.miss, briefBefore(ec.task, ec.review), report))
	cmd.Dir = os.TempDir() // away from any repo's CLAUDE.md
	answer, err := e.session(cmd, out, "the judge")
	if err != nil {
		return "", "", err
	}
	v, finding, ok := parseJudgement(answer)
	if !ok {
		return "", "", fmt.Errorf("the judge answered none of found, missed or not a review finding; see %s", filepath.Base(out))
	}
	return v, finding, nil
}

// parseJudgement reads the judge's answer: the verdict that starts its first
// line, then the finding it quotes, on the rest of that line or else the next
// line with text, or "" for none.
func parseJudgement(answer string) (verdict, string, bool) {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	v, rest, ok := leadingVerdict(lines[0])
	if !ok {
		return "", "", false
	}
	quote := strings.Trim(rest, " *`_.:—-") // "found: …", "**Found.**"
	for _, l := range lines[1:] {
		if quote == "" {
			quote = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), ">"))
		}
	}
	if strings.EqualFold(strings.Trim(quote, " .\"*"), "none") {
		quote = ""
	}
	return v, quote, true
}

// leadingVerdict is the verdict line starts with, in any case and markdown,
// and what follows it. A verdict must end a word: "foundation" isn't "found".
func leadingVerdict(line string) (verdict, string, bool) {
	line = strings.Trim(line, " *`_")
	for _, v := range []verdict{verdictNotAFinding, verdictFound, verdictMissed} {
		if len(line) < len(v) || !strings.EqualFold(line[:len(v)], string(v)) {
			continue
		}
		rest := line[len(v):]
		if next, _ := utf8.DecodeRuneInString(rest); rest == "" || !unicode.IsLetter(next) && !unicode.IsDigit(next) {
			return v, rest, true
		}
	}
	return "", "", false
}

// printSession runs cmd, a claude -p session, and returns its final message, which
// it also writes to out; what it logs goes beside it, in a .log file. who names
// the session in an error, which names the log by its file alone: the report
// gives their directory.
func printSession(cmd *exec.Cmd, out, who string) (string, error) {
	logf := strings.TrimSuffix(out, ".md") + ".log"
	log, err := os.Create(logf)
	if err != nil {
		return "", err
	}
	var said bytes.Buffer
	cmd.Stdout, cmd.Stderr = &said, log
	runErr := cmd.Run()
	log.Close()
	if err := os.WriteFile(out, said.Bytes(), 0o644); err != nil {
		return "", err
	}
	if runErr != nil {
		var x *exec.ExitError
		if errors.As(runErr, &x) {
			return "", fmt.Errorf("%s exited with %d; see %s", who, x.ExitCode(), filepath.Base(logf))
		}
		return "", fmt.Errorf("%s didn't start: %v", who, runErr)
	}
	return said.String(), nil
}

// evalReport is the report of a run: what it tested with, the totals, how it
// compares with the last run, then one line per case. The next run reads its
// header and case lines back with parseEvalReport, so the two keep one format;
// TestEvalReportAndComparison checks the round trip.
func evalReport(project string, at time.Time, tested evalSetup, logs string, results []evalResult, comparison string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review eval of %s, %s\n\n", project, at.Format(logTime))
	fmt.Fprintf(&b, "- Rubric: `%s` (%s)\n", tested.rubric, tested.rubricHash)
	fmt.Fprintf(&b, "- Reviewer: `%s` (%s)\n", tested.reviewer, tested.reviewerHash)
	fmt.Fprintf(&b, "- Claude Code: %s\n", tested.claude)
	fmt.Fprintf(&b, "- Cases: %s\n", cmp.Or(tested.selection, "all"))
	fmt.Fprintf(&b, "- Briefings, reviews and judgements: `%s`\n\n", logs)
	b.WriteString(evalTotals(results) + "\n")
	if comparison != "" {
		b.WriteString("\n" + comparison + "\n")
	}
	b.WriteString("\n## Cases\n\n")
	for _, r := range results {
		fmt.Fprintf(&b, "- `%s` **%s** — `%s..%s` — %s", r.id, r.verdict, short(r.review.Fork), short(r.review.Head), firstLine(r.miss, 100))
		if r.detail != "" {
			fmt.Fprintf(&b, " — %s", firstLine(r.detail, 160))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// evalTotals counts the misses found, of those judged review findings, and
// the cases that weren't.
func evalTotals(results []evalResult) string {
	n := map[verdict]int{}
	for _, r := range results {
		n[r.verdict]++
	}
	s := fmt.Sprintf("Found %d of %d misses.", n[verdictFound], n[verdictFound]+n[verdictMissed])
	if k := n[verdictNotAFinding]; k > 0 {
		s += fmt.Sprintf(" Not a review finding: %d.", k)
	}
	if k := n[verdictNotRun]; k > 0 {
		s += fmt.Sprintf(" Not run: %d.", k)
	}
	return s
}

// short is the first 7 characters of a SHA.
func short(sha string) string { return sha[:min(7, len(sha))] }

// firstLine is the first line of text with something in it, without a quote's
// ">", cut to at most n characters.
func firstLine(text string, n int) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), ">"))
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > n {
			return string(r[:n-1]) + "…"
		}
		return l
	}
	return ""
}

// lastFullEval is the newest report in dir of a run over every case that
// judged at least one, with what it tested and its verdicts. A run narrowed by
// --task or --limit, a trial, or one cut short would compare a full run with a
// few of its cases. Reports are named for their times, so the newest sorts last.
func lastFullEval(dir string) (string, evalSetup, map[string]verdict, bool) {
	reports, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	slices.Sort(reports)
	for i := len(reports) - 1; i >= 0; i-- {
		b, err := os.ReadFile(reports[i])
		if err != nil {
			continue
		}
		setup, verdicts := parseEvalReport(string(b))
		judged := false
		for _, v := range verdicts {
			judged = judged || v != verdictNotRun
		}
		if setup.selection == "" && judged {
			return reports[i], setup, verdicts, true
		}
	}
	return "", evalSetup{}, nil, false
}

// parseEvalReport reads back what evalReport wrote: what the run tested with,
// and each case's verdict by its id.
func parseEvalReport(text string) (evalSetup, map[string]verdict) {
	var s evalSetup
	hash := func(rest string) string { // "`<path>` (<hash>)"
		_, h, _ := strings.Cut(rest, "` (")
		return strings.TrimSuffix(h, ")")
	}
	verdicts := map[string]verdict{}
	for _, l := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(l, "- Rubric: "); ok {
			s.rubricHash = hash(rest)
		} else if rest, ok := strings.CutPrefix(l, "- Reviewer: "); ok {
			s.reviewerHash = hash(rest)
		} else if rest, ok := strings.CutPrefix(l, "- Claude Code: "); ok {
			s.claude = rest
		} else if rest, ok := strings.CutPrefix(l, "- Cases: "); ok && rest != "all" {
			s.selection = rest
		} else if rest, ok := strings.CutPrefix(l, "- `"); ok {
			id, rest, _ := strings.Cut(rest, "` **")
			v, _, ok := strings.Cut(rest, "**")
			if ok {
				verdicts[id] = verdict(v)
			}
		}
	}
	return s, verdicts
}

// compareEvals compares this run's verdicts with the last full run's, in prevFile,
// on the cases both judged, and says what changed in what they tested with.
// Totals alone wouldn't do: the test set grows as tasks merge.
func compareEvals(prevFile string, prev evalSetup, prevVerdicts map[string]verdict, now evalSetup, results []evalResult) string {
	shared, nowFound, nowMissed, added := 0, 0, 0, 0
	for _, r := range results {
		was, ok := prevVerdicts[r.id]
		switch {
		case !ok:
			added++
		case was == verdictNotRun || r.verdict == verdictNotRun:
			// Not judged in one of the runs: nothing to compare.
		default:
			shared++
			if was == verdictMissed && r.verdict == verdictFound {
				nowFound++
			} else if was == verdictFound && r.verdict == verdictMissed {
				nowMissed++
			}
		}
	}
	changed := func(what, before, after string) string {
		if before == after {
			return what + " is the same"
		}
		return fmt.Sprintf("%s changed (%s → %s)", what, before, after)
	}
	return fmt.Sprintf("Compared with the last full run, `%s`: %d now found, %d now missed, of %d shared case(s); %d new case(s). Since then: %s; %s; %s.",
		prevFile, nowFound, nowMissed, shared, added,
		changed("the rubric", prev.rubricHash, now.rubricHash),
		changed("the reviewer's prompt", prev.reviewerHash, now.reviewerHash),
		changed("Claude Code", prev.claude, now.claude))
}
