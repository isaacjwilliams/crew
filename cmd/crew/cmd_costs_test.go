package main

import (
	"fmt"
	"io"
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
// message, in the shape Claude Code writes it in a session run by claude: the
// message's ID, model and usage, and the block. A million output tokens of
// Opus cost $20.
func assistantLine(id, model string, at time.Time, output int, block string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"entrypoint":"cli","message":{"id":%q,"model":%q,"content":[%s],`+
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

	ts, err := scanTranscripts(dir, map[string]owner{"-crew-lead": {step: stepLead}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.charges) != 5 || ts.unread != 1 {
		t.Errorf("got %d charges and %d lines unread; want msg_1 and msg_3 to msg_6 charged, and the cut-off line unread", len(ts.charges), ts.unread)
	}
	r, err := costsIn(ts, nil, nil, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if r.total != 60 || r.steps[stepLead] != 60 {
		t.Errorf("total = $%v, lead = $%v; want $60 for msg_1, msg_4 and msg_5", r.total, r.steps[stepLead])
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
		{owner{step: stepLead}, &message{"claude-opus-5-5", at, u}},
		{owner{step: stepLead}, &message{"claude-haiku-5-5", at, u}},
		{owner{step: stepLead}, &message{"claude-mystery-1", at, u}},
	}
	r, err := costsIn(transcripts{charges: charges}, nil, nil, costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00"))
	if err != nil {
		t.Fatal(err)
	}
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
// to a step named by the agent, whatever it was asked to do, and all of them
// to the task. A brief review is its own step, charged to the task whose file
// it was given, whoever ran it, and a lead's or goal lead's other agents are
// charged to a step for them and to no task.
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
		// A skill that starts no agents leaves its window open: the transcript
		// doesn't say where its turn ends. The worker's next built-in agent is
		// charged to the skill, which is a known mischarge.
		assistantLine("m9", opus, at, 1, toolUseBlock("t9", "Skill", `{"skill":"simplify"}`)),
		assistantLine("m10", opus, at, 1, toolUseBlock("t10", "Read", `{}`)),
		assistantLine("m11", opus, at, 1, toolUseBlock("own-3", "Agent", `{}`)),
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
	subagent(task, "own-3", "general-purpose", "own-3", "Find the tests.", 24)
	subagent(task, "security-1", "general-purpose", "security-1", "Check for injection.", 14)
	subagent(task, "observer", "casual-observer", "observer", "Base origin/main.", 15)
	subagent(task, "test-review", "test-review", "test-review", "Review the tests.", 19)
	subagent(task, "reviewer", "reviewer", "t10", "Scope: the PR.", 16)
	subagent(task, "explore", "Explore", "t11", "Find it.", 17)
	subagent(task, "unrecorded", "", "", "Do it.", 18)
	subagent("-crew-lead", "brief", "brief-reviewer", "t12", "/home/u/crew/projects/p/tasks/001-a.md", 20)
	subagent("-crew-lead", "helper", "general-purpose", "t13", "Look at /home/u/crew/projects/p/tasks/001-a.md.", 21)
	subagent("-work-p-_goal-g", "helper", "Explore", "t16", "Look around.", 25)
	subagent("-work-p-_goal-g", "plan", "plan-reviewer", "t14", "Review /home/v/crew/projects/p/tasks/002-b.md.", 22)
	subagent("-home-u-Code-elsewhere", "reviewer", "reviewer", "t15", "Review it.", 23)
	subagent("-crew-lead", "unnamed-brief", "brief-reviewer", "t17", "Review the brief I pasted.", 26)

	ts, err := scanTranscripts(dir, map[string]owner{
		task:              {stepWorker, "p/001-a"},
		"-crew-lead":      {step: stepLead},
		"-work-p-_goal-g": {step: stepGoalLead},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]owner{}
	for _, c := range ts.charges {
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
		21: {step: stepLeadAgents},
		22: {stepBriefReview, "p/002-b"},
		23: {step: stepOther},
		24: {stepExhale, "p/001-a"},
		25: {step: stepGoalLeadAgents},
		26: {step: stepBriefReview},
	} {
		if got[output] != want {
			t.Errorf("the message with %d output tokens is charged to %+v; want %+v", output, got[output], want)
		}
	}
}

// The transcripts are complete from when the oldest session Claude Code
// deletes by age was last written: it deletes a session once its transcript
// has gone unwritten for a while, so any session that's gone was last written
// before then. A session still running may hold older messages, and its
// subagents' transcripts go with it, however old. Claude Code keeps an empty
// transcript, and a session started or last continued in Claude Desktop, at
// any age.
func TestCostsAreCompleteFromTheOldestSessionsLastWrite(t *testing.T) {
	dir := t.TempDir()
	opus := "claude-opus-5-5"
	text := `{"type":"text"}`
	writeTranscript(t, dir, "-crew-lead/empty.jsonl")
	writeTranscript(t, dir, "-crew-lead/oldest.jsonl", assistantLine("msg_1", opus, costsDay(t, "2026-10-22 09:00"), 1, text))
	writeTranscript(t, dir, "-crew-lead/running.jsonl", assistantLine("msg_2", opus, costsDay(t, "2026-10-20 09:00"), 1, text))
	writeTranscript(t, dir, "-crew-lead/running/subagents/agent-a.jsonl", assistantLine("msg_3", opus, costsDay(t, "2026-10-01 09:00"), 1, text))
	writeTranscript(t, dir, "-crew-lead/desktop.jsonl",
		assistantLine("msg_4", opus, costsDay(t, "2026-09-30 09:00"), 1, text),
		strings.Replace(assistantLine("msg_5", opus, costsDay(t, "2026-10-01 09:00"), 1, text), `"cli"`, `"claude-desktop"`, 1))
	desktopPrompt := `{"type":"user","timestamp":"2026-10-01T09:00:00Z","entrypoint":"claude-desktop","message":{"role":"user","content":"Go on."}}` + "\n"
	writeTranscript(t, dir, "-crew-lead/started-in-desktop.jsonl", desktopPrompt,
		assistantLine("msg_6", opus, costsDay(t, "2026-10-01 10:00"), 1, text))
	writeTranscript(t, dir, "-crew-lead/continued-in-desktop.jsonl",
		assistantLine("msg_7", opus, costsDay(t, "2026-09-30 10:00"), 1, text), desktopPrompt)
	for path, at := range map[string]string{
		"empty.jsonl":                     "2026-09-18 09:00",
		"oldest.jsonl":                    "2026-10-22 17:00",
		"running.jsonl":                   "2026-11-23 09:00",
		"running/subagents/agent-a.jsonl": "2026-10-01 09:00",
		"desktop.jsonl":                   "2026-10-01 09:00",
		"started-in-desktop.jsonl":        "2026-10-01 10:00",
		"continued-in-desktop.jsonl":      "2026-10-01 09:00",
	} {
		if err := os.Chtimes(filepath.Join(dir, "-crew-lead", path), time.Time{}, costsDay(t, at)); err != nil {
			t.Fatal(err)
		}
	}
	ts, err := scanTranscripts(dir, map[string]owner{"-crew-lead": {step: stepLead}})
	if err != nil {
		t.Fatal(err)
	}
	if want := costsDay(t, "2026-10-22 17:00"); !ts.completeFrom.Equal(want) {
		t.Errorf("complete from %v; want %v, when oldest.jsonl was last written", ts.completeFrom, want)
	}

	// With no session to date them by, the transcripts are complete only from
	// when they were read.
	began := time.Now()
	if err := os.Remove(filepath.Join(dir, "-crew-lead", "oldest.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "-crew-lead", "running.jsonl")); err != nil {
		t.Fatal(err)
	}
	if ts, err = scanTranscripts(dir, map[string]owner{"-crew-lead": {step: stepLead}}); err != nil {
		t.Fatal(err)
	}
	if ts.completeFrom.Before(began) {
		t.Errorf("complete from %v, with no session to date them by; want no earlier than when they were read, %v", ts.completeFrom, began)
	}
}

// A range that starts before the transcripts are complete says from when they
// are, so a short total isn't taken for a cheap week; one that starts after
// doesn't.
func TestCostsSayFromWhenTheTranscriptsAreComplete(t *testing.T) {
	ts := transcripts{completeFrom: costsDay(t, "2026-11-10 09:00")}
	for _, c := range []struct {
		from string
		want bool
	}{{"2026-11-09", true}, {"2026-11-11", false}} {
		var out strings.Builder
		printCosts(t, &out, ts, nil, nil, costsDay(t, c.from+" 00:00"), costsDay(t, "2026-11-15 00:00"))
		const note = "This machine's transcripts are complete only from 2026-11-10 09:00: older sessions may have been deleted.\n"
		if got := strings.Contains(out.String(), note); got != c.want {
			t.Errorf("from %s: report says the transcripts are incomplete = %v; want %v:\n%s", c.from, got, c.want, out.String())
		}
	}
}

// finishedTask is a task that was created and merged at the local times given,
// as "YYYY-MM-DD HH:MM", the merge recorded by crew sync.
func finishedTask(t *testing.T, id, created, merged string) Task {
	t.Helper()
	task := migratedTask(t, id, "---\nstatus: merged\ncreated: "+costsDay(t, created).Format(time.RFC3339)+"\n---\n## Log\n")
	changed(t, task, "crew sync", costsDay(t, merged), Merged)
	return task
}

// printCosts prints the report costsIn makes from what it's given to out.
func printCosts(t *testing.T, out io.Writer, ts transcripts, tasks map[string]Task, elsewhere []tenure, from, to time.Time) {
	t.Helper()
	r, err := costsIn(ts, tasks, elsewhere, from, to)
	if err != nil {
		t.Fatal(err)
	}
	r.print(out)
}

// dollarsLine is an assistant line from a session run by claude whose message
// cost the dollars given at Opus's rates.
func dollarsLine(id string, at time.Time, dollars float64, block string) string {
	return assistantLine(id, "claude-opus-5-5", at, int(dollars*50_000), block)
}

// A step's median counts all a task spent on it, before the range too, over
// the tasks that merged or were done in the range and ran the step. A brief
// review counts toward its task, whichever lead ran it, and a step a task
// didn't run leaves it out of that step's median only.
func TestCostsMedianOverTasksThatFinishedInTheRange(t *testing.T) {
	dir := t.TempDir()
	text := `{"type":"text"}`
	at := func(s string) time.Time { return costsDay(t, s) }
	// a spent $20 before the range and $20 in it, ran /simplify for $3 and had a
	// brief review from the lead for $1: $44 in all. b spent $60 and had a brief
	// review from a goal lead for $2: $62. Both brief reviews were before the
	// range, so their step has a median but nothing spent in it.
	writeTranscript(t, dir, "-work-p-a/s.jsonl",
		dollarsLine("a1", at("2026-11-12 09:00"), 20, text),
		dollarsLine("a2", at("2026-11-17 09:00"), 20, toolUseBlock("t1", "Skill", `{"skill":"simplify"}`)),
		dollarsLine("a3", at("2026-11-17 09:05"), 0, toolUseBlock("simplify-1", "Agent", `{}`)))
	writeTranscript(t, dir, "-work-p-a/s/subagents/agent-simplify-1.meta.json", `{"agentType":"general-purpose","isBuiltIn":true,"toolUseId":"simplify-1"}`)
	writeTranscript(t, dir, "-work-p-a/s/subagents/agent-simplify-1.jsonl", dollarsLine("a4", at("2026-11-17 09:06"), 3, text))
	writeTranscript(t, dir, "-work-p-b/s.jsonl", dollarsLine("b1", at("2026-11-20 09:00"), 60, text))
	brief := func(folder, task, id string, sent time.Time, dollars float64) {
		path := folder + "/s/subagents/agent-" + id
		writeTranscript(t, dir, path+".meta.json", `{"agentType":"brief-reviewer","isBuiltIn":false}`)
		writeTranscript(t, dir, path+".jsonl",
			fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"/home/u/crew/projects/p/tasks/%s.md"}}`+"\n", task),
			dollarsLine(id, sent, dollars, text))
	}
	writeTranscript(t, dir, "-crew-lead/s.jsonl", dollarsLine("lead", at("2026-11-18 09:00"), 5, text))
	brief("-crew-lead", "a", "brief-a", at("2026-11-10 09:00"), 1)
	writeTranscript(t, dir, "-work-p-_goal-g/s.jsonl")
	brief("-work-p-_goal-g", "b", "brief-b", at("2026-11-14 09:00"), 2)
	// Tasks that finished outside the range, or haven't, count in the totals only.
	writeTranscript(t, dir, "-work-p-before/s.jsonl", dollarsLine("before", at("2026-11-15 09:00"), 100, text))
	writeTranscript(t, dir, "-work-p-after/s.jsonl", dollarsLine("after", at("2026-11-20 09:00"), 100, text))
	writeTranscript(t, dir, "-work-p-open/s.jsonl", dollarsLine("open", at("2026-11-20 09:00"), 100, text))
	tasks := map[string]Task{
		"p/a":      finishedTask(t, "a", "2026-11-10 08:00", "2026-11-18 12:00"),
		"p/b":      finishedTask(t, "b", "2026-11-14 09:00", "2026-11-22 23:59"),
		"p/before": finishedTask(t, "before", "2026-11-10 09:00", "2026-11-15 23:59"),
		"p/after":  finishedTask(t, "after", "2026-11-14 09:00", "2026-11-23 00:00"),
		"p/open":   taskFile(t, "open", "---\nstatus: pr-open\ncreated: 2026-11-15T09:00:00Z\n---\n## Log\n"),
	}
	owners := map[string]owner{"-crew-lead": {step: stepLead}, "-work-p-_goal-g": {step: stepGoalLead}}
	for id := range tasks {
		owners["-work-"+strings.Replace(id, "/", "-", 1)] = owner{stepWorker, id}
	}
	ts, err := scanTranscripts(dir, owners)
	if err != nil {
		t.Fatal(err)
	}
	ts.completeFrom = at("2026-11-01 09:00") // the sessions were written just now

	var out strings.Builder
	printCosts(t, &out, ts, tasks, nil, at("2026-11-16 00:00"), at("2026-11-22 00:00"))
	want := `Spend from 2026-11-16 to 2026-11-22, at API rates, from this machine's transcripts:

                                          median
  step             total  share  tasks  per task
  Brief review     $0.00     0%      2     $1.50
  Worker sessions   $280    97%      2       $50
  Exhale           $3.00     1%      1     $3.00

  Lead sessions    $5.00   1.7%

  All               $288   100%      2       $53

A step's median is over the tasks that merged or were done in the range and ran that step. It counts all a task spent, before the range too.
It counts only tasks this machine's transcripts hold whole: those begun since 2026-11-01 09:00, from when they're complete, and not under way while another machine ran the crew.

Anthropic's API rates`
	if got := out.String(); !strings.HasPrefix(got, want) {
		t.Errorf("report:\n%s\nwant it to start:\n%s", got, want)
	}
}

