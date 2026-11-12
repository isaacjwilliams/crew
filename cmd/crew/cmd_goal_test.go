package main

import "testing"

// A goal lead is sent its workers' questions, and never the ones crew records
// for the user: a collaborator's comment, or CI still red past MAX_CI_FIXES.
func TestWorkerQuestionsLeaveOutTheUsers(t *testing.T) {
	task := Task{docWith(t, "## Open questions\n"+
		"- [ ] Should the export include archived seasons?\n"+
		"- [ ] @bob commented on PR #12, comment 5 — [link](u5)\n"+
		"- [ ] CI is still red on ccc3333 after 2 CI rounds (MAX_CI_FIXES=2): test. Look at it yourself, or tell the worker to try again.\n"+
		"- [x] An answered question\n\n## Log\n")}
	if got, want := workerQuestions(task), "- [ ] Should the export include archived seasons?"; got != want {
		t.Errorf("workerQuestions = %q, want %q", got, want)
	}
}
