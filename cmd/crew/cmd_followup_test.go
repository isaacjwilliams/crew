package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

// keepMiseDirs points mise at its real directories before a test swaps HOME.
// The stubs run jq, which may be a mise shim, and under a fake HOME mise
// rejects the real config as untrusted, jq exits non-zero and the stubs fail.
// This mirrors tests/lib.sh's isolate_home: change them together. The paths
// are mise's own defaults.
func keepMiseDirs(t *testing.T) {
	t.Helper()
	for _, d := range []struct{ miseVar, xdgVar, homeSubdir string }{
		{"MISE_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"},
		{"MISE_DATA_DIR", "XDG_DATA_HOME", ".local/share"},
		{"MISE_STATE_DIR", "XDG_STATE_HOME", ".local/state"},
		{"MISE_CACHE_DIR", "XDG_CACHE_HOME", ".cache"},
	} {
		if os.Getenv(d.miseVar) != "" {
			continue
		}
		base := os.Getenv(d.xdgVar)
		if base == "" {
			base = filepath.Join(os.Getenv("HOME"), d.homeSubdir)
		}
		t.Setenv(d.miseVar, filepath.Join(base, "mise"))
	}
}

func newFollowupCrew(t *testing.T, tool string) followupCrew {
	t.Helper()
	stubs, err := filepath.Abs("../../tests/stubs")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	keepMiseDirs(t) // before HOME changes: it reads the real one
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

// resumable records the task's worker as one followup would resume: started by
// the launcher followup builds now.
func (f followupCrew) resumable(t *testing.T, task Task) {
	t.Helper()
	p, err := f.loadProject("p")
	if err != nil {
		t.Fatal(err)
	}
	start, err := f.launchWorker(p, task.Model(), task.Effort())
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Set("worker_fingerprint", start.fingerprint); err != nil {
		t.Fatal(err)
	}
}

// followupInProcess runs followup as code inside a long-lived crew process
// does, and fails the test if it writes to the process's stdout or stderr, or
// leaves the ledger lock held when it returns.
func (f followupCrew) followupInProcess(t *testing.T, task Task, with followupWith) (followupSent, []string, error) {
	t.Helper()
	said := watchTerminal(t)
	sent, warnings, err := runInProcess(f.Crew, func(op *Crew) (followupSent, error) { return op.followup(task, with) })
	if out := said(); out != "" {
		t.Errorf("followup wrote to the terminal:\n%s", out)
	}
	lock, lockErr := flock(f.path(".crew.lock"), 0)
	if lockErr != nil {
		t.Errorf("the ledger lock is still held after followup returned: %v", lockErr)
	} else {
		lock.Close()
	}
	return sent, warnings, err
}

// watchTerminal swaps the process's stdout and stderr for a pipe until the
// function it returns is called, which gives back what was written to either.
func watchTerminal(t *testing.T) func() string {
	t.Helper()
	stdout, stderr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	read := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	return func() string {
		os.Stdout, os.Stderr = stdout, stderr
		w.Close()
		defer r.Close()
		return <-read
	}
}

// refused fails the test unless err is a refusal: an error with a reason, not
// one of the outcomes that are neither sent nor refused.
func refused(t *testing.T, err error) {
	t.Helper()
	switch {
	case err == nil:
		t.Fatal("followup sent the note")
	case errors.As(err, new(unrecordedSession)), errors.As(err, new(worktreeError)):
		t.Fatalf("followup ended in %T, want a refusal: %v", err, err)
	case strings.TrimSpace(err.Error()) == "":
		t.Fatal("followup refused without a reason")
	}
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
	if err := f.noteRefusal(task); err != nil {
		t.Errorf("asked first, followup would refuse a note: %v", err)
	}
	sent, warnings, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
	if err != nil {
		t.Fatalf("followup refused: %v", err)
	}
	if got := lastLog(task); !strings.HasPrefix(got, "followup (fresh") || !strings.HasSuffix(got, "— note: carry on") {
		t.Errorf("the Log says %q, want a fresh followup with the note", got)
	}
	if task.WorkerJob() == "w1" || task.WorkerJob() == "" {
		t.Errorf("worker_job is %q, want the fresh worker's", task.WorkerJob())
	}
	want := followupSent{job: task.WorkerJob(), mode: "fresh: its instructions or settings changed"}
	if sent != want || len(warnings) != 0 {
		t.Errorf("followup sent %+v with warnings %q, want %+v and none", sent, warnings, want)
	}
}

// A worker whose session won't resume gets a fresh one, and followup's caller
// hears why.
func TestFollowupSendsANoteWithWarnings(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.workers(t, "done", false)
	task := f.task(t)
	f.resumable(t, task)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "fail-resume"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sent, warnings, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
	if err != nil {
		t.Fatalf("followup refused: %v", err)
	}
	if sent.mode != "fresh" || sent.job != task.WorkerJob() {
		t.Errorf("followup sent %+v, want a fresh worker, %s", sent, task.WorkerJob())
	}
	want := "couldn't resume session w1-session, so crew tried a fresh one instead; claude said:\nError: session not found"
	if len(warnings) != 1 || warnings[0] != want {
		t.Errorf("followup warned %q, want %q", warnings, want)
	}
}

// A second followup from the same process finds the ledger lock free, as do
// other crew commands.
func TestFollowupTwiceInOneProcess(t *testing.T) {
	f := newFollowupCrew(t, "git")
	task := f.task(t)
	for _, note := range []string{"first", "second"} {
		f.workers(t, "done", false)
		if _, _, err := f.followupInProcess(t, task, followupWith{note: note}); err != nil {
			t.Fatalf("followup with the %s note refused: %v", note, err)
		}
		if got := lastLog(task); !strings.HasSuffix(got, "— note: "+note) {
			t.Errorf("the Log says %q, want the %s note", got, note)
		}
	}
}

// A long-lived process outlives a change to crew.env, so each in-process
// followup reads it again, as a crew followup process would.
func TestFollowupReadsCrewEnvEachTime(t *testing.T) {
	f := newFollowupCrew(t, "git")
	task := f.task(t)
	f.workers(t, "done", false)
	if _, _, err := f.followupInProcess(t, task, followupWith{note: "first"}); err != nil {
		t.Fatalf("the first followup refused: %v", err)
	}
	writeFile(t, f.Crew, "not a setting\n", "config", "crew.env")
	f.workers(t, "done", false)
	_, _, err := f.followupInProcess(t, task, followupWith{note: "second"})
	if err == nil || !strings.Contains(err.Error(), "crew.env") {
		t.Errorf("followup after crew.env went bad gave %v, want crew.env's error", err)
	}
	if got := lastLog(task); !strings.HasSuffix(got, "— note: first") {
		t.Errorf("the Log says %q, want only the first note", got)
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
		{name: "a finished task whose worker is busy", fields: []string{"status: merged"}, state: "working", open: true,
			want: "001-task is merged; not resuming its worker"},
		{name: "a finished task in a paused project", fields: []string{"status: merged"}, state: "done",
			setup: func(f followupCrew) { writeFile(t, f.Crew, "by: crew pause\n", "projects", "p", "paused") },
			want:  "001-task is merged; not resuming its worker"},
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
			_, _, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
			refused(t, err)
			if err.Error() != tc.want {
				t.Errorf("followup said %q, want %q", err, tc.want)
			}
			if ask := f.noteRefusal(task); ask == nil || ask.Error() != err.Error() {
				t.Errorf("asked first, followup said %v, want %q", ask, err)
			}
		})
	}
}

