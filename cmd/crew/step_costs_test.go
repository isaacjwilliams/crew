package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeTranscript writes Claude Code transcripts for tests: each line it adds is a
// minute after the last.
type fakeTranscript struct {
	t     *testing.T
	path  string
	at    time.Time
	lines []string
}

// newFakeSession is a main session of a worker in worktree wt, under transcripts.
func newFakeSession(t *testing.T, transcripts, wt, id string, start time.Time) *fakeTranscript {
	return &fakeTranscript{t: t, path: filepath.Join(transcripts, transcriptFolder(wt), id+".jsonl"), at: start}
}

// subagent is a subagent of s, of the given type, started by the tool call
// toolUseID. Claude Code keeps a subagent's own subagents beside it.
func (s *fakeTranscript) subagent(id, agentType, toolUseID string) *fakeTranscript {
	dir := filepath.Join(strings.TrimSuffix(s.path, ".jsonl"), "subagents")
	if filepath.Base(filepath.Dir(s.path)) == "subagents" {
		dir = filepath.Dir(s.path)
	}
	writeMeta(s.t, filepath.Join(dir, "agent-"+id+".meta.json"), map[string]string{"agentType": agentType, "toolUseId": toolUseID})
	return &fakeTranscript{t: s.t, path: filepath.Join(dir, "agent-"+id+".jsonl"), at: s.at}
}

func (s *fakeTranscript) add(line map[string]any) *fakeTranscript {
	s.at = s.at.Add(time.Minute)
	line["timestamp"] = s.at.UTC().Format(time.RFC3339)
	b, _ := json.Marshal(line)
	s.lines = append(s.lines, string(b))
	return s
}

