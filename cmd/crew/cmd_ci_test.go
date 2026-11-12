package main

import (
	"slices"
	"testing"
)

func TestRollup(t *testing.T) {
	run := func(name, status, conclusion string) ciCheck {
		return ciCheck{Typename: "CheckRun", Name: name, Status: status, Conclusion: conclusion}
	}
	status := func(context, state string) ciCheck {
		return ciCheck{Typename: "StatusContext", Context: context, State: state}
	}
	for _, c := range []struct {
		name   string
		checks []ciCheck
		state  ciState
		failed []string
	}{
		{"no checks", nil, "", nil},
		{"check runs that passed", []ciCheck{run("test", "COMPLETED", "SUCCESS"), run("lint", "COMPLETED", "SUCCESS")}, ciPassing, nil},
		{"a failed check run", []ciCheck{run("test", "COMPLETED", "FAILURE"), run("lint", "COMPLETED", "SUCCESS")}, ciFailing, []string{"test"}},
		{"timed out, failed to start or needing action is red", []ciCheck{run("a", "COMPLETED", "TIMED_OUT"), run("b", "COMPLETED", "STARTUP_FAILURE"), run("c", "COMPLETED", "ACTION_REQUIRED")}, ciFailing, []string{"a", "b", "c"}},
		{"cancelled, skipped and neutral aren't red", []ciCheck{run("a", "COMPLETED", "CANCELLED"), run("b", "COMPLETED", "SKIPPED"), run("c", "COMPLETED", "NEUTRAL")}, ciPassing, nil},
		{"a check run still in progress", []ciCheck{run("test", "COMPLETED", "FAILURE"), run("lint", "IN_PROGRESS", "")}, ciRunning, nil},
		{"a check run queued", []ciCheck{run("test", "QUEUED", "")}, ciRunning, nil},
		{"status contexts that passed", []ciCheck{status("ci/semaphore", "SUCCESS")}, ciPassing, nil},
		{"a failed or errored status context", []ciCheck{status("ci/semaphore", "FAILURE"), status("ci/other", "ERROR")}, ciFailing, []string{"ci/semaphore", "ci/other"}},
		{"a pending status context", []ciCheck{status("ci/semaphore", "FAILURE"), status("ci/other", "PENDING")}, ciRunning, nil},
		{"an expected status context that hasn't reported", []ciCheck{status("ci/semaphore", "EXPECTED")}, ciRunning, nil},
		{"both kinds, one red", []ciCheck{run("test", "COMPLETED", "SUCCESS"), status("ci/semaphore", "FAILURE")}, ciFailing, []string{"ci/semaphore"}},
	} {
		state, failed := rollup(c.checks)
		if state != c.state || !slices.Equal(failed, c.failed) {
			t.Errorf("%s: rollup = %q, %q; want %q, %q", c.name, state, failed, c.state, c.failed)
		}
	}
}