func TestFollowupWhenClaudeStartsNoSession(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.claudeSays(t, "Error: out of credits", 1)
	task := f.task(t)
	_, _, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
	refused(t, err)
	if want := "could not start a session; claude said:\nError: out of credits"; err.Error() != want {
		t.Errorf("followup said %q, want %q", err, want)
	}
	if task.WorkerJob() != "w1" {
		t.Errorf("worker_job is %q, want w1 still", task.WorkerJob())
	}
}

// When claude refuses the resume and then the fresh start, the warning about
// the resume mustn't say a session started.
func TestFollowupWhenClaudeResumesNothingAndStartsNoSession(t *testing.T) {
	f := newFollowupCrew(t, "git")
	task := f.task(t)
	f.resumable(t, task)
	f.claudeSays(t, "Error: out of credits", 1)
	_, warnings, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
	refused(t, err)
	if want := "could not start a session; claude said:\nError: out of credits"; err.Error() != want {
		t.Errorf("followup said %q, want %q", err, want)
	}
	want := []string{"couldn't resume session w1-session, so crew tried a fresh one instead; claude said:\nError: out of credits"}
	if !slices.Equal(warnings, want) {
		t.Errorf("followup warned %q, want %q", warnings, want)
	}
}

func TestFollowupWhenClaudeStartsASessionCrewCantRecord(t *testing.T) {
	f := newFollowupCrew(t, "git")
	f.claudeSays(t, "backgrounded · not/a/job · stub", 0)
	task := f.task(t)
	_, _, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
	var unrecorded unrecordedSession
	if !errors.As(err, &unrecorded) || unrecorded.output != "backgrounded · not/a/job · stub" {
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
			sent, _, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
			if err != nil {
				t.Fatalf("followup refused: %v", err)
			}
			if want := "recreated worktree from origin/" + followupBranch; sent.worktree != want {
				t.Errorf("followup says the worktree came back %q, want %q", sent.worktree, want)
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
	for tool, want := range map[string]string{
		"git":       "git couldn't create the worktree: fatal: '" + followupBranch + "' is already used by worktree at",
		"worktrunk": "✗ Cannot prompt for approval in non-interactive environment",
	} {
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
			_, _, err := f.followupInProcess(t, task, followupWith{note: "carry on"})
			if !errors.As(err, new(worktreeError)) || !strings.Contains(err.Error(), want) {
				t.Errorf("followup said %v, want a worktreeError that says %q", err, want)
			}
			if task.WorkerJob() != "w1" || isDir(f.worktree()) {
				t.Errorf("worker_job is %q and the worktree made: %v; want w1 and none", task.WorkerJob(), isDir(f.worktree()))
			}
		})
	}
}
