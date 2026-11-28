package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// crew migrate gives every task one status history, whichever Log it has: one
// with a status line for each change, one from before SetStatus, whose
// dispatched, followup and merge lines stood for changes they didn't log, and
// one with its history recorded already, which it leaves as it is.
func TestMigrateStatusHistory(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation(logTime, s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	recorded := migratedTask(t, "003-recorded", "---\nstatus: needs-input\n---\n## Log\n"+
		"- 2026-11-26 09:00 — status → pr-open\n- 2026-11-26 10:00 — status → merged\n")
	changed(t, recorded, "crew dispatch", at("2026-11-26 08:00"), Working)
	changed(t, recorded, "crew tick", at("2026-11-26 08:30"), NeedsInput)
	dir := filepath.Dir(recorded.Path)
	write := func(id, content string) Task {
		task := Task{Doc{filepath.Join(dir, id+".md")}}
		if err := os.WriteFile(task.Path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return task
	}
	lines := write("001-lines", "---\nstatus: needs-input\n---\n# Lines\n\nProse.\n\n## Log\n"+
		"- 2026-11-20 09:00 — created\n- 2026-11-20 09:30 — status → ready\n"+
		"- 2026-11-20 10:00 — status → working\n- 2026-11-20 10:00 — dispatched: worker a\n"+
		"- 2026-11-20 12:00 — status → pr-open\n- 2026-11-20 13:00 — status → addressing-feedback\n"+
		"- 2026-11-20 14:00 — status → pr-open\n- 2026-11-20 15:00 — status → needs-input\n")
	old := write("002-old", "---\nstatus: merged\n---\n## Log\n"+
		"- 2026-11-01 08:00 — marked ready\n- 2026-11-01 09:00 — dispatched: worker a\n"+
		"- 2026-11-01 10:00 — status → pr-open\n- 2026-11-01 12:00 — followup (resume): worker a\n"+
		"- 2026-11-01 13:00 — status → pr-open\n- 2026-11-02 13:00 — PR merged\n")
	prose, err := os.ReadFile(lines.Path)
	if err != nil {
		t.Fatal(err)
	}

	for _, task := range []Task{lines, old, recorded} {
		if _, err := migrateStatusHistory(task); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		task       Task
		handedOver bool
		before     Status // the status before needs-input
		merged     time.Time
		goAhead    Status // a go-ahead on this status is met; empty for none
	}{
		{lines, true, PROpen, time.Time{}, PROpen},
		{old, true, Merged, at("2026-11-02 13:00"), Merged},
		{recorded, false, Working, time.Time{}, ""},
	} {
		h, err := c.task.StatusHistory()
		if err != nil {
			t.Fatal(err)
		}
		handedOver, _ := c.task.HandedOver()
		before, _ := statusBeforeNeedsInput(c.task)
		if handedOver != c.handedOver || before != c.before || !h.Merged().Equal(c.merged) {
			t.Errorf("%s: handed over %v, before needs-input %s, merged %v; want %v, %s, %v",
				c.task.ID(), handedOver, before, h.Merged(), c.handedOver, c.before, c.merged)
		}
		if c.goAhead == "" {
			continue
		}
		if met, err := (GoAhead{c.task, c.goAhead}).Met(); err != nil || !met {
			t.Errorf("%s: a go-ahead on %s, met = %v, %v; want met", c.task.ID(), c.goAhead, met, err)
		}
	}
	// The older task's followup and merge count as the changes they made.
	h, _ := old.StatusHistory()
	var got []Status
	for _, r := range h {
		got = append(got, r.Status.To)
		if r.By != "crew migrate" {
			t.Errorf("%s's change to %s is by %s, want crew migrate", old.ID(), r.Status.To, r.By)
		}
	}
	if want := []Status{Working, PROpen, AddressingFeedback, PROpen, Merged}; !slices.Equal(got, want) {
		t.Errorf("%s's history = %v, want %v", old.ID(), got, want)
	}
	if h, _ := recorded.StatusHistory(); len(h) != 2 {
		t.Errorf("%s had its history already; now it has %d changes, want 2", recorded.ID(), len(h))
	}
	if after, _ := os.ReadFile(lines.Path); string(after) != string(prose) {
		t.Errorf("migrating changed %s's file:\n%s", lines.ID(), after)
	}
	// A second run, after one cut short, records nothing twice.
	if again, err := migrateStatusHistory(lines); err != nil || again {
		t.Errorf("a second migration of %s recorded = %v, %v; want nothing", lines.ID(), again, err)
	}
}
