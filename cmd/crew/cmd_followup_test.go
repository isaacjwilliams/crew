package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// followupCrew is a home that runs the crew, with a git repo of its own, and
// project p cloned at repo: its origin is a bare repo holding main and the
// task's branch, agent/task, and its worktrees go under work. The suite's claude
// and wt stubs come first on PATH, after the home's bin/ (claudeSays), keeping
// their state in HOME.
type followupCrew struct {
	*Crew
	repo, work string
}

// The task followupCrew.task writes, and its branch.
const (
	followupTask   = "001-task"
	followupBranch = "agent/task"
)

func newFollowupCrew(t *testing.T, tool string) followupCrew {
	t.Helper()
	stubs, err := filepath.Abs("../../tests/stubs")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}

	c := &Crew{Home: t.TempDir(), Host: "here"}
	t.Setenv("PATH", strings.Join([]string{c.path("bin"), stubs, os.Getenv("PATH")}, string(os.PathListSeparator)))
	defaults, err := os.ReadFile("../../config/defaults.env")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, c, string(defaults), "config", "defaults.env")
	writeFile(t, c, "{}\n", "config", "worker-settings.json")
	writeFile(t, c, "here\n", "host")
	if c.Settings, err = sharedSettings(c.Home); err != nil {
		t.Fatal(err)
	}

	origin, repo := t.TempDir(), filepath.Join(t.TempDir(), "repo")
	runIn(t, origin, "git", "init", "-q", "--bare", "-b", "main")
	runIn(t, origin, "git", "clone", "-q", origin, repo)
	g := gitRepo{t, repo}
	g.git("commit", "-q", "--allow-empty", "-m", "init")
	g.git("push", "-q", "origin", "HEAD:main", "HEAD:"+followupBranch)
	g.git("fetch", "-q", "origin")

	f := followupCrew{Crew: c, repo: repo, work: t.TempDir()}
	writeFile(t, c, "REPO="+repo+"\nWORK_ROOT="+f.work+"\nDEFAULT_BRANCH=main\nWORKTREE_TOOL="+tool+"\n", "projects", "p", "project.env")
	runIn(t, c.Home, "git", "init", "-q")
	runIn(t, c.Home, "git", "add", "-A")
	runIn(t, c.Home, "git", "commit", "-qm", "home")
	return f
}

// worktree is where this machine keeps the task's worktree.
func (f followupCrew) worktree() string { return filepath.Join(f.work, "p", followupTask) }

