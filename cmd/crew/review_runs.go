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
// Log's records (reviewRecord), each automated review, whose findings are
// raised and whose fixed ones are taken, and the runs old records name.
func (t Task) ReviewRuns() ([]ReviewRun, error) {
	var runs, old []ReviewRun
	for _, e := range t.LogEntries() {
		if run, ok := recordedRun(e); ok {
			runs = append(runs, run)
		} else {
			old = append(old, oldRunsIn(e)...)
		}
	}
	if t.SecurityReviewed() {
		// A high-risk task can't be handed over before its security review is
		// recorded, so the review ran by its first hand-over.
		h, err := t.StatusHistory()
		if err != nil {
			return nil, err
		}
		old = append(old, ReviewRun{Step: stepSecurityReview, At: h.First(PROpen)})
	}
	// An old record counts unless a counted record of its step comes at or
	// before it: from then on the step's runs are counted, so an old-form line
	// only names one of them again. The observer and the Evidence's security
	// review say only that the step ran in the task, so any counted record of
	// it covers them.
	for _, o := range old {
		onceATask := o.Step == stepPrePRReview || o.Step == stepSecurityReview
		if !slices.ContainsFunc(runs, func(r ReviewRun) bool {
			return r.Step == o.Step && (onceATask || r.Known && !r.At.After(o.At))
		}) {
			runs = append(runs, o)
		}
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
	return runs, nil
}

// rerunLabel is what a lead adds to a step's name for its second run: "Brief
// review 2", "Brief review (2nd)". The run is still a run of that step.
const rerunLabel = `(?: \d+| \([^)]*\))?`

// reviewRecord is a review step's record, a Log message that starts
// "<step>: <raised> raised, <taken> taken", as LEDGER.md's Log section gives it.
// It takes any step name, so a repo's own review agent needs no listing here.
var reviewRecord = regexp.MustCompile(`^([^:(]+?)` + rerunLabel + `: (\d+) raised, (\d+) taken\b`)

// recordedRun is the run a Log entry records, when it's a review step's record.
func recordedRun(e logEntry) (ReviewRun, bool) {
	m := reviewRecord.FindStringSubmatch(e.msg)
	if m == nil {
		return ReviewRun{}, false
	}
	return ReviewRun{Step: m[1], At: e.at, Raised: atoiOr0(m[2]), Taken: atoiOr0(m[3]), Known: true}, true
}

// The records from before steps stated their counts, in the forms the ledger
// holds. A report over past tasks should count those runs, as runs of unknown
// value rather than runs that took nothing. They can go once no report reads
// tasks logged before 2026-11-18, when the counted records began. A past
// security review is read from the task's Evidence (SecurityReviewed).
var oldRecords = []struct {
	step string
	re   *regexp.Regexp
}{
	// "Plan review: 3 of 4 taken.", "Brief review (2nd): …". A brief review was
	// called a plan review at first. "Plan review skipped: …" isn't a run.
	{stepBriefReview, regexp.MustCompile(`^(Plan|Brief) review` + rerunLabel + `:`)},
	// "Exhale: folded a repeated line."
	{stepExhale, regexp.MustCompile(`^Exhale:`)},
	// "PR #24 opened. Pre-PR review: took 1 …": the observer's result often sat
	// inside another entry, so this one matches anywhere in the line.
	{stepPrePRReview, regexp.MustCompile(`Pre-PR review|casual-observer declined`)},
}

// A repo review agent's runs in the old forms: a sentence that starts with its
// result, "test-review: no findings." or "test-review x3 (…)", and the agents a
// pre-PR review lists, "Pre-PR review (casual-observer, test-review x2,
// comment-review)". Each ran once, or as many times as its " x<n>" says.
var (
	oldAgentRuns   = regexp.MustCompile(`(?:^|[.;] )([a-z]+(?:-[a-z]+)*-review)(?::| x(\d+))`)
	oldAgentList   = regexp.MustCompile(`Pre-PR review \(([^)]*)\)`)
	oldListedAgent = regexp.MustCompile(`\b([a-z]+(?:-[a-z]+)*-review)(?: x(\d+))?\b`)
)

// oldRunsIn is the runs a Log entry's free text names in an old form.
func oldRunsIn(e logEntry) []ReviewRun {
	var runs []ReviewRun
	for _, old := range oldRecords {
		if old.re.MatchString(e.msg) {
			runs = append(runs, ReviewRun{Step: old.step, At: e.at})
		}
	}
	agents := oldAgentRuns.FindAllStringSubmatch(e.msg, -1)
	for _, list := range oldAgentList.FindAllStringSubmatch(e.msg, -1) {
		agents = append(agents, oldListedAgent.FindAllStringSubmatch(list[1], -1)...)
	}
	// An entry may name an agent in both forms. It ran as many times as the
	// larger count says, not their sum.
	var names []string
	times := map[string]int{}
	for _, m := range agents {
		if _, named := times[m[1]]; !named {
			names = append(names, m[1])
		}
		times[m[1]] = max(times[m[1]], atoiOr0(m[2]), 1)
	}
	for _, name := range names {
		for range times[name] {
			runs = append(runs, ReviewRun{Step: name, At: e.at})
		}
	}
	return runs
}
