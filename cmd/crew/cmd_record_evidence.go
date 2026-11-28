package main

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const evidenceHeading = "## Evidence"

// crew record-evidence TASK N --on SHA (--ran "<what ran and what it showed>" |
// --not-run "<why it couldn't run, and what to try>"): records what shows the
// task's acceptance criterion N holds, as a record that replaces any earlier
// one for N. crew hand-over decides from these records alone; the prose under
// ## Evidence is for people.
func cmdRecordEvidence(c *Crew, args []string) error {
	const use = `crew record-evidence TASK N --on SHA (--ran "<what ran and what it showed>" | --not-run "<why it couldn't run, and what to try>")`
	if len(args) < 2 {
		return usage(use)
	}
	var on, ran, notRun string
	if rest, err := parseText(args[2:], map[string]*string{"--on": &on, "--ran": &ran, "--not-run": &notRun}, use); err != nil {
		return err
	} else if rest != "" {
		return usage(use)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	criteria := len(t.AcceptanceCriteria())
	if criteria == 0 {
		return fail("%s's ## Acceptance criteria has no '- ' items to record evidence for", t.ID())
	}
	n, err := strconv.Atoi(args[1])
	if err != nil || n < 1 || n > criteria {
		return fail("%s has acceptance criteria 1 to %d, not %s: give the number of the one this evidence shows", t.ID(), criteria, args[1])
	}
	ran, notRun = strings.TrimSpace(ran), strings.TrimSpace(notRun)
	if (ran == "") == (notRun == "") {
		return fail("say whether the check ran: --ran with what ran and what it showed, or --not-run with why it couldn't and what to try")
	}
	if on == "" {
		return fail("name the commit the check ran on, with --on <sha>")
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	sha, err := branchCommit(p.Repo(), t.Branch(), on)
	if err != nil {
		return err
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	r := newRecord("crew record-evidence", records)
	r.Evidence = &Evidence{Criterion: n, Commit: sha, Ran: ran != "", Shows: ran + notRun}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("record-evidence " + t.ID())
	fmt.Printf("recorded criterion %d's evidence, on %s\n", n, cut(sha, 12))
	return nil
}

// branchCommit is the full SHA of the commit on names, which must be on the
// task's branch, here or on origin: evidence shows the work the user would
// merge.
func branchCommit(repo, branch, on string) (string, error) {
	sha, _ := git(repo, "rev-parse", "--verify", "--quiet", on+"^{commit}")
	if sha == "" {
		return "", fail("%s isn't a commit in %s", on, repo)
	}
	for _, ref := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
		if branch != "" && gitOK(repo, "merge-base", "--is-ancestor", sha, ref) {
			return sha, nil
		}
	}
	return "", fail("%s isn't on the task's branch %s, here or on origin: record the evidence on a commit of the branch", on, branch)
}

// crew record-security-review TASK "<what it found, and what was done about
// each>": records a high-risk task's /security-review, which crew hand-over
// needs before it lets the task through.
func cmdRecordSecurityReview(c *Crew, args []string) error {
	if len(args) != 2 {
		return usage(`crew record-security-review TASK "<what it found, and what was done about each>"`)
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	findings := strings.TrimSpace(args[1])
	if findings == "" {
		return fail("say what the security review found, and what was done about each finding (or that it found nothing)")
	}
	if err := c.lock(); err != nil {
		return err
	}
	records, err := t.Records()
	if err != nil {
		return fail("%v", err)
	}
	r := newRecord("crew record-security-review", records)
	r.SecurityReview = &SecurityReview{Findings: findings}
	if err := t.AddRecords(r); err != nil {
		return err
	}
	c.commit("record-security-review " + t.ID())
	fmt.Println("recorded the security review")
	return nil
}

// evidenceLines are the task's evidence records as crew show prints them under
// ## Evidence: each criterion's newest record, in order, with whether its check
// ran, then the newest security review.
func evidenceLines(t Task, evidence map[int]Record, reviews []Record) []string {
	criteria, userChecks := t.AcceptanceCriteria(), t.UserChecks()
	var out []string
	entry := func(heading, status string, r Record, text string) {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(append(out, heading, strings.TrimSpace(status+" Recorded "+stamp(r)+":")), strings.Split(text, "\n")...)
	}
	for _, n := range slices.Sorted(maps.Keys(evidence)) {
		r := evidence[n]
		criterion := "(a criterion the brief no longer has)"
		if n <= len(criteria) {
			criterion = criteria[n-1]
		}
		status := "Ran."
		switch {
		case !r.Evidence.Ran && userChecks[n]:
			status = "Not run: a user check."
		case !r.Evidence.Ran:
			status = "Not run, and not a user check."
		}
		entry(fmt.Sprintf("### %d. %s (on %s)", n, criterion, cut(r.Evidence.Commit, 12)), status, r, r.Evidence.Shows)
	}
	if len(reviews) > 0 {
		r := reviews[len(reviews)-1]
		entry("### Security review", "", r, r.SecurityReview.Findings)
	}
	return out
}
