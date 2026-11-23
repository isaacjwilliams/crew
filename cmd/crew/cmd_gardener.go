package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// crew gardener [--all|--project P]: the gardener over each project's commits
// merged since its last run, and the misses agent over its tasks merged since
// its last run. It exits 1 if any project's run fails.
func cmdGardener(c *Crew, args []string) error {
	for _, tool := range []string{"git", "claude"} {
		if err := need(tool); err != nil {
			return err
		}
	}
	if err := c.requireHost(); err != nil {
		return err
	}
	proj, all := "", false
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all, args = true, args[1:]
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[2:]
		default:
			return usage("crew gardener [--all|--project P]")
		}
	}
	if !all {
		var err error
		if proj, err = c.oneProject(proj); err != nil {
			return err
		}
	}
	projects := c.projects(proj)
	os.MkdirAll(c.path("logs"), 0o755)
	failed := false
	for _, name := range projects {
		if err := c.garden(name); err != nil {
			report(err)
			warn("%s: gardener run failed", name)
			failed = true
		}
	}
	if failed {
		return exitQuietly(1)
	}
	return nil
}

// gardenerCheckout is the working directory the gardener and the misses agent
// share: a detached checkout of origin/DEFAULT_BRANCH.
func gardenerCheckout(p *Project) string {
	return filepath.Join(p.WorkRoot(), p.Name, "_gardener")
}

// garden runs the gardener and the misses agent over one project, unless it's
// paused.
func (c *Crew) garden(name string) error {
	p, err := c.loadProject(name)
	if err != nil {
		return err
	}
	if p.Paused() {
		fmt.Printf("%s: paused; skipped\n", name)
		return nil
	}
	wt := gardenerCheckout(p)
	if err := p.detachedCheckout(wt); err != nil {
		return err
	}
	// Each run keeps its own cursor, so one failing doesn't hold back the other.
	failed := false
	for _, run := range []func(*Project, string) error{c.reviewCommits, c.sortMisses} {
		if err := run(p, wt); err != nil {
			report(err)
			failed = true
		}
	}
	if failed {
		return exitQuietly(1)
	}
	return nil
}

// reviewCommits runs the gardener over the default branch's commits since the
// one in gardener.last, in wt, a checkout of the branch. The first run starts
// from GARDENER_LOOKBACK.
func (c *Crew) reviewCommits(p *Project, wt string) error {
	head, _ := git(wt, "rev-parse", "HEAD")
	lastFile := filepath.Join(p.Dir, "gardener.last")
	b, _ := os.ReadFile(lastFile)
	last := strings.TrimRight(string(b), "\n")
	if last == head {
		fmt.Printf("%s: no new commits since the last run\n", p.Name)
		return nil
	}
	var base string
	if last != "" && gitOK(wt, "merge-base", "--is-ancestor", last, head) {
		base = last
	} else {
		base, _ = git(wt, "rev-list", "-1", "--before="+p.Settings["GARDENER_LOOKBACK"], head)
	}
	rng := head
	if base != "" {
		rng = base + ".." + head
	}
	count, _ := git(wt, "rev-list", "--count", rng)
	if count == "0" {
		os.WriteFile(lastFile, []byte(head+"\n"), 0o644)
		fmt.Printf("%s: no commits in range\n", p.Name)
		return nil
	}
	prompt := fmt.Sprintf("Project: %s\nProposals directory: %s\nTasks directory: %s\nProposal template: %s\nCommit range to review: %s (%s commits on origin/%s)\nToday's date: %s\nFollow your process and write proposals only into the proposals directory.",
		p.Name, filepath.Join(p.Dir, "proposals"), filepath.Join(p.Dir, "tasks"), c.path("templates/proposal.md"), rng, count, p.DefaultBranch(), time.Now().Format("2006-01-02"))
	if err := c.runAgent(p, wt, "gardener", fmt.Sprintf("reviewing %s commits (%s)", count, rng), prompt); err != nil {
		return err
	}
	return c.advance(lastFile, head, "gardener "+p.Name+" ("+rng+")")
}