// task writes the task, a working one with a worker, job w1, whose worktree is
// on this machine, then changes or adds the front matter lines given.
func (f followupCrew) task(t *testing.T, fields ...string) Task {
	t.Helper()
	if _, err := os.Stat(f.worktree()); err != nil {
		runIn(t, f.repo, "git", "worktree", "add", "-q", f.worktree(), followupBranch)
	}
	base := []string{"project: p", "status: working", "base: main", "branch: " + followupBranch, "worktree: " + f.worktree(),
		"worker_name: crew-p-" + followupTask, "worker_job: w1", "worker_session: w1-session"}
	task := reportTask(t, f.Crew, "p", followupTask, false, base...)
	for _, field := range fields {
		k, v, _ := strings.Cut(field, ": ")
		if err := task.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

// workers has claude agents list w1 in state, with its process open or not.
func (f followupCrew) workers(t *testing.T, state string, open bool) {
	t.Helper()
	w := map[string]any{"id": "w1", "kind": "background", "state": state, "sessionId": "w1-session", "pid": nil}
	if open {
		w["pid"], w["status"] = 4242, "busy"
	}
	b, err := json.Marshal([]any{w})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "agents.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeSays puts a claude of the home's own first on PATH, which lists no
// workers and prints out for anything else, exiting with code.
func (f followupCrew) claudeSays(t *testing.T, out string, code int) {
	t.Helper()
	writeFile(t, f.Crew, fmt.Sprintf("#!/bin/sh\n[ \"$1\" = agents ] && { echo '[]'; exit 0; }\necho '%s'\nexit %d\n", out, code), "bin", "claude")
}

func lastLog(task Task) string {
	es := task.LogEntries()
	if len(es) == 0 {
		return ""
	}
	return es[len(es)-1].msg
}

func TestFollowupSendsANote(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.workers(t, "done", false)
	task := f.task(t)
	if err := f.followup(task, followupWith{note: "carry on"}); err != nil {
		t.Fatalf("followup refused: %v", err)
	}
	if got := lastLog(task); !strings.HasPrefix(got, "followup (fresh") || !strings.HasSuffix(got, "— note: carry on") {
		t.Errorf("the Log says %q, want a fresh followup with the note", got)
	}
	if task.WorkerJob() == "w1" || task.WorkerJob() == "" {
		t.Errorf("worker_job is %q, want the fresh worker's", task.WorkerJob())
	}
}

func TestFollowupRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(f followupCrew)
		fields []string
		state  string
		open   bool
		want   string
	}{
		{name: "no worker", fields: []string{"worker_job: ", "worktree: "}, state: "done",
			want: "001-task has no worker yet; dispatch it first"},
		{name: "a busy worker", state: "working", open: true,
			want: "worker w1 is busy (working). Wait for it, answer it in your terminal with crew attach 001-task, or SendMessage crew-p-001-task a note"},
		{name: "a busy worker with a round waiting", fields: []string{"review_rounds: 2", "last_round_sent: 1"}, state: "working", open: true,
			want: "worker w1 is busy (working). crew tick sends round 2 once it's free"},
		{name: "a finished task", fields: []string{"status: merged"}, state: "done",
			want: "001-task is merged; not resuming its worker"},
		{name: "a paused project", setup: func(f followupCrew) { writeFile(t, f.Crew, "by: crew pause\n", "projects", "p", "paused") }, state: "done",
			want: "p is paused; 'crew resume --project p' lets its work go on"},
		{name: "a machine that doesn't run the crew", setup: func(f followupCrew) { writeFile(t, f.Crew, "laptop\n", "host") }, state: "done",
			want: "crew runs on laptop; this machine can only read (crew host up --take moves it here)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFollowupCrew(t, "git")
			f.workers(t, tc.state, tc.open)
			task := f.task(t, tc.fields...)
			if tc.setup != nil {
				tc.setup(f)
			}
			err := f.followup(task, followupWith{note: "carry on"})
			if err == nil || err.Error() != tc.want {
				t.Errorf("followup said %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFollowupWhenClaudeStartsNoSession(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.claudeSays(t, "Error: out of credits", 1)
	task := f.task(t)
	err := f.followup(task, followupWith{note: "carry on"})
	if err == nil || !strings.HasPrefix(err.Error(), "could not start a session") {
		t.Errorf("followup said %v, want that it couldn't start a session", err)
	}
	if task.WorkerJob() != "w1" {
		t.Errorf("worker_job is %q, want w1 still", task.WorkerJob())
	}
}

func TestFollowupWhenClaudeStartsASessionCrewCantRecord(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.claudeSays(t, "backgrounded · not/a/job · stub", 0)
	task := f.task(t)
	err := f.followup(task, followupWith{note: "carry on"})
	if err == nil || !strings.Contains(err.Error(), "claude started a session but crew can't read its job ID") {
		t.Errorf("followup said %v, want that claude started a session crew couldn't record", err)
	}
	if task.WorkerJob() != "w1" {
		t.Errorf("worker_job is %q, want w1 still", task.WorkerJob())
	}
}

// A task whose worktree is on another machine: its branch is on origin only.
func TestFollowupRebuildsAWorktreeThatIsntHere(t *testing.T) {
	for _, tool := range []string{"git", "worktrunk"} {
		t.Run(tool, func(t *testing.T) {
			f := newFollowupCrew(t, tool)
			f.workers(t, "done", false)
			task := f.task(t, "worktree: /elsewhere/"+followupTask)
			runIn(t, f.repo, "git", "worktree", "remove", f.worktree())
			runIn(t, f.repo, "git", "branch", "-D", followupBranch)
			if err := f.followup(task, followupWith{note: "carry on"}); err != nil {
				t.Fatalf("followup refused: %v", err)
			}
			if task.Worktree() != f.worktree() || !isDir(f.worktree()) {
				t.Errorf("the worktree is %q (made: %v), want %s", task.Worktree(), isDir(f.worktree()), f.worktree())
			}
			if log := task.Section("## Log"); !strings.Contains(log, "recreated worktree from origin/"+followupBranch) {
				t.Errorf("the Log doesn't say how the worktree came back:\n%s", log)
			}
		})
	}
}

// A task whose worktree isn't here and can't be made: git finds its branch
// checked out elsewhere, and wt has hooks that need approval.
func TestFollowupWhenTheWorktreeCantBeMade(t *testing.T) {
	for _, tool := range []string{"git", "worktrunk"} {
		t.Run(tool, func(t *testing.T) {
			f := newFollowupCrew(t, tool)
			f.workers(t, "done", false)
			task := f.task(t, "worktree: /elsewhere/"+followupTask)
			runIn(t, f.repo, "git", "worktree", "remove", f.worktree())
			if tool == "git" {
				runIn(t, f.repo, "git", "worktree", "add", "-q", filepath.Join(t.TempDir(), "other"), followupBranch)
			} else {
				runIn(t, f.repo, "git", "branch", "-D", followupBranch)
				if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "wt-unapproved"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.followup(task, followupWith{note: "carry on"}); err == nil {
				t.Fatal("followup sent the note")
			}
			if task.WorkerJob() != "w1" || isDir(f.worktree()) {
				t.Errorf("worker_job is %q and the worktree made: %v; want w1 and none", task.WorkerJob(), isDir(f.worktree()))
			}
		})
	}
}