// prompt adds a prompt sent to the session.
func (s *fakeTranscript) prompt(text string) *fakeTranscript {
	return s.add(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

// reply adds message id's reply, which took output tokens and makes the tool
// calls given as name and input pairs: "Skill", `{"skill":"simplify"}`. Its
// tool calls' IDs are id/1, id/2 and so on.
func (s *fakeTranscript) reply(id string, output int, calls ...string) *fakeTranscript {
	content := []map[string]any{{"type": "text", "text": "ok"}}
	for i := 0; i+1 < len(calls); i += 2 {
		content = append(content, map[string]any{"type": "tool_use", "id": fmt.Sprintf("%s/%d", id, i/2+1), "name": calls[i], "input": json.RawMessage(calls[i+1])})
	}
	return s.add(map[string]any{"type": "assistant", "message": map[string]any{
		"id": id, "model": "claude-opus-5-5", "role": "assistant", "content": content,
		"usage": map[string]int{"input_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": output},
	}})
}

func (s *fakeTranscript) write() {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(s.path, []byte(strings.Join(s.lines, "\n")+"\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func writeMeta(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// outputs is each step, agent and model's output tokens, as "step, agent:
// output".
func outputs(tc TaskCost) []string {
	var got []string
	for _, s := range tc.Steps {
		got = append(got, fmt.Sprintf("%s, %s: %d", s.Step, s.Agent, s.Tokens.Output))
	}
	return got
}

// totalOf is the tokens in every transcript under dir, as claudeRunCost counts
// them on their own, with no steps.
func totalOf(t *testing.T, dir string) int {
	t.Helper()
	var all []byte
	filepath.WalkDir(dir, func(path string, _ os.DirEntry, _ error) error {
		if strings.HasSuffix(path, ".jsonl") {
			b, _ := os.ReadFile(path)
			all = append(all, b...)
		}
		return nil
	})
	cost, err := claudeRunCost(all)
	if err != nil {
		t.Fatal(err)
	}
	return cost.tokens.total()
}

func stepTotal(tc TaskCost) int {
	n := 0
	for _, s := range tc.Steps {
		n += s.Tokens.total()
	}
	return n
}

const (
	implementerPrompt = "You are the implementer for task 001-feature in project webapp.\nTask file: /home/u/crew/projects/webapp/tasks/001-feature.md"
	evidenceEdit      = `{"file_path":"/home/u/crew/projects/webapp/tasks/001-feature.md","old_string":"## Evidence","new_string":"## Evidence\n### 1. It works (on abc1234)"}`
)

// Every step of crew's process, through a first run, a round with both red CI
// and comments, a session interrupted and picked up fresh, a lead's rebase note, a restack and a
// repo's own review agent, is charged where it ran, and every token once.
func TestTaskCostChargesEachStep(t *testing.T) {
	transcripts, wt := t.TempDir(), "/work/webapp/001-feature"
	task := taskFile(t, "001-feature", "## Review rounds\n"+
		"### Round 1 — 2026-11-10 10:00 (PR #12)\n\n- [ ] CI failed on aaa1111: test\n\n**Conversation comments**\n- [ ] comment 5 — [link](u5)\n  > rename it\n\n"+
		"### Round 2 — 2026-11-11 10:00 (PR #12)\n\n- [ ] Automated review requested (crew review)\n\n"+
		"## Log\n- 2026-11-09 09:00 — dispatched: worker aaaa1111 (session aaaa1111-0000) in /work/webapp/001-feature on claude/feature\n")
	start := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)

	first := newFakeSession(t, transcripts, wt, "aaaa1111-0000", start).
		prompt(implementerPrompt).
		reply("m1", 1).
		reply("m1", 2). // the same message, written again with its final usage
		reply("m2", 4, "Agent", `{"subagent_type":"test-review"}`).
		reply("m3", 8, "Skill", `{"skill":"simplify"}`).
		reply("m4", 16, "Agent", `{"subagent_type":"casual-observer"}`).
		reply("m5", 32, "Skill", `{"skill":"security-review"}`).
		reply("m6", 64, "Edit", evidenceEdit).
		reply("m7", 128, "Bash", `{"command":"gh pr create --draft --base main"}`).
		reply("m8", 256, "Skill", `{"skill":"crew:pr-review"}`).
		reply("m9", 512, "Agent", `{"subagent_type":"reviewer"}`).
		reply("m10", 1024).
		prompt("New input for task 001-feature (task file: x). New review feedback is in round 1 under '## Review rounds'; follow your review-round workflow.").
		reply("m11", 2048, "Bash", `{"command":"crew ci-summary 001-feature aaa1111 /tmp/ci.md"}`).
		reply("m12", 4096).
		prompt("a person's message carries on the step").
		reply("m13", 8192)
	first.subagent("t1", "test-review", "m2/1").reply("t1", 1<<14).write()
	first.subagent("o1", "casual-observer", "m4/1").reply("o1", 1<<15).write()
	reviewer := first.subagent("r1", "reviewer", "m9/1")
	reviewer.reply("r1", 1<<16, "Agent", `{"subagent_type":"Explore"}`).write()
	// A subagent's own subagent is charged where the subagent was.
	reviewer.subagent("e1", "Explore", "r1/1").reply("e1", 1<<17).write()
	first.write()

	// Interrupted, then picked up in a fresh session for round 2, a lead's
	// note asking for a rebase, and a restack.
	newFakeSession(t, transcripts, wt, "bbbb2222-0000", first.at.Add(time.Hour)).
		prompt("You are picking up task 001-feature in project webapp, which already has work on its branch.\nTask file: x\nNew input for task 001-feature (task file: x). New review feedback is in round 2 under '## Review rounds'; follow your review-round workflow.").
		reply("p1", 1<<18).
		prompt("New input for task 001-feature (task file: x). Note from the lead (also check Decisions in the task file): Rebase claude/feature onto origin/main and force-push with lease.").
		reply("p2", 1<<19).
		prompt("New input for task 001-feature (task file: x). Restack (main moved): run git rebase --onto abc def").
		reply("p3", 1<<20).
		write()

	tc, err := taskCost(task, transcripts, wt, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		// A message is charged to the step it's written in, and its tool
		// calls start the next: m3 calls /simplify from implementing.
		"Implementing, worker: 14", // m1 counted once, with its last usage
		"Exhale, worker: 16",
		"Pre-PR review, casual-observer: 32768",
		"Pre-PR review, worker: 32",
		"Security review, worker: 64",
		"Evidence and PR, worker: 384",
		"Automated review, Explore: 131072",
		"Automated review, reviewer: 65536",
		"Automated review, worker: " + fmt.Sprint(512+1024+1<<18), // the review, and round 2's request for another
		"Review rounds, worker: 12288",
		"CI rounds, worker: 2048",
		"Restacks and conflicts, worker: " + fmt.Sprint(1<<19+1<<20),
		"test-review, test-review: 16384",
	}
	if got := outputs(tc); !slices.Equal(got, want) {
		t.Errorf("steps =\n%q\nwant\n%q", got, want)
	}
	if got, want := stepTotal(tc), totalOf(t, transcripts); got != want {
		t.Errorf("the steps add up to %d tokens; the transcripts hold %d", got, want)
	}
	if !slices.Equal(tc.NotRun, []string{stepBriefReview}) || tc.Missing != nil {
		t.Errorf("not run %q, missing %q; want only the brief review not run", tc.NotRun, tc.Missing)
	}
}

// A brief review is found by the task file its prompt names, whichever agent
// name, lead or home ran it, and never charged to a task of the same number in
// another project.
func TestBriefReviews(t *testing.T) {
	transcripts := t.TempDir()
	start := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	lead := newFakeSession(t, transcripts, "/home/u/crew/lead", "1111", start)
	lead.subagent("b1", "brief-reviewer", "x/1").prompt("Review the brief in /home/u/crew/projects/webapp/tasks/001-feature.md").reply("b1", 1).write()
	lead.subagent("b2", "plan-reviewer", "x/2").prompt("Review /home/isaac/agent-ledger/projects/webapp/tasks/001-feature.md, the old home").reply("b2", 2).write()
	lead.subagent("b3", "brief-reviewer", "x/3").prompt("Review /home/u/crew/projects/api/tasks/001-feature.md").reply("b3", 4).write()
	lead.subagent("o1", "Explore", "x/4").prompt("Look at /home/u/crew/projects/webapp/tasks/001-feature.md").reply("o1", 8).write()
	goalLead := newFakeSession(t, transcripts, "/work/webapp/goal-001-launch", "2222", start)
	goalLead.subagent("b4", "brief-reviewer", "y/1").prompt("Review /home/u/crew/projects/webapp/tasks/001-feature.md again").reply("b4", 16).write()

	briefs, err := briefReviews(transcripts)
	if err != nil {
		t.Fatal(err)
	}
	task := taskFile(t, "001-feature", "## Log\n- 2026-11-09 08:00 — Brief review: 3 raised, 2 taken\n")
	tc, err := taskCost(task, transcripts, "/work/webapp/001-feature", briefs["webapp/001-feature"])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Brief review, brief-reviewer: 17", "Brief review, plan-reviewer: 2"}
	if got := outputs(tc); !slices.Equal(got, want) {
		t.Errorf("steps = %q\nwant %q", got, want)
	}
	if len(briefs["api/001-feature"]) != 1 {
		t.Errorf("api/001-feature has %d brief reviews; want its own one", len(briefs["api/001-feature"]))
	}
}

// A step a task didn't run is kept as not run: here pre-PR review, turned off,
// with exhale run or not.
func TestTaskCostNotRun(t *testing.T) {
	for _, exhaled := range []bool{true, false} {
		transcripts, wt := t.TempDir(), "/work/webapp/001-feature"
		log := "## Log\n- 2026-11-09 09:00 — dispatched: worker aaaa1111 (session aaaa1111-0000) in /work/webapp/001-feature on claude/feature\n"
		s := newFakeSession(t, transcripts, wt, "aaaa1111-0000", time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)).
			prompt(implementerPrompt).reply("m1", 1)
		if exhaled {
			log += "- 2026-11-09 10:00 — Exhale: 2 raised, 1 taken\n"
			s.reply("m2", 2, "Skill", `{"skill":"simplify"}`).reply("m3", 4)
		}
		s.reply("m4", 8, "Bash", `{"command":"gh pr create"}`).reply("m5", 16).write()
		task := taskFile(t, "001-feature", "---\npre_pr_review: no\n---\n"+log)

		tc, err := taskCost(task, transcripts, wt, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{stepBriefReview, stepExhale, stepPrePRReview, stepSecurityReview, stepAutomatedReview, stepReviewRounds, stepCIRounds, stepRestacks}
		if exhaled {
			want = slices.DeleteFunc(want, func(s string) bool { return s == stepExhale })
		}
		if !slices.Equal(tc.NotRun, want) || tc.Missing != nil {
			t.Errorf("exhaled %v: not run %q, missing %q\nwant not run %q", exhaled, tc.NotRun, tc.Missing, want)
		}
	}
}

// What ran but left no transcript on this machine is kept as missing, never
// as zero or not run: a worker session the Log names, or a brief review it
// records.
func TestTaskCostMissing(t *testing.T) {
	transcripts, wt := t.TempDir(), "/work/webapp/001-feature"
	newFakeSession(t, transcripts, wt, "aaaa1111-0000", time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)).
		prompt(implementerPrompt).reply("m1", 1).write()
	task := taskFile(t, "001-feature", "## Log\n"+
		"- 2026-11-09 08:00 — Brief review: 1 raised, 1 taken\n"+
		"- 2026-11-09 09:00 — dispatched: worker aaaa1111 (session aaaa1111-0000) in /work/webapp/001-feature on claude/feature\n"+
		"- 2026-11-10 09:00 — followup (fresh: moved): worker cccc3333\n"+
		"- 2026-11-10 10:00 — followup (resume): worker cccc3333\n")

	tc, err := taskCost(task, transcripts, wt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{stepBriefReview, "worker session cccc3333"}; !slices.Equal(tc.Missing, want) {
		t.Errorf("missing %q; want %q", tc.Missing, want)
	}
	// The missing session could have run any of the worker's steps.
	if tc.NotRun != nil {
		t.Errorf("not run %q; want none, with a session missing", tc.NotRun)
	}
	if got := outputs(tc); !slices.Equal(got, []string{"Implementing, worker: 1"}) {
		t.Errorf("steps = %q", got)
	}
}

// What String writes, Cost reads back.
func TestCostSection(t *testing.T) {
	kept := TaskCost{
		Steps: []StepCost{
			{stepImplementing, workerAgent, "claude-opus-5-5", claudeUsage{24, 57045, 392958, 10639}},
			{"test-review", "test-review", "claude-sonnet-5", claudeUsage{1, 2, 3, 4}},
		},
		NotRun:  []string{stepExhale},
		Missing: []string{stepBriefReview, "worker session cccc3333"},
	}
	task := taskFile(t, "001-feature", "## Cost\n\n## Log\n")
	if _, ok := task.Cost(); ok {
		t.Error("an empty section reads as kept")
	}
	if err := task.AppendToSection("## Cost", kept.String()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Section("## Cost"), "- Implementing, worker, claude-opus-5-5: 24 input, 57,045 cache write, 392,958 cache read, 10,639 output\n") {
		t.Errorf("section:\n%s", task.Section("## Cost"))
	}
	got, ok := task.Cost()
	if !ok || got.String() != kept.String() {
		t.Errorf("read back (kept %v):\n%s\nwant\n%s", ok, got, kept)
	}
}