// sortMisses runs the misses agent over the project's tasks merged after
// missesSince that have the user's comments after an automated review, in wt, a
// checkout of the default branch. The prompt names each task's comments, so the
// agent sorts exactly those. misses.last then holds the newest merge time the
// run read.
func (c *Crew) sortMisses(p *Project, wt string) error {
	lastFile := filepath.Join(p.Dir, "misses.last")
	since, err := missesSince(p, wt, lastFile)
	if err != nil {
		return err
	}
	tasks, newest := tasksToSort(c.tasks(p.Name), since, time.Now())
	if len(tasks) == 0 {
		fmt.Printf("%s: no newly merged tasks with your comments after automated review\n", p.Name)
		return nil
	}
	var list strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&list, "- %s\n", t.task.Path)
		for _, cm := range t.comments {
			fmt.Fprintf(&list, "  - round %d, after automated review %d: %s\n", cm.Round, cm.Review.Number, cm.Comment.Item)
		}
	}
	prompt := fmt.Sprintf("Project: %s\nProposals directory: %s\nProposal template: %s\nCrew's review rubric: %s\nToday's date: %s\nTasks to read, and the comments to sort in each:\n%sFollow your process and write proposals only into the proposals directory.",
		p.Name, filepath.Join(p.Dir, "proposals"), c.path("templates/proposal.md"), c.path(".claude/skills/pr-review/rubric.md"), time.Now().Format("2006-01-02"), list.String())
	if err := c.runAgent(p, wt, "misses", fmt.Sprintf("sorting your comments on %d merged task(s)", len(tasks)), prompt); err != nil {
		return err
	}
	return c.advance(lastFile, newest.Format(time.RFC3339), fmt.Sprintf("misses %s (%d tasks)", p.Name, len(tasks)))
}

// A merged task for the misses agent, and the comments in it to sort: the
// user's comments after an automated review.
type taskToSort struct {
	task     Task
	comments []CommentAfterReview
}

// tasksToSort picks, from tasks, those merged after since that have the
// user's comments after an automated review, and returns them with the newest
// of their merge times. A merge time is when crew sync logged the merge, so a
// task synced late still comes after since. Log times are to the minute, so a
// task synced later in now's minute would tie with a cursor set now, and be
// skipped: one merged in that minute waits for the next run.
func tasksToSort(tasks []Task, since, now time.Time) ([]taskToSort, time.Time) {
	thisMinute := now.Truncate(time.Minute)
	var picked []taskToSort
	newest := since
	for _, t := range tasks {
		if t.Status() != Merged {
			continue
		}
		merged := mergedAt(t.LogEntries())
		if !merged.After(since) || !merged.Before(thisMinute) {
			continue
		}
		if comments := t.CommentsAfterReview(); len(comments) > 0 {
			picked = append(picked, taskToSort{t, comments})
			if merged.After(newest) {
				newest = merged
			}
		}
	}
	return picked, newest
}

// missesSince is where the misses agent's run starts: the time in lastFile,
// or on the first run the date of the newest commit in wt before
// GARDENER_LOOKBACK. A repo younger than that has none, so the first run reads
// every merged task.
func missesSince(p *Project, wt, lastFile string) (time.Time, error) {
	b, err := os.ReadFile(lastFile)
	if errors.Is(err, os.ErrNotExist) {
		date, _ := git(wt, "log", "-1", "--format=%cI", "--before="+p.Settings["GARDENER_LOOKBACK"], "HEAD")
		since, _ := time.Parse(time.RFC3339, date)
		return since, nil
	} else if err != nil {
		return time.Time{}, err
	}
	since, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return since, fail("%s: '%s' isn't a time", lastFile, strings.TrimSpace(string(b)))
	}
	return since, nil
}

// runAgent runs agent once in wt, with config/gardener-settings.json, and
// writes its output to a log of its own. doing says what the run is for. It
// warns with the agent's exit code when the run fails.
func (c *Crew) runAgent(p *Project, wt, agent, doing, prompt string) error {
	settings, err := readSettings(c.path("config/gardener-settings.json"))
	if err != nil {
		return err
	}
	logf := c.path("logs", agent+"-"+p.Name+"-"+time.Now().Format("20060102-1504")+".log")
	fmt.Printf("%s: %s — log: %s\n", p.Name, doing, logf)
	log, err := os.Create(logf)
	if err != nil {
		return err
	}
	cmd := c.claude("-p", "--add-dir", c.Home, "--agent", agent, "--permission-mode", "acceptEdits",
		"--permission-prompts", "none", "--max-turns", p.Settings["GARDENER_MAX_TURNS"],
		"--settings", c.sessionSettings(settings, p), prompt)
	cmd.Dir, cmd.Stdout, cmd.Stderr = wt, log, log
	runErr := cmd.Run()
	log.Close()
	tailTo(os.Stdout, logf, 15)
	if runErr != nil {
		rc := 127 // claude didn't start
		var x *exec.ExitError
		if errors.As(runErr, &x) {
			rc = x.ExitCode()
		}
		warn("%s: %s exited with %d; see %s", p.Name, agent, rc, logf)
		return exitQuietly(1)
	}
	return nil
}

// advance records a finished run: it writes cursor into file, and commits the
// ledger with msg.
func (c *Crew) advance(file, cursor, msg string) error {
	if err := os.WriteFile(file, []byte(cursor+"\n"), 0o644); err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	c.commit(msg)
	c.unlock()
	return nil
}
