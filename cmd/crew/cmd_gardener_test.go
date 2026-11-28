package main

import (
	"slices"
	"testing"
	"time"
)

func TestTasksToSort(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation(logTime, s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	// A task with status st, an automated review at 10:00 and a round at 11:00
	// holding round, merged at merged.
	task := func(st Status, round, merged string) Task {
		task := migratedTask(t, "001-a", "---\nstatus: "+string(st)+"\n---\n"+
			"## Automated reviews\n### Automated review 1 — 2026-11-06 10:00\n- Reviewers: Claude reviewer\nNo findings.\n\n"+
			"## Review rounds\n### Round 1 — 2026-11-06 11:00 (PR #3)\n"+round+"\n\n## Log\n")
		changed(t, task, "crew sync", at(merged), Merged)
		return task
	}
	comment := "**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > rename it"
	tasks := []Task{
		task(Merged, comment, "2026-11-06 12:00"),
		task(Merged, comment, "2026-11-06 16:00"),                            // merged in now's minute
		task(Merged, comment, "2026-11-04 12:00"),                            // merged before since
		task(PROpen, comment, "2026-11-06 12:00"),                            // not merged any more
		task(Merged, "- [x] CI failed on aaa1111: test", "2026-11-06 13:00"), // no comment after the review
	}
	picked, newest, err := tasksToSort(tasks, at("2026-11-05 00:00"), at("2026-11-06 16:00").Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range picked {
		got = append(got, p.task.Path)
		if len(p.comments) != 1 || p.comments[0].Comment.Item != "comment 5 — [link](u5)" || p.comments[0].Review.Number != 1 {
			t.Errorf("%s: comments %+v", p.task.Path, p.comments)
		}
	}
	if !slices.Equal(got, []string{tasks[0].Path}) {
		t.Errorf("picked %v, want only %s", got, tasks[0].Path)
	}
	if !newest.Equal(at("2026-11-06 12:00")) {
		t.Errorf("newest = %v, want the picked task's merge time", newest)
	}
	// A minute later, the task merged at 16:00 is picked.
	if picked, _, _ := tasksToSort(tasks, at("2026-11-06 12:00"), at("2026-11-06 16:01")); len(picked) != 1 || picked[0].task.Path != tasks[1].Path {
		t.Errorf("a minute later, picked %v", picked)
	}
}