// A task whose spend this machine's transcripts may not hold whole is left out
// of every median, and counted. Claude Code deletes a session once it has gone
// unwritten for a while, so the transcripts are complete from when the oldest
// session it would delete was last written, and a task begun before then may
// have lost sessions. An agent's transcript goes with its session, and Claude
// Code keeps an empty transcript, and a session started or last continued in
// Claude Desktop, at any age, so none of those may date the transcripts
// earlier: each case here would let the early task in if it did.
func TestCostsLeaveOutTasksBegunBeforeTheTranscriptsAreComplete(t *testing.T) {
	text := `{"type":"text"}`
	desktopPrompt := `{"type":"user","entrypoint":"claude-desktop","message":{"role":"user","content":"Go on."}}` + "\n"
	for _, c := range []struct {
		name  string
		older map[string][]string // transcripts last written 2026-11-05, by path
	}{
		{"its earlier sessions were deleted by age", nil},
		{"an agent's transcript is older than its session", map[string][]string{
			"-crew-lead/oldest/subagents/agent-a.jsonl": {dollarsLine("agent", costsDay(t, "2026-11-05 09:00"), 1, text)},
		}},
		{"a session started in Claude Desktop", map[string][]string{
			"-crew-lead/desktop.jsonl": {desktopPrompt, dollarsLine("desktop", costsDay(t, "2026-11-05 09:00"), 1, text)},
		}},
		{"a session last continued in Claude Desktop", map[string][]string{
			"-crew-lead/desktop.jsonl": {dollarsLine("desktop", costsDay(t, "2026-11-05 09:00"), 1, text), desktopPrompt},
		}},
		{"an empty transcript", map[string][]string{"-crew-lead/empty.jsonl": nil}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			written := map[string]string{
				"-crew-lead/oldest.jsonl": "2026-11-10 09:00",
				"-work-p-whole/s.jsonl":   "2026-11-18 09:00",
				"-work-p-early/s.jsonl":   "2026-11-18 09:00",
			}
			writeTranscript(t, dir, "-crew-lead/oldest.jsonl", dollarsLine("lead", costsDay(t, "2026-11-10 09:00"), 1, text))
			writeTranscript(t, dir, "-work-p-whole/s.jsonl", dollarsLine("whole", costsDay(t, "2026-11-18 09:00"), 10, text))
			writeTranscript(t, dir, "-work-p-early/s.jsonl", dollarsLine("early", costsDay(t, "2026-11-18 09:00"), 30, text))
			for path, lines := range c.older {
				writeTranscript(t, dir, path, lines...)
				written[path] = "2026-11-05 09:00"
			}
			for path, at := range written {
				if err := os.Chtimes(filepath.Join(dir, path), time.Time{}, costsDay(t, at)); err != nil {
					t.Fatal(err)
				}
			}
			ts, err := scanTranscripts(dir, map[string]owner{
				"-crew-lead":    {step: stepLead},
				"-work-p-whole": {stepWorker, "p/whole"},
				"-work-p-early": {stepWorker, "p/early"},
			})
			if err != nil {
				t.Fatal(err)
			}
			tasks := map[string]Task{
				"p/whole": finishedTask(t, "whole", "2026-11-10 09:00", "2026-11-18 12:00"),
				"p/early": finishedTask(t, "early", "2026-11-09 12:00", "2026-11-18 12:00"),
			}
			var out strings.Builder
			printCosts(t, &out, ts, tasks, nil, costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00"))
			for _, want := range []string{
				"  All                $40   100%      1       $10\n",
				"those begun since 2026-11-10 09:00, from when they're complete, and not under way while another machine ran the crew. Left out: 1 begun before then.\n",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("report doesn't say %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// Another machine's sessions stay on it, so a task under way while another
// machine ran the crew is left out of every median, and the report names the
// machine. One whose time doesn't touch another machine's stays in.
func TestCostsLeaveOutTasksUnderWayWhileAnotherMachineRanTheCrew(t *testing.T) {
	at := func(s string) time.Time { return costsDay(t, s) }
	spent := func(task string, dollars float64) *charge {
		return &charge{owner{stepWorker, task}, &message{"claude-opus-5-5", at("2026-11-18 09:00"), claudeUsage{Output: int(dollars * 50_000)}}}
	}
	tasks := map[string]Task{
		"p/whole":      finishedTask(t, "whole", "2026-11-13 10:00", "2026-11-18 12:00"),
		"p/moved":      finishedTask(t, "moved", "2026-11-10 10:00", "2026-11-18 12:00"),
		"p/also-moved": finishedTask(t, "also-moved", "2026-11-10 08:00", "2026-11-19 12:00"),
		"p/desk":       finishedTask(t, "desk", "2026-11-13 08:00", "2026-11-20 12:00"),
	}
	ts := transcripts{completeFrom: at("2026-11-01 09:00"), charges: []*charge{
		spent("p/whole", 10), spent("p/moved", 30), spent("p/also-moved", 30), spent("p/desk", 30),
	}}
	elsewhere := []tenure{
		{"laptop", at("2026-11-10 09:30"), at("2026-11-10 12:00")},
		{"desk", at("2026-11-12 09:00"), at("2026-11-13 09:00")},
		{holder: "laptop", from: at("2026-11-25 09:00")},
	}
	var out strings.Builder
	printCosts(t, &out, ts, tasks, elsewhere, at("2026-11-16 00:00"), at("2026-11-22 00:00"))
	for _, want := range []string{
		"  All               $100   100%      1       $10\n",
		"Left out: 3 under way while desk or laptop ran the crew.\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report doesn't say %q:\n%s", want, out.String())
		}
	}
}

// A task with a message at a model there's no rate for has a cost nobody
// knows, and one with nothing spent in the transcripts ran somewhere else, so
// each is left out of every median, and counted.
func TestCostsLeaveOutTasksWhoseCostIsUnknown(t *testing.T) {
	at := func(s string) time.Time { return costsDay(t, s) }
	spent := func(task, model string) *charge {
		return &charge{owner{stepWorker, task}, &message{model, at("2026-11-18 09:00"), claudeUsage{Output: 500_000}}}
	}
	tasks := map[string]Task{
		"p/rated":   finishedTask(t, "rated", "2026-11-13 10:00", "2026-11-18 12:00"),
		"p/unrated": finishedTask(t, "unrated", "2026-11-13 10:00", "2026-11-18 12:00"),
		"p/unspent": finishedTask(t, "unspent", "2026-11-13 10:00", "2026-11-18 12:00"),
	}
	ts := transcripts{completeFrom: at("2026-11-01 09:00"), charges: []*charge{
		spent("p/rated", "claude-opus-5-5"), spent("p/unrated", "claude-opus-5-5"), spent("p/unrated", "claude-mystery-1"),
	}}
	var out strings.Builder
	printCosts(t, &out, ts, tasks, nil, at("2026-11-16 00:00"), at("2026-11-22 00:00"))
	for _, want := range []string{
		"  All                $20   100%      1       $10\n",
		"Left out: 1 with a message there's no rate for and 1 with nothing spent in this machine's transcripts.\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report doesn't say %q:\n%s", want, out.String())
		}
	}
}

// crew migrate read a finish from its Log line, whose time is the clock of the
// machine that wrote it, with no zone. Here, in New York, a task merged on a
// laptop in Los Angeles at 06:30 its time, after the laptop took over at 04:00
// there: 06:30 read in New York would be before that. A finish recorded since
// has its zone, so the same record by crew sync was before the laptop took
// over, at 07:00 in New York.
func TestCostsLeaveOutATaskThatFinishedOnAnotherMachinesClock(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("EST", -5*60*60)
	t.Cleanup(func() { time.Local = local })
	at := func(s string) time.Time { return costsDay(t, s) }
	for _, c := range []struct {
		by      string
		leftOut bool
	}{{"crew migrate", true}, {"crew sync", false}} {
		west := migratedTask(t, "west", "---\nstatus: merged\ncreated: "+at("2026-11-13 10:00").Format(time.RFC3339)+"\n---\n## Log\n")
		changed(t, west, c.by, at("2026-11-18 06:30"), Merged)
		ts := transcripts{completeFrom: at("2026-11-01 09:00"), charges: []*charge{
			{owner{stepWorker, "p/west"}, &message{"claude-opus-5-5", at("2026-11-17 09:00"), claudeUsage{Output: 500_000}}},
		}}
		elsewhere := []tenure{{holder: "laptop", from: time.Date(2026, 11, 18, 4, 0, 0, 0, time.FixedZone("PST", -8*60*60))}}
		var out strings.Builder
		printCosts(t, &out, ts, map[string]Task{"p/west": west}, elsewhere, at("2026-11-16 00:00"), at("2026-11-22 00:00"))
		const want = "Left out: 1 under way while laptop ran the crew.\n"
		if got := strings.Contains(out.String(), want); got != c.leftOut {
			t.Errorf("finish by %s: report leaves the task out = %v; want %v:\n%s", c.by, got, c.leftOut, out.String())
		}
	}
}

// Here, in New York, a task merged at 10:00, and a laptop in New York took
// over at 12:00: the task finished before then, on either clock.
func TestCostsKeepATaskThatFinishedBeforeAnotherMachineTookOver(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("EST", -5*60*60)
	t.Cleanup(func() { time.Local = local })
	at := func(s string) time.Time { return costsDay(t, s) }
	tasks := map[string]Task{"p/a": finishedTask(t, "a", "2026-11-13 10:00", "2026-11-18 10:00")}
	ts := transcripts{completeFrom: at("2026-11-01 09:00"), charges: []*charge{
		{owner{stepWorker, "p/a"}, &message{"claude-opus-5-5", at("2026-11-17 09:00"), claudeUsage{Output: 500_000}}},
	}}
	elsewhere := []tenure{{holder: "laptop", from: at("2026-11-18 12:00")}}
	var out strings.Builder
	printCosts(t, &out, ts, tasks, elsewhere, at("2026-11-16 00:00"), at("2026-11-22 00:00"))
	if strings.Contains(out.String(), "Left out") {
		t.Errorf("report leaves out a task that finished before the laptop took over:\n%s", out.String())
	}
}

// The expected folders come from the encoder in Claude Code 2.1.328 itself.
func TestTranscriptFolderCutsAndHashesWhatPassesTwoHundredCharacters(t *testing.T) {
	at200 := "/" + strings.Repeat("a/", 99) + "b"
	at201 := "/" + strings.Repeat("a/", 100)
	withUTF16Pairs := "/" + strings.Repeat("ab/", 70) + "é😀"
	for _, tc := range []struct{ name, dir, want string }{
		{"short", "/home/isaac/crew.x", "-home-isaac-crew-x"},
		{"200 characters", at200, strings.NewReplacer("/", "-").Replace(at200)},
		{"201 characters", at201, strings.NewReplacer("/", "-").Replace(at201)[:200] + "-f6id5z"},
		{"astral characters count as two", withUTF16Pairs, strings.NewReplacer("/", "-").Replace(withUTF16Pairs[:len(withUTF16Pairs)-len("é😀")])[:200] + "-1lfpmr"},
	} {
		if got := transcriptFolder(tc.dir); got != tc.want {
			t.Errorf("%s: transcriptFolder = %q; want %q", tc.name, got, tc.want)
		}
	}
}
