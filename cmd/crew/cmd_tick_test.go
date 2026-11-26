package main

import "testing"

func TestWaitsForSaysWhatATaskWaitsForNext(t *testing.T) {
	c := goalLedger(t, boardsGoal...)
	deps := newDependencies(c, c.branches("p"))
	for id, want := range map[string]string{
		"166": "it comes after 242",
		"243": "it's stacked on 148, which hasn't been dispatched yet",
	} {
		task, err := c.resolveTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := waitsFor(deps, task); got != want {
			t.Errorf("%s waits for %q, want %q", id, got, want)
		}
	}
}
