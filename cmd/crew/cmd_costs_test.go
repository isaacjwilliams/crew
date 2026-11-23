package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// costsDay is the local time at "YYYY-MM-DD HH:MM".
func costsDay(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation(logTime, s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// assistantLine is a transcript's line for one content block of an assistant
// message, in the shape Claude Code writes it: the message's ID, model and
// usage, and the block. A million output tokens of Opus cost $20.
func assistantLine(id, model string, at time.Time, output int, block string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":%q,"content":[%s],`+
		`"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`+"\n",
		at.Format(time.RFC3339Nano), id, model, block, output)
}

// toolUseBlock is a tool call in an assistant message.
func toolUseBlock(id, name, input string) string {
	return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":%s}`, id, name, input)
}

// writeTranscript writes lines to path under dir, making its folders.
func writeTranscript(t *testing.T, dir, path string, lines ...string) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Each message counts once, however many times and in however many sessions
// Claude Code wrote it, with its fullest usage, and only when it was sent in
// the range: from midnight on the first day to midnight after the last.
func TestCostsCountEachMessageInTheRangeOnce(t *testing.T) {
	dir := t.TempDir()
	from, to := costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00")
	opus := "claude-opus-5-5"
	writeTranscript(t, dir, "-crew-lead/s1.jsonl",
		`{"type":"user","timestamp":"2026-11-18T10:00:00Z","message":{"role":"user","content":"Go."}}`+"\n",
		assistantLine("msg_1", opus, costsDay(t, "2026-11-18 10:00"), 400_000, `{"type":"thinking"}`),
		assistantLine("msg_1", opus, costsDay(t, "2026-11-18 10:00"), 1_000_000, `{"type":"text"}`),
		assistantLine("msg_2", "<synthetic>", costsDay(t, "2026-11-18 10:01"), 1_000_000, `{"type":"text"}`),
		assistantLine("msg_3", opus, from.Add(-time.Second), 1_000_000, `{"type":"text"}`),
		assistantLine("msg_4", opus, from, 1_000_000, `{"type":"text"}`),
		assistantLine("msg_5", opus, to.AddDate(0, 0, 1).Add(-time.Second), 1_000_000, `{"type":"text"}`),
		assistantLine("msg_6", opus, to.AddDate(0, 0, 1), 1_000_000, `{"type":"text"}`),
		`{"type":"assistant","message":{"id":"msg_7","usa`)
	// A resumed session starts with the messages it resumed, which may have
	// their counts zeroed.
	writeTranscript(t, dir, "-crew-lead/s2.jsonl",
		assistantLine("msg_1", opus, costsDay(t, "2026-11-18 10:00"), 0, `{"type":"text"}`))

	charges, unread, err := scanTranscripts(dir, map[string]owner{"-crew-lead": {step: stepLead}})
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 5 || unread != 1 {
		t.Errorf("got %d charges and %d lines unread; want msg_1 and msg_3 to msg_6 charged, and the cut-off line unread", len(charges), unread)
	}
	r := costsIn(charges, nil, from, to)
	if r.total != 60 || r.steps[stepLead].dollars != 60 {
		t.Errorf("total = $%v, lead = $%v; want $60 for msg_1, msg_4 and msg_5", r.total, r.steps[stepLead].dollars)
	}
}

// Each message is priced at its model's rates, a cache write at its bucket's,
// and a model there's no rate for is reported with its tokens, not priced.
func TestCostsPriceEachMessageAtItsModelsRates(t *testing.T) {
	var u claudeUsage
	u.Input, u.Output, u.CacheRead, u.CacheWrite = 1_000_000, 1_000_000, 1_000_000, 2_000_000
	u.CacheWriteKept.OneHour = 1_000_000
	for model, want := range map[string]float64{
		// input + output + cache read + 1.25 × input for a 5-minute write + 2 × input for a 1-hour one
		"claude-opus-5-5":   4 + 20 + 0.2 + 5 + 8,
		"claude-sonnet-5-5": 2 + 10 + 0.1 + 2.5 + 4,
		"claude-haiku-5-5":  0.1 + 0.5 + 0.01 + 0.125 + 0.2,
		"claude-fable-5-2":  8 + 40 + 0.2 + 10 + 16,
	} {
		if got := apiRates[model].dollars(u); fmt.Sprintf("%.4f", got) != fmt.Sprintf("%.4f", want) {
			t.Errorf("%s: $%v; want $%v", model, got, want)
		}
	}

	at := costsDay(t, "2026-11-18 10:00")
	charges := []*charge{
		{owner{step: stepLead}, "claude-opus-5-5", at, u},
		{owner{step: stepLead}, "claude-haiku-5-5", at, u},
		{owner{step: stepLead}, "claude-mystery-1", at, u},
	}
	r := costsIn(charges, nil, costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00"))
	var out strings.Builder
	r.print(&out)
	for _, want := range []string{
		"Lead sessions    $38   100%",
		"Not priced, as there's no rate for it: claude-mystery-1, 5,000,000 tokens.",
		"  claude-haiku-5-5: 0.1, 0.5, 0.01\n  claude-opus-5-5: 4, 20, 0.2\nA cache write",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report doesn't say %q:\n%s", want, out.String())
		}
	}
}

// A subagent is charged to its step: in a task's worktree, /simplify's and
// /security-review's agents to theirs, crew's review agents to theirs, even
// when the worker starts them straight after a skill's, and the worker's own
// to a step named by the agent, whatever it was asked to do. A brief review is
// charged to the task whose file it was given, whoever ran it.
func TestCostsChargeEachAgentToItsStep(t *testing.T) {
	dir := t.TempDir()
	at := costsDay(t, "2026-11-18 10:00")
	opus := "claude-opus-5-5"
	task := "-work-p-001-a"
	// Each message's output tokens say which session it's from.
	writeTranscript(t, dir, task+"/main.jsonl",
		assistantLine("m1", opus, at, 1, toolUseBlock("t1", "Agent", `{"subagent_type":"general-purpose","description":"Review the diff"}`)),
		assistantLine("m2", opus, at, 1, toolUseBlock("t2", "Skill", `{"skill":"simplify"}`)),
		assistantLine("m3", opus, at, 1, toolUseBlock("t3", "Bash", `{"command":"git diff"}`)),
		assistantLine("m4", opus, at, 1, toolUseBlock("simplify-1", "Agent", `{}`)),
		assistantLine("m4", opus, at, 1, toolUseBlock("simplify-2", "Agent", `{}`)),
		assistantLine("m4", opus, at, 1, toolUseBlock("observer", "Agent", `{}`)),
		assistantLine("m4", opus, at, 1, toolUseBlock("test-review", "Agent", `{}`)),
		assistantLine("m5", opus, at, 1, toolUseBlock("t5", "Read", `{}`)),
		assistantLine("m6", opus, at, 1, toolUseBlock("own-2", "Agent", `{}`)),
		assistantLine("m7", opus, at, 1, toolUseBlock("t7", "Skill", `{"skill":"security-review"}`)),
		assistantLine("m8", opus, at, 1, toolUseBlock("security-1", "Task", `{}`)),
	)
	subagent := func(folder, id, agentType, toolUseID, prompt string, output int) {
		path := folder + "/main/subagents/agent-" + id
		if agentType != "" {
			builtIn := agentType == "general-purpose" || agentType == "Explore"
			writeTranscript(t, dir, path+".meta.json", fmt.Sprintf(`{"agentType":%q,"isBuiltIn":%t,"description":"Review it","toolUseId":%q}`, agentType, builtIn, toolUseID))
		}
		writeTranscript(t, dir, path+".jsonl",
			fmt.Sprintf(`{"type":"user","timestamp":"2026-11-18T10:00:00Z","message":{"role":"user","content":%q}}`+"\n", prompt),
			assistantLine(folder+"/"+id, opus, at, output, `{"type":"text"}`))
	}
	subagent(task, "own-1", "general-purpose", "t1", "Review the diff for bugs.", 10)
	subagent(task, "simplify-1", "general-purpose", "simplify-1", "Look for reuse.", 11)
	subagent(task, "simplify-2", "Explore", "simplify-2", "Look for waste.", 12)
	subagent(task, "own-2", "general-purpose", "own-2", "Find the callers.", 13)
	subagent(task, "security-1", "general-purpose", "security-1", "Check for injection.", 14)
	subagent(task, "observer", "casual-observer", "observer", "Base origin/main.", 15)
	subagent(task, "test-review", "test-review", "test-review", "Review the tests.", 19)
	subagent(task, "reviewer", "reviewer", "t10", "Scope: the PR.", 16)
	subagent(task, "explore", "Explore", "t11", "Find it.", 17)
	subagent(task, "unrecorded", "", "", "Do it.", 18)
	subagent("-crew-lead", "brief", "brief-reviewer", "t12", "Review /home/u/crew/projects/p/tasks/001-a.md.", 20)
	subagent("-crew-lead", "helper", "general-purpose", "t13", "Look at /home/u/crew/projects/p/tasks/001-a.md.", 21)
	subagent("-work-p-_goal-g", "plan", "plan-reviewer", "t14", "Review /home/v/crew/projects/p/tasks/002-b.md.", 22)
	subagent("-home-u-Code-elsewhere", "reviewer", "reviewer", "t15", "Review it.", 23)

	charges, _, err := scanTranscripts(dir, map[string]owner{
		task:              {stepWorker, "p/001-a"},
		"-crew-lead":      {step: stepLead},
		"-work-p-_goal-g": {step: stepGoalLead},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]owner{}
	for _, c := range charges {
		got[c.usage.Output] = c.owner
	}
	for output, want := range map[int]owner{
		1:  {stepWorker, "p/001-a"},
		10: {"general-purpose", "p/001-a"},
		11: {stepExhale, "p/001-a"},
		12: {stepExhale, "p/001-a"},
		13: {"general-purpose", "p/001-a"},
		14: {stepSecurityReview, "p/001-a"},
		15: {stepPrePRReview, "p/001-a"},
		16: {stepAutomatedReview, "p/001-a"},
		17: {"Explore", "p/001-a"},
		18: {"unnamed agent", "p/001-a"},
		19: {"test-review", "p/001-a"},
		20: {stepBriefReview, "p/001-a"},
		21: {step: stepLead},
		22: {stepBriefReview, "p/002-b"},
		23: {step: stepOther},
	} {
		if got[output] != want {
			t.Errorf("the message with %d output tokens is charged to %+v; want %+v", output, got[output], want)
		}
	}
}

// A median counts the whole of each task that finished in the range, over the
// tasks that ran the step. A task that began before the transcripts do, or
// has none, is left out, and the report says how many were.
func TestCostsMedianOverTasksThatFinishedInTheRange(t *testing.T) {
	task := func(id, created, finished string) Task {
		return taskFile(t, id, "---\nstatus: merged\ncreated: "+costsDay(t, created).Format(time.RFC3339)+
			"\n---\n## Log\n- "+finished+" — status → merged\n")
	}
	tasks := map[string]Task{
		"p/a":       task("a", "2026-11-10 09:00", "2026-11-18 12:00"),
		"p/b":       task("b", "2026-11-14 09:00", "2026-11-22 23:59"),
		"p/before":  task("before", "2026-11-10 09:00", "2026-11-15 23:59"),
		"p/after":   task("after", "2026-11-14 09:00", "2026-11-23 00:00"),
		"p/old":     task("old", "2026-10-30 09:00", "2026-11-19 12:00"),
		"p/nowhere": task("nowhere", "2026-11-15 09:00", "2026-11-20 12:00"),
		"p/open":    taskFile(t, "open", "---\nstatus: pr-open\ncreated: 2026-11-15T09:00:00Z\n---\n## Log\n"),
	}
	opus := func(task, step, at string, dollars int) *charge {
		var u claudeUsage
		u.Output = dollars * 50_000
		return &charge{owner{step, task}, "claude-opus-5-5", costsDay(t, at), u}
	}
	charges := []*charge{
		opus("", stepLead, "2026-11-01 09:00", 5),
		opus("p/a", stepWorker, "2026-11-12 09:00", 20),
		opus("p/a", stepWorker, "2026-11-17 09:00", 20),
		opus("p/a", stepExhale, "2026-11-17 10:00", 3),
		opus("p/b", stepWorker, "2026-11-20 09:00", 60),
		opus("p/before", stepWorker, "2026-11-15 09:00", 100),
		opus("p/after", stepWorker, "2026-11-20 09:00", 100),
		opus("p/old", stepWorker, "2026-11-19 09:00", 100),
		opus("p/open", stepWorker, "2026-11-20 09:00", 100),
	}
	r := costsIn(charges, tasks, costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00"))
	var out strings.Builder
	r.print(&out)
	want := `Spend from 2026-11-16 to 2026-11-22, at API rates, from this machine's transcripts:

                                          median
  step             total  share  tasks  per task
  Worker sessions   $380  99.2%      2       $50
  Exhale           $3.00   0.8%      1     $3.00

  All               $383   100%      2       $52

Medians are of whole tasks, over the 2 that merged or were done in the range: each step's over the tasks that ran it. 2 more are left out, as some of their sessions aren't in this machine's transcripts.
`
	if got := out.String(); !strings.HasPrefix(got, want) {
		t.Errorf("report:\n%s\nwant it to start:\n%s", got, want)
	}
	if strings.Contains(out.String(), "transcripts begin") {
		t.Errorf("the report says when the transcripts begin, though the range starts after:\n%s", out.String())
	}

	out.Reset()
	costsIn(charges, tasks, costsDay(t, "2026-10-25 00:00"), costsDay(t, "2026-11-22 00:00")).print(&out)
	if want := "This machine's transcripts begin at 2026-11-01 09:00: there's nothing from before then.\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report doesn't say %q:\n%s", want, out.String())
	}
}
