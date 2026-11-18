package main

// What each review step in crew's process found, read from a task's file: how
// many findings each run raised, and how many were taken. A finding is taken
// when it changed the work: the brief, or the branch.

import (
	"regexp"
	"slices"
	"time"
)

// The review steps crew names. A review agent a repo's AGENTS.md has the worker
// run is a step too, named by the agent.
const (
	stepBriefReview     = "Brief review"     // the lead's or goal lead's brief-reviewer
	stepExhale          = "Exhale"           // the worker's /simplify
	stepPrePRReview     = "Pre-PR review"    // the worker's casual-observer
	stepSecurityReview  = "Security review"  // the worker's /security-review
	stepAutomatedReview = "Automated review" // the worker's pr-review skill
)

// A run of a review step: when it was recorded, and the findings it raised and
// how many of them were taken. Known is false for a run recorded before steps
// stated their counts: its value is unknown, and Raised and Taken mean nothing.
type ReviewRun struct {
	Step          string
	At            time.Time
	Raised, Taken int
	Known         bool
}

// ReviewRuns is every run of a review step the task records, oldest first: the
// Log's records (reviewRecord), and each automated review, whose findings are
// raised and whose fixed ones are taken.
func (t Task) ReviewRuns() []ReviewRun {
	var runs []ReviewRun
	for _, e := range t.LogEntries() {
		runs = append(runs, reviewRunsIn(e)...)
	}
	for _, r := range t.AutomatedReviews() {
		run := ReviewRun{Step: stepAutomatedReview, At: r.At, Raised: len(r.Findings), Known: true}
		for _, f := range r.Findings {
			if f.Outcome() == OutcomeFixed {
				run.Taken++
			}
		}
		runs = append(runs, run)
	}
	slices.SortStableFunc(runs, func(a, b ReviewRun) int { return a.At.Compare(b.At) })
	return runs
}

// reviewRecord is a review step's record, a Log message that starts
// "<step>: <raised> raised, <taken> taken", as LEDGER.md's Log section gives it.
// It takes any step name, so a repo's own review agent needs no listing here.
var reviewRecord = regexp.MustCompile(`^([^:]+): (\d+) raised, (\d+) taken\b`)

// The records from before steps stated their counts, in the forms the ledger
// holds. A report over past tasks should count those runs, as runs of unknown
// value rather than runs that took nothing. They can go once no report reads
// tasks logged before 2026-11-18, when the counted records began.
var oldRecords = []struct {
	step string
	re   *regexp.Regexp
}{
	// "Plan review: 3 of 4 taken.", "Brief review (2nd): …". A brief review was
	// called a plan review at first. "Plan review skipped: …" isn't a run.
	{stepBriefReview, regexp.MustCompile(`^(Plan|Brief) review( \d+| \([^)]*\))?:`)},
	// "Exhale: folded a repeated line."
	{stepExhale, regexp.MustCompile(`^Exhale:`)},
	// "PR #24 opened. Pre-PR review: took 1 …": the observer's result often sat
	// inside another entry, so this one matches anywhere in the line.
	{stepPrePRReview, regexp.MustCompile(`Pre-PR review|casual-observer declined`)},
}

// oldAgentRuns is a repo review agent's runs in the old form, its name and how
// many times it ran: "test-review x3" is three runs of test-review.
var oldAgentRuns = regexp.MustCompile(`\b([a-z]+(?:-[a-z]+)+) x(\d+)\b`)

// reviewRunsIn is the runs a Log entry records: one, when it's a review
// step's record, or else those its free text names in an old form.
func reviewRunsIn(e logEntry) []ReviewRun {
	if m := reviewRecord.FindStringSubmatch(e.msg); m != nil {
		return []ReviewRun{{Step: m[1], At: e.at, Raised: atoiOr0(m[2]), Taken: atoiOr0(m[3]), Known: true}}
	}
	var runs []ReviewRun
	for _, old := range oldRecords {
		if old.re.MatchString(e.msg) {
			runs = append(runs, ReviewRun{Step: old.step, At: e.at})
		}
	}
	for _, m := range oldAgentRuns.FindAllStringSubmatch(e.msg, -1) {
		for range atoiOr0(m[2]) {
			runs = append(runs, ReviewRun{Step: m[1], At: e.at})
		}
	}
	return runs
}
