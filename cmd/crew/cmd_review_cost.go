package main

// crew review-cost: what one run of the Claude reviewer cost, for the record
// pr-review keeps of each review. crew codex reports a Codex run's cost itself.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// runCost is what one reviewer's run cost, as its source reports it. What the
// source doesn't report stays empty and reads "not reported".
type runCost struct {
	tokens  tokenUsage // nil when the source reported no tokens
	took    time.Duration
	models  []string
	efforts []string
}

// tokenUsage is a run's tokens in its source's own counts. Each source says how
// many there were in all, because they count differently: Claude's counts
// don't overlap, but Codex's input includes the tokens it read from cache.
type tokenUsage interface {
	total() int
	String() string // the counts, in the source's own terms
}

// String is the line pr-review records, for example "460,666 tokens (24 input, 57,045 cache write, 392,958 cache
// read, 10,639 output); 2m27s; claude-opus-5-5 at high effort".
func (c runCost) String() string {
	tokens := "tokens not reported"
	if c.tokens != nil {
		tokens = fmt.Sprintf("%s tokens (%s)", thousands(c.tokens.total()), c.tokens)
	}
	model := "model not reported"
	if len(c.models) > 0 {
		model = strings.Join(c.models, " and ")
	}
	if len(c.efforts) > 0 {
		model += " at " + strings.Join(c.efforts, " and ") + " effort"
	} else {
		model += ", effort not reported"
	}
	return fmt.Sprintf("%s; %s; %s", tokens, c.took.Round(time.Second), model)
}

// thousands writes n with a comma between each group of three digits.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// claudeUsage is the usage Claude's API reports for a message. Its four counts
// don't overlap.
type claudeUsage struct {
	Input      int `json:"input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	Output     int `json:"output_tokens"`
}

func (u claudeUsage) total() int { return u.Input + u.CacheWrite + u.CacheRead + u.Output }

func (u claudeUsage) add(v claudeUsage) claudeUsage {
	return claudeUsage{u.Input + v.Input, u.CacheWrite + v.CacheWrite, u.CacheRead + v.CacheRead, u.Output + v.Output}
}

func (u claudeUsage) String() string {
	return fmt.Sprintf("%s input, %s cache write, %s cache read, %s output",
		thousands(u.Input), thousands(u.CacheWrite), thousands(u.CacheRead), thousands(u.Output))
}

// crew review-cost AGENT_ID
//
// Prints what the run of the Claude subagent AGENT_ID cost, read from its
// transcript.
func cmdReviewCost(c *Crew, args []string) error {
	// The agent ID goes into a glob pattern.
	if len(args) != 1 || strings.ContainsAny(args[0], `/\*?[`) {
		return usage("crew review-cost AGENT_ID")
	}
	transcripts := claudeTranscripts()
	matches, _ := filepath.Glob(filepath.Join(transcripts, "*", "*", "subagents", "agent-"+args[0]+".jsonl"))
	if len(matches) != 1 {
		return fail("found %d transcripts for agent %s under %s; want one", len(matches), args[0], transcripts)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return err
	}
	cost, err := claudeRunCost(b)
	if err != nil {
		return fail("%s: %v", matches[0], err)
	}
	fmt.Println(cost)
	return nil
}

// claudeRunCost reads what a Claude subagent's run cost from its transcript:
// the tokens its API messages used, the time from its first entry to its last,
// and the models and efforts its replies name.
//
// Claude Code writes a message's usage again with each of the message's
// content blocks, so each message counts once, by its ID, with the usage
// written last. A message without an ID counts on its own.
func claudeRunCost(transcript []byte) (runCost, error) {
	var cost runCost
	usage := map[string]claudeUsage{}
	var first, last time.Time
	lines := bufio.NewScanner(bytes.NewReader(transcript))
	lines.Buffer(nil, 64<<20)
	for n := 1; lines.Scan(); n++ {
		if len(bytes.TrimSpace(lines.Bytes())) == 0 {
			continue
		}
		var e struct {
			Type      string
			Timestamp time.Time
			Effort    string
			Message   struct {
				ID    string
				Model string
				Usage *claudeUsage
			}
		}
		// A line that doesn't read could hold a message's only usage, so the
		// count would be short.
		if err := json.Unmarshal(lines.Bytes(), &e); err != nil {
			return runCost{}, fmt.Errorf("line %d: %v", n, err)
		}
		if !e.Timestamp.IsZero() {
			if first.IsZero() {
				first = e.Timestamp
			}
			last = e.Timestamp
		}
		if e.Type != "assistant" {
			continue
		}
		// Claude Code names its own messages, such as an API error, <synthetic>.
		if m := e.Message.Model; m != "" && m != "<synthetic>" && !slices.Contains(cost.models, m) {
			cost.models = append(cost.models, m)
		}
		if e.Effort != "" && !slices.Contains(cost.efforts, e.Effort) {
			cost.efforts = append(cost.efforts, e.Effort)
		}
		if id := e.Message.ID; e.Message.Usage != nil {
			if id == "" {
				id = fmt.Sprintf("line %d", n)
			}
			usage[id] = *e.Message.Usage
		}
	}
	if err := lines.Err(); err != nil {
		return runCost{}, err
	}
	if len(usage) > 0 {
		var sum claudeUsage
		for _, u := range usage {
			sum = sum.add(u)
		}
		cost.tokens = sum
	}
	cost.took = last.Sub(first)
	return cost, nil
}
