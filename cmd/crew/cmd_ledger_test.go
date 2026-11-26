package main

import "testing"

func TestWithoutDelivered(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	reportTask(t, c, "p", "001-merged", false, "status: merged")
	reportTask(t, c, "p", "002-done-spike", false, "status: done", "kind: spike")
	reportTask(t, c, "p", "003-abandoned", false, "status: abandoned")
	reportTask(t, c, "p", "004-working", false, "status: working")
	reportTask(t, c, "p", "005-twin-a", false, "status: working")
	reportTask(t, c, "p", "006-twin-b", false, "status: working")
	reportTask(t, c, "q", "001-merged-elsewhere", false, "status: merged")
	task := reportTask(t, c, "p", "010-waiting", false, "status: ready")

	for _, tc := range []struct{ name, value, want string }{
		{"drops merged and done, keeping the order of the rest", "004-working 001-merged 003-abandoned 002-done-spike", "004-working 003-abandoned"},
		{"reads short numbers", "4 1 2 3", "4 3"},
		{"reads commas", "004-working, 001-merged,002-done-spike", "004-working"},
		{"reads brackets", "[001-merged 003-abandoned]", "003-abandoned"},
		{"clears a list of only delivered tasks", "1, 2", ""},
		{"keeps a list with nothing to drop as written", "[4, 3]", "[4, 3]"},
		{"keeps an entry that names no task", "nonesuch 1 4", "nonesuch 4"},
		{"keeps an entry that names several tasks", "twin 1", "twin"},
		{"reads only the task's own project", "001-merged-elsewhere 1", "001-merged-elsewhere"},
		{"keeps an empty value", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.withoutDelivered(task, tc.value); got != tc.want {
				t.Errorf("withoutDelivered(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
