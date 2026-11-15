package main

// crew stats: how much of the user's time each merged task took, and how its
// automated review went, read from the ledger alone.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// crew stats [--since YYYY-MM-DD] [--all|--project P]: a row for each task
// merged since the date, 30 days ago by default, then totals and the median
// waits. It only reads the ledger, so it needs no lock and runs on any machine.
func cmdStats(c *Crew, args []string) error {
	y, m, d := time.Now().Date()
	since := time.Date(y, m, d-30, 0, 0, 0, 0, time.Local)
	all, proj := false, ""
	for len(args) > 0 {
		switch {
		case args[0] == "--all":
			all = true
		case args[0] == "--project" && len(args) > 1:
			proj, args = args[1], args[1:]
		case args[0] == "--since" && len(args) > 1:
			var err error
			if since, err = time.ParseInLocation("2006-01-02", args[1], time.Local); err != nil {
				return fail("--since takes a date, YYYY-MM-DD, not '%s'", args[1])
			}
			args = args[1:]
		default:
			return usage("crew stats [--since YYYY-MM-DD] [--all|--project P]")
		}
		args = args[1:]
	}
	scope, err := c.scope(proj, all)
	if err != nil {
		return err
	}
	for i, p := range c.projects(scope) {
		var stats []taskStats
		for _, t := range c.tasks(p) {
			if t.Status() != Merged {
				continue
			}
			if s := statsOf(t); !s.merged.Before(since) {
				stats = append(stats, s)
			}
		}
		slices.SortFunc(stats, func(a, b taskStats) int { return a.merged.Compare(b.merged) })
		if i > 0 {
			fmt.Println()
		}
		printStats(p, since, stats)
	}
	return nil
}

// taskStats is how much of the user's time a merged task took, and how its
// automated review went.
type taskStats struct {
	task   Task
	merged time.Time

	rounds, comments int           // the user's review rounds, and their comments in them
	prWait           time.Duration // the PR waiting on the user
	questionWait     time.Duration // the task's questions waiting on the user

	reviews    int // automated reviews
	cleanAfter int // the reviews it took to come back clean, or 0 if none did

	// The reviews' findings the user would ask about: those fixed, those the
	// worker judged not a defect, and those left for the user (a disputed
	// finding is both of the last two). Proposals, recorded nitpicks and
	// withdrawn findings aren't reported.
	fixed, setAside, leftForUser int
}

// add adds o's counts and waits to s's.
func (s *taskStats) add(o taskStats) {
	s.rounds += o.rounds
	s.comments += o.comments
	s.prWait += o.prWait
	s.questionWait += o.questionWait
	s.reviews += o.reviews
	s.fixed += o.fixed
	s.setAside += o.setAside
	s.leftForUser += o.leftForUser
}

// cells is the task's row after its ID, under statsHeadings. clean is the
// "clean after" cell.
func (s taskStats) cells(clean string) []string {
	return []string{strconv.Itoa(s.rounds), strconv.Itoa(s.comments), roughly(s.prWait), roughly(s.questionWait),
		strconv.Itoa(s.reviews), clean, strconv.Itoa(s.fixed), strconv.Itoa(s.setAside), strconv.Itoa(s.leftForUser)}
}

// cleanAfterText is the "clean after" cell: the number of reviews, "never",
// or "-" for a task with no automated review.
func (s taskStats) cleanAfterText() string {
	switch {
	case s.cleanAfter > 0:
		return strconv.Itoa(s.cleanAfter)
	case s.reviews > 0:
		return "never"
	}
	return "-"
}

// statsOf measures a task from its file.
func statsOf(t Task) taskStats {
	log := t.LogEntries()
	s := taskStats{
		task:     t,
		merged:   mergedAt(log),
		rounds:   t.FeedbackRounds(),
		comments: t.ReviewComments(),
		prWait:   waited(log, changedTo(PROpen), changesStatus),
	}
	asked := changedTo(NeedsInput)
	if t.Goal() != "" {
		// The goal lead answers most of its tasks' workers' questions itself.
		// Only those it leaves for the user wait on them, and for each it logs a
		// "For the user: …" line (goal-lead.md). A collaborator's comment and red
		// CI past MAX_CI_FIXES go to the user directly.
		asked = func(e logEntry) bool {
			return strings.HasPrefix(e.msg, "For the user:") || strings.HasSuffix(e.msg, recordedForTheUser)
		}
	}
	// A question is answered once the task moves on: crew tick marking it
	// needs-input, once its worker is idle, isn't an answer.
	answered := func(e logEntry) bool { return changesStatus(e) && !changedTo(NeedsInput)(e) }
	s.questionWait = waited(log, asked, answered)

	reviews := t.AutomatedReviews()
	s.reviews = len(reviews)
	for i, r := range reviews {
		if s.cleanAfter == 0 && r.Clean() {
			s.cleanAfter = i + 1
		}
		for _, f := range r.Findings {
			switch f.Outcome() {
			case OutcomeFixed:
				s.fixed++
			case OutcomeLeftForUser:
				s.leftForUser++
			}
			if f.SetAside() {
				s.setAside++
			}
		}
	}
	return s
}

