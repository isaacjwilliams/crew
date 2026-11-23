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

	ts, err := scanTranscripts(dir, map[string]string{"-crew-lead": stepLead})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.charges) != 5 || ts.unread != 1 {
		t.Errorf("got %d charges and %d lines unread; want msg_1 and msg_3 to msg_6 charged, and the cut-off line unread", len(ts.charges), ts.unread)
	}
	r := costsIn(ts, from, to)
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
		{stepLead, &message{"claude-opus-5-5", at, u}},
		{stepLead, &message{"claude-haiku-5-5", at, u}},
		{stepLead, &message{"claude-mystery-1", at, u}},
	}
	r := costsIn(transcripts{charges: charges}, costsDay(t, "2026-11-16 00:00"), costsDay(t, "2026-11-22 00:00"))
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
// its own step, whoever ran it, and a lead's or goal lead's other agents are
// charged to a step for them.
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
	subagent("-crew-lead", "brief", "brief-reviewer", "t12", "Review /home/u/crew/projects/p/tasks/001-a.md.", 20)
	subagent("-crew-lead", "helper", "general-purpose", "t13", "Look at /home/u/crew/projects/p/tasks/001-a.md.", 21)
	subagent("-work-p-_goal-g", "helper", "Explore", "t16", "Look around.", 25)
	subagent("-work-p-_goal-g", "plan", "plan-reviewer", "t14", "Review /home/v/crew/projects/p/tasks/002-b.md.", 22)
	subagent("-home-u-Code-elsewhere", "reviewer", "reviewer", "t15", "Review it.", 23)

	ts, err := scanTranscripts(dir, map[string]string{
		task:              stepWorker,
		"-crew-lead":      stepLead,
		"-work-p-_goal-g": stepGoalLead,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, c := range ts.charges {
		got[c.usage.Output] = c.step
	}
	for output, want := range map[int]string{
		1:  stepWorker,
		10: "general-purpose",
		11: stepExhale,
		12: stepExhale,
		13: "general-purpose",
		14: stepSecurityReview,
		15: stepPrePRReview,
		16: stepAutomatedReview,
		17: "Explore",
		18: "unnamed agent",
		19: "test-review",
		20: stepBriefReview,
		21: stepLeadAgents,
		22: stepBriefReview,
		23: stepOther,
		24: stepExhale,
		25: stepGoalLeadAgents,
	} {
		if got[output] != want {
			t.Errorf("the message with %d output tokens is charged to %q; want %q", output, got[output], want)
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
	ts, err := scanTranscripts(dir, map[string]string{"-crew-lead": stepLead})
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
	if ts, err = scanTranscripts(dir, map[string]string{"-crew-lead": stepLead}); err != nil {
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
		costsIn(ts, costsDay(t, c.from+" 00:00"), costsDay(t, "2026-11-15 00:00")).print(&out)
		const note = "This machine's transcripts are complete only from 2026-11-10 09:00: older sessions may have been deleted.\n"
		if got := strings.Contains(out.String(), note); got != c.want {
			t.Errorf("from %s: report says the transcripts are incomplete = %v; want %v:\n%s", c.from, got, c.want, out.String())
		}
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
