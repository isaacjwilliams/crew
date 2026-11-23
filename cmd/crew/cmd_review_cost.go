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
	// The cache writes kept for an hour, which cost more than the rest: those
	// kept for five minutes, the API's default. CacheWrite counts both.
	CacheWriteKept struct {
		OneHour int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u claudeUsage) total() int { return u.Input + u.CacheWrite + u.CacheRead + u.Output }

func (u claudeUsage) add(v claudeUsage) claudeUsage {
	sum := claudeUsage{Input: u.Input + v.Input, CacheWrite: u.CacheWrite + v.CacheWrite,
		CacheRead: u.CacheRead + v.CacheRead, Output: u.Output + v.Output}
	sum.CacheWriteKept.OneHour = u.CacheWriteKept.OneHour + v.CacheWriteKept.OneHour
	return sum
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
	cost, err := claudeRunCost(matches[0])
	if err != nil {
		return fail("%s: %v", matches[0], err)
	}
	fmt.Println(cost)
	return nil
}

// claudeRunCost reads what the Claude subagent run in the transcript at path
// cost: the tokens its API messages used, the time from its first entry to its
// last, and the models and efforts its replies name.
func claudeRunCost(path string) (runCost, error) {
	var cost runCost
	var msgs messages
	var first, last time.Time
	unread, err := scanLines(path, "", func(l transcriptLine) bool {
		if !l.Timestamp.IsZero() {
			if first.IsZero() {
				first = l.Timestamp
			}
			last = l.Timestamp
		}
		if l.Type != "assistant" {
			return true
		}
		if m := l.Message.Model; m != "" && m != syntheticModel && !slices.Contains(cost.models, m) {
			cost.models = append(cost.models, m)
		}
		if l.Effort != "" && !slices.Contains(cost.efforts, l.Effort) {
			cost.efforts = append(cost.efforts, l.Effort)
		}
		msgs.add(l)
		return true
	})
	if err != nil {
		return runCost{}, err
	}
	// A line that doesn't read could hold a message's only usage, so the count
	// would be short.
	if unread > 0 {
		return runCost{}, fmt.Errorf("lines that didn't read: %d", unread)
	}
	if len(msgs.all) > 0 {
		var sum claudeUsage
		for _, m := range msgs.all {
			sum = sum.add(m.usage)
		}
		cost.tokens = sum
	}
	cost.took = last.Sub(first)
	return cost, nil
}

// transcriptLine is what is read from a line of a Claude Code transcript.
type transcriptLine struct {
	Type       string
	Timestamp  time.Time
	Effort     string
	Entrypoint string // how the session was run: "cli" for claude, "sdk-cli" for claude -p
	Message    struct {
		ID      string
		Model   string
		Usage   *claudeUsage
		Content json.RawMessage
	}
}

// scanLines calls f with each line of the transcript at path that holds want,
// read, in order, until f returns false. Lines without want aren't read, which
// keeps a scan quick, and a line that doesn't read is counted rather than
// passed on: a session may be writing it.
func scanLines(path, want string, f func(transcriptLine) bool) (unread int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	lines := bufio.NewScanner(file)
	lines.Buffer(nil, 64<<20)
	for lines.Scan() {
		if len(bytes.TrimSpace(lines.Bytes())) == 0 || !bytes.Contains(lines.Bytes(), []byte(want)) {
			continue
		}
		var l transcriptLine
		if json.Unmarshal(lines.Bytes(), &l) != nil {
			unread++
			continue
		}
		if !f(l) {
			break
		}
	}
	if err := lines.Err(); err != nil {
		return unread, err
	}
	return unread, nil
}

// syntheticModel is the model Claude Code names its own messages, such as an
// API error. They cost nothing.
const syntheticModel = "<synthetic>"

// message is one API message of a transcript.
type message struct {
	model string
	at    time.Time
	usage claudeUsage
}

// messages is the API messages of the transcripts read, each once, in the
// order they were first read. Claude Code writes a message's usage again with
// each of its content blocks, the last time in full, and a resumed session may
// copy a message with its counts zeroed, so a message keeps its fullest usage.
// A message without an ID counts on its own.
type messages struct {
	all  []*message
	byID map[string]*message
}

// add takes the message on line l, and returns it with whether it's new. It
// skips a line with no usage or a <synthetic> message, and returns nil.
func (ms *messages) add(l transcriptLine) (m *message, isNew bool) {
	u := l.Message.Usage
	if u == nil || l.Message.Model == syntheticModel {
		return nil, false
	}
	if m, ok := ms.byID[l.Message.ID]; ok {
		if u.total() > m.usage.total() {
			m.usage = *u
		}
		return m, false
	}
	m = &message{l.Message.Model, l.Timestamp, *u}
	ms.all = append(ms.all, m)
	if l.Message.ID != "" {
		if ms.byID == nil {
			ms.byID = map[string]*message{}
		}
		ms.byID[l.Message.ID] = m
	}
	return m, true
}
