package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reviewerTranscript is a Claude subagent's transcript, trimmed to the fields
// claudeRunCost reads, in the shape Claude Code writes it: message 1's usage
// once for each of its two content blocks, entries without a message, and a
// <synthetic> reply of Claude Code's own.
const reviewerTranscript = `{"type":"user","timestamp":"2026-11-17T03:42:54.662Z","message":{"role":"user","content":"Review it."}}
{"type":"attachment","timestamp":"2026-11-17T03:42:54.700Z","attachment":{"type":"environment"}}
{"type":"api-request","request":{"output_config":{"effort":"high"}}}
{"type":"assistant","timestamp":"2026-11-17T03:42:56.664Z","effort":"high","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":13332,"cache_read_input_tokens":0,"output_tokens":224,"cache_creation":{"ephemeral_5m_input_tokens":13332}}}}
{"type":"assistant","timestamp":"2026-11-17T03:42:57.997Z","effort":"high","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":13332,"cache_read_input_tokens":0,"output_tokens":224,"cache_creation":{"ephemeral_5m_input_tokens":13332}}}}
{"type":"user","timestamp":"2026-11-17T03:42:58.100Z","message":{"role":"user","content":[{"type":"tool_result"}]}}
{"type":"assistant","timestamp":"2026-11-17T03:43:00.992Z","effort":"high","message":{"id":"msg_2","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":3827,"cache_read_input_tokens":13332,"output_tokens":296}}}
{"type":"assistant","timestamp":"2026-11-17T03:43:01.000Z","message":{"id":"msg_3","model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}
{"type":"user","timestamp":"2026-11-17T03:45:21.834Z","message":{"role":"user","content":[{"type":"tool_result"}]}}
`

func TestClaudeRunCostCountsEachMessageOnce(t *testing.T) {
	cost, err := claudeRunCost([]byte(reviewerTranscript))
	if err != nil {
		t.Fatal(err)
	}
	want := "31,015 tokens (4 input, 17,159 cache write, 13,332 cache read, 520 output); 2m27s; claude-opus-5-5 at high effort"
	if got := cost.String(); got != want {
		t.Errorf("cost = %q\nwant %q", got, want)
	}
}

// A transcript with no usage, model or effort says so rather than reporting
// zeros, and a line that doesn't read is an error, since it could hold a
// message's only usage.
func TestClaudeRunCostSaysWhatTheTranscriptDoesNotReport(t *testing.T) {
	cost, err := claudeRunCost([]byte(`{"type":"user","timestamp":"2026-11-17T03:42:54Z"}` + "\n" + `{"type":"user","timestamp":"2026-11-17T03:42:59Z"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cost.String(), "tokens not reported; 5s; model not reported, effort not reported"; got != want {
		t.Errorf("cost = %q; want %q", got, want)
	}
	if _, err := claudeRunCost([]byte(reviewerTranscript + `{"type":"assistant","message":{"id":"msg_4","usa`)); err == nil || !strings.Contains(err.Error(), "line 10") {
		t.Errorf("a cut-off line gave %v; want an error naming line 10", err)
	}
}

// Every turn that completes reports its usage, and only those do: a run Codex
// was stopped in reports none.
func TestCodexTokensReadsCompletedTurns(t *testing.T) {
	events := filepath.Join(t.TempDir(), "answer-0.events.jsonl")
	write := func(s string) {
		if err := os.WriteFile(events, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	started := `{"type":"thread.started","thread_id":"0199a3b2"}` + "\n" + `{"type":"turn.started"}` + "\n" + `{"type":"item.completed","item":{"type":"agent_message","text":"input_tokens"}}` + "\n"
	write(started + `{"type":"turn.completed","usage":{"input_tokens":221068,"cached_input_tokens":182912,"cache_write_input_tokens":0,"output_tokens":3086,"reasoning_output_tokens":1349}}` + "\n")
	cost := runCost{tokens: codexTokens(events), took: 362 * time.Second, models: []string{"gpt-6-astra"}, efforts: []string{"xhigh"}}
	if got, want := cost.String(), "224,154 tokens (221,068 input including 182,912 cache read, 3,086 output); 6m2s; gpt-6-astra at xhigh effort"; got != want {
		t.Errorf("cost = %q\nwant %q", got, want)
	}
	write(started + `{"type":"item.started","item":{"type":"command_exe`)
	if u := codexTokens(events); u != nil {
		t.Errorf("a stalled run's tokens = %v; want none reported", u)
	}
}