// mergedAt is when the task's Log says it merged, the last time if it says so
// more than once, or zero if it never does: such a task isn't reported. crew
// sync logs "PR merged" after the status line; before Task.SetStatus it logged
// only that, so this reads it as changesStatus does, and until the same date.
func mergedAt(log []logEntry) time.Time {
	var at time.Time
	for _, e := range log {
		if st, ok := e.status(); (ok && st == Merged) || e.msg == "PR merged" {
			at = e.at
		}
	}
	return at
}

// changedTo picks the Log entries that change the task's status to s.
func changedTo(s Status) func(logEntry) bool {
	return func(e logEntry) bool {
		st, ok := e.status()
		return ok && st == s
	}
}

// waited adds up the time from each Log entry that starts a wait on the user
// to the next one that ends it.
func waited(log []logEntry, starts, ends func(logEntry) bool) time.Duration {
	var total time.Duration
	var since time.Time // when the wait under way began, or zero
	for _, e := range log {
		if !since.IsZero() && ends(e) {
			total += e.at.Sub(since)
			since = time.Time{}
		}
		if since.IsZero() && starts(e) {
			since = e.at
		}
	}
	return total
}

// changesStatus: the entry marks a change of the task's status.
func changesStatus(e logEntry) bool {
	if _, ok := e.status(); ok {
		return true
	}
	// Before Task.SetStatus, crew dispatch, crew followup and crew sync changed a
	// task's status without a status line, so their own lines mark the change in
	// tasks merged before then. Remove this after 2026-12-31: those tasks are
	// outside the default 30 days by then.
	return strings.HasPrefix(e.msg, "dispatched") || (strings.HasPrefix(e.msg, "followup (") && !onlyAfterAPause(e)) || e.msg == "PR merged"
}

// onlyAfterAPause: a followup whose only news was that a pause cut the
// worker's turn short. It leaves the status as it was.
func onlyAfterAPause(e logEntry) bool {
	return strings.Contains(e.msg, ", carrying on after a pause") && !strings.Contains(e.msg, " — note: ")
}

// The report's columns after the task's, each headed by two lines.
var statsHeadings = [][2]string{
	{"review", "rounds"}, {"", "comments"},
	{"PR", "waited on you"}, {"questions", "waited on you"},
	{"automated", "reviews"}, {"clean", "after"},
	{"findings", "fixed"}, {"set", "aside"}, {"left for", "you"},
}

// printStats prints a project's report: a row for each task, then the totals
// and the median waits.
func printStats(project string, since time.Time, stats []taskStats) {
	day := since.Format("2006-01-02")
	if len(stats) == 0 {
		fmt.Printf("%s: no tasks merged since %s\n", project, day)
		return
	}
	fmt.Printf("%s: %d task(s) merged since %s\n\n", project, len(stats), day)
	idWidth := len("median")
	for _, s := range stats {
		idWidth = max(idWidth, len(s.task.ID()))
	}
	row := func(id string, cells ...string) {
		line := fmt.Sprintf("  %-*s", idWidth, id)
		for i, h := range statsHeadings {
			line += fmt.Sprintf("  %*s", max(len(h[0]), len(h[1])), cells[i])
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	var top, bottom []string
	for _, h := range statsHeadings {
		top = append(top, h[0])
		bottom = append(bottom, h[1])
	}
	row("", top...)
	row("task", bottom...)

	var sum taskStats
	var prWaits, questionWaits []time.Duration
	for _, s := range stats {
		row(s.task.ID(), s.cells(s.cleanAfterText())...)
		sum.add(s)
		prWaits = append(prWaits, s.prWait)
		questionWaits = append(questionWaits, s.questionWait)
	}
	fmt.Println()
	row("total", sum.cells("")...)
	row("median", "", "", roughly(median(prWaits)), roughly(median(questionWaits)), "", "", "", "", "")
}

// median is the middle of the durations, or the mean of the two in the middle.
func median(ds []time.Duration) time.Duration {
	ds = slices.Sorted(slices.Values(ds))
	n := len(ds)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return ds[n/2]
	}
	return (ds[n/2-1] + ds[n/2]) / 2
}

// roughly is a duration as a person needs it: "40m", "5h", "3d", or "-" for
// none. Up to two days it's in hours, so a day and a half isn't "2d".
func roughly(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Hour:
		return fmt.Sprintf("%dm", d.Round(time.Minute)/time.Minute)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", d.Round(time.Hour)/time.Hour)
	}
	return fmt.Sprintf("%dd", d.Round(24*time.Hour)/(24*time.Hour))
}
