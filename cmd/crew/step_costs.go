package main

// What a finished task cost in tokens, per step of crew's process, agent and
// model, read from the Claude Code transcripts of the machine that ran it and
// kept in the task's ## Cost section. Transcripts stay on that machine and are
// deleted after a while; the ledger keeps what they said.

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The steps of a worker's session that aren't review steps (review_runs.go has
// those).
const (
	stepImplementing  = "Implementing"
	stepEvidenceAndPR = "Evidence and PR" // first-run steps 6 to 8: the evidence, the PR, recording it
	stepReviewRounds  = "Review rounds"
	stepCIRounds      = "CI rounds"
	stepRestacks      = "Restacks and conflicts"
)

// crewSteps is every step of crew's own process, in the order a task meets
// them. A task keeps each as not run when it cost nothing. A repo's own review
// agent is a step too, named by the agent, and kept only when it ran.
var crewSteps = []string{stepBriefReview, stepImplementing, stepExhale, stepPrePRReview, stepSecurityReview,
	stepEvidenceAndPR, stepAutomatedReview, stepReviewRounds, stepCIRounds, stepRestacks}

// workerAgent is the agent named for the worker's own session.
const workerAgent = "worker"

// TaskCost is what a task cost, as its ## Cost section keeps it.
type TaskCost struct {
	Steps  []StepCost
	NotRun []string // steps that didn't run on the task
	// What ran but left no transcript on the machine that kept the cost:
	// "Brief review", or "worker session <job>" for a session the Log names.
	Missing []string
}

// StepCost is the tokens one agent spent on one step, on one model.
type StepCost struct {
	Step, Agent, Model string
	Tokens             claudeUsage
}

// String is the ## Cost section's lines, one for each step, agent and model:
// "- Implementing, worker, claude-opus-5-5: 24 input, 57,045 cache write,
// 392,958 cache read, 10,639 output", then "- Exhale: not run" and
// "- Brief review: missing".
func (tc TaskCost) String() string {
	var b strings.Builder
	for _, s := range tc.Steps {
		fmt.Fprintf(&b, "- %s, %s, %s: %s\n", s.Step, s.Agent, s.Model, s.Tokens)
	}
	for _, step := range tc.NotRun {
		fmt.Fprintf(&b, "- %s: not run\n", step)
	}
	for _, what := range tc.Missing {
		fmt.Fprintf(&b, "- %s: missing\n", what)
	}
	return b.String()
}

var (
	costLine  = regexp.MustCompile(`^- ([^,]+), ([^,]+), ([^,]+): ([\d,]+) input, ([\d,]+) cache write, ([\d,]+) cache read, ([\d,]+) output$`)
	stateLine = regexp.MustCompile(`^- (.+): (not run|missing)$`)
)

// Cost is the task's cost, read back from its ## Cost section, and whether it
// has been kept.
func (t Task) Cost() (TaskCost, bool) {
	var tc TaskCost
	text := t.Section("## Cost")
	count := func(s string) int { return atoiOr0(strings.ReplaceAll(s, ",", "")) }
	for _, l := range strings.Split(text, "\n") {
		if m := costLine.FindStringSubmatch(l); m != nil {
			tokens := claudeUsage{count(m[4]), count(m[5]), count(m[6]), count(m[7])}
			tc.Steps = append(tc.Steps, StepCost{m[1], m[2], m[3], tokens})
		} else if m := stateLine.FindStringSubmatch(l); m != nil && m[2] == "not run" {
			tc.NotRun = append(tc.NotRun, m[1])
		} else if m != nil {
			tc.Missing = append(tc.Missing, m[1])
		}
	}
	return tc, text != ""
}

// taskCost reads what task t cost from this machine's transcripts: every
// session run in its worktree, wt, and the brief reviews of it, briefs (from
// briefReviews).
//
// A session the task's Log names (it dispatched or followed up the worker)
// with no transcript in the worktree's folder is missing, and so is a brief
// review the Log records when no transcript of one is here. A step that has no
// cost and nothing missing that could have run it didn't run.
func taskCost(t Task, transcripts, wt string, briefs []string) (TaskCost, error) {
	ch := newCharges()
	for _, path := range briefs {
		review, err := readTranscript(path)
		if err != nil {
			return TaskCost{}, err
		}
		ch.subagent(review, stepBriefReview, subagentOf(path).AgentType)
	}
	dir := filepath.Join(transcripts, transcriptFolder(wt))
	if err := ch.workerSessions(dir, t.ReviewRounds()); err != nil {
		return TaskCost{}, err
	}
	tc := TaskCost{Steps: ch.sum()}
	if len(briefs) == 0 && slices.ContainsFunc(t.ReviewRuns(), func(r ReviewRun) bool { return r.Step == stepBriefReview }) {
		tc.Missing = append(tc.Missing, stepBriefReview)
	}
	for _, job := range t.WorkerJobs() {
		if found, _ := filepath.Glob(filepath.Join(dir, job+"*.jsonl")); len(found) == 0 {
			tc.Missing = append(tc.Missing, "worker session "+job)
		}
	}
	workerMissing := len(tc.Missing) > 0 && tc.Missing[len(tc.Missing)-1] != stepBriefReview
	for _, step := range crewSteps {
		if slices.Contains(tc.Missing, step) || step != stepBriefReview && workerMissing ||
			slices.ContainsFunc(tc.Steps, func(s StepCost) bool { return s.Step == step }) {
			continue
		}
		tc.NotRun = append(tc.NotRun, step)
	}
	return tc, nil
}

// transcriptFolder is the folder Claude Code keeps the transcripts of sessions
// started in dir in: dir with every character but a letter or digit made "-".
func transcriptFolder(dir string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
}

// workerJob is the job a Log line starts or resumes the worker in.
var workerJob = regexp.MustCompile(`^(?:dispatched|followup \([^)]*\)): worker ([0-9a-f]{8})\b`)

// WorkerJobs is every job the task's worker ran in, as its Log names them: a
// job is the first 8 characters of its session's ID.
func (t Task) WorkerJobs() []string {
	var jobs []string
	for _, e := range t.LogEntries() {
		if m := workerJob.FindStringSubmatch(e.msg); m != nil && !slices.Contains(jobs, m[1]) {
			jobs = append(jobs, m[1])
		}
	}
	return jobs
}

// charges is each message's step, agent, model and tokens, by message ID, so a
// message Claude Code wrote several times counts once: in the step it first
// appeared in, with the usage written last, as claudeRunCost counts it.
// steps is the step each tool call ran in, by the call's ID, so a subagent is
// charged to the step that started it.
type charges struct {
	messages map[string]*StepCost
	steps    map[string]string
}

func newCharges() charges { return charges{map[string]*StepCost{}, map[string]string{}} }

// charge charges the message in the transcript at path's nth entry to step.
func (ch charges) charge(path string, n int, step, agent string, l transcriptLine) {
	if l.Message.Usage == nil {
		return
	}
	id := cmp.Or(l.Message.ID, fmt.Sprintf("%s entry %d", path, n))
	if s, ok := ch.messages[id]; ok {
		s.Tokens = *l.Message.Usage
		return
	}
	ch.messages[id] = &StepCost{step, agent, cmp.Or(l.Message.Model, "unknown model"), *l.Message.Usage}
}

// sum is the charges added up by step, agent and model, in crewSteps' order, a
// repo's own review agents after them.
func (ch charges) sum() []StepCost {
	var sums []StepCost
	for _, m := range ch.messages {
		i := slices.IndexFunc(sums, func(s StepCost) bool { return s.Step == m.Step && s.Agent == m.Agent && s.Model == m.Model })
		if i < 0 {
			sums = append(sums, StepCost{Step: m.Step, Agent: m.Agent, Model: m.Model})
			i = len(sums) - 1
		}
		sums[i].Tokens = sums[i].Tokens.add(m.Tokens)
	}
	order := func(step string) int {
		if i := slices.Index(crewSteps, step); i >= 0 {
			return i
		}
		return len(crewSteps)
	}
	slices.SortFunc(sums, func(a, b StepCost) int {
		return cmp.Or(cmp.Compare(order(a.Step), order(b.Step)), cmp.Compare(a.Step, b.Step),
			cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Model, b.Model))
	})
	// Claude Code's own messages, such as an API error, use no tokens.
	return slices.DeleteFunc(sums, func(s StepCost) bool { return s.Tokens.total() == 0 })
}

// workerSessions charges every session started in the task's worktree, whose
// transcripts are in dir, to the steps it ran: its main sessions, oldest first,
// carrying the step from one to the next, then their subagents.
func (ch charges) workerSessions(dir string, rounds []ReviewRound) error {
	mains, err := readTranscripts(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return err
	}
	// A message is charged to the step it was written in, and the tool calls
	// in it can start the next.
	st := stepper{step: stepImplementing, rounds: rounds}
	for _, session := range mains {
		for n, l := range session.lines {
			st.prompt(l.prompt())
			ch.charge(session.path, n+1, st.step, workerAgent, l)
			for _, use := range l.toolUses() {
				st.toolUse(use)
				ch.steps[use.ID] = st.step
			}
		}
	}
	// A subagent starts after the session or subagent that started it, so in
	// order of their start each one's step is known by the time it's charged.
	subagents, err := readTranscripts(filepath.Join(dir, "*", "subagents", "agent-*.jsonl"))
	if err != nil {
		return err
	}
	for _, a := range subagents {
		meta := subagentOf(a.path)
		// A repo's own review agent is a step of its own. Any other subagent
		// is charged to the step that started it, and one whose start isn't in
		// these transcripts to implementing.
		step := ch.steps[meta.ToolUseID]
		if strings.HasSuffix(meta.AgentType, "-review") {
			step = meta.AgentType
		}
		ch.subagent(a, cmp.Or(step, stepImplementing), cmp.Or(meta.AgentType, "subagent"))
	}
	return nil
}

// subagent charges every message of a subagent's transcript to step.
func (ch charges) subagent(a transcript, step, agent string) {
	for n, l := range a.lines {
		for _, use := range l.toolUses() {
			ch.steps[use.ID] = step
		}
		ch.charge(a.path, n+1, step, agent, l)
	}
}

// subagentMeta is what Claude Code records of a subagent beside its
// transcript: its agent, and the tool call that started it.
type subagentMeta struct{ AgentType, ToolUseID string }

// subagentOf is the subagent whose transcript is at path, or nothing when
// Claude Code recorded nothing of it.
func subagentOf(path string) subagentMeta {
	var meta subagentMeta
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		json.Unmarshal(b, &meta)
	}
	return meta
}

// A transcript: its file, and its lines in the order Claude Code wrote them.
type transcript struct {
	path  string
	lines []transcriptLine
}

// transcriptLine is what a cost needs from a line of a Claude Code transcript.
type transcriptLine struct {
	Type      string
	Timestamp time.Time
	Message   struct {
		ID      string
		Model   string
		Usage   *claudeUsage
		Content json.RawMessage
	}
}

// A tool call of the model's: its ID, the tool, and what it was given.
type toolUse struct {
	ID, Name string
	Input    json.RawMessage
}

// prompt is the text of a prompt sent to the session, or "" when the line
// holds none: Claude Code writes tool results and its own notices as user
// lines too, but only a prompt as plain text.
func (l transcriptLine) prompt() string {
	var text string
	if l.Type == "user" && json.Unmarshal(l.Message.Content, &text) == nil {
		return text
	}
	return ""
}

// toolUses is the tool calls an assistant line holds.
func (l transcriptLine) toolUses() []toolUse {
	if l.Type != "assistant" {
		return nil
	}
	var blocks []struct {
		Type string
		toolUse
	}
	json.Unmarshal(l.Message.Content, &blocks)
	var uses []toolUse
	for _, b := range blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b.toolUse)
		}
	}
	return uses
}

// readTranscripts reads every transcript that matches pattern, in the order
// their sessions started.
func readTranscripts(pattern string) ([]transcript, error) {
	paths, _ := filepath.Glob(pattern)
	var all []transcript
	for _, path := range paths {
		t, err := readTranscript(path)
		if err != nil {
			return nil, err
		}
		all = append(all, t)
	}
	slices.SortStableFunc(all, func(a, b transcript) int { return a.start().Compare(b.start()) })
	return all, nil
}

// readTranscript reads the transcript at path. A line that doesn't read could
// hold a message's only usage, so it fails the read rather than leave the cost
// short.
func readTranscript(path string) (transcript, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return transcript{}, err
	}
	t := transcript{path: path}
	lines := bufio.NewScanner(bytes.NewReader(b))
	lines.Buffer(nil, 64<<20)
	for n := 1; lines.Scan(); n++ {
		var l transcriptLine
		if len(bytes.TrimSpace(lines.Bytes())) == 0 {
			continue
		} else if err := json.Unmarshal(lines.Bytes(), &l); err != nil {
			return transcript{}, fmt.Errorf("%s: line %d: %v", path, n, err)
		}
		t.lines = append(t.lines, l)
	}
	if err := lines.Err(); err != nil {
		return transcript{}, fmt.Errorf("%s: %v", path, err)
	}
	return t, nil
}

// start is when the transcript's session started: its first time.
func (t transcript) start() time.Time {
	for _, l := range t.lines {
		if !l.Timestamp.IsZero() {
			return l.Timestamp
		}
	}
	return time.Time{}
}

// stepper follows a worker's main sessions through the steps of crew's
// process. Each prompt crew sends the worker starts a step, and so does each
// step's own mark in the worker's tool calls: the skill or agent it runs, or,
// for evidence and PR, the first evidence entry or the PR. Anything else, such
// as a person's message, carries on the step it came in.
type stepper struct {
	step   string
	next   []string // the steps still to come in a review round, in the order the worker takes them
	rounds []ReviewRound
}

var (
	roundsPrompt = regexp.MustCompile(`(?i)review feedback is in rounds? (\d+)(?: to (\d+))?`)
	// A note that has the worker rebase or retarget its branch, or merge its
	// base in: crew's restack and retarget notes, and a lead's about a conflict
	// or asking for a rebase. "rebase" must start a word, so a task ID such as
	// 042-auto-rebase doesn't count.
	restackNote = regexp.MustCompile(`Restack \(|Retarget \(|\bconflicts with\b|(?i:(?:^|\s)rebase\b)`)
)

// prompt follows a prompt sent to the session: crew's open with "You are" or
// "New input for task".
func (s *stepper) prompt(p string) {
	if !strings.HasPrefix(p, "You are ") && !strings.HasPrefix(p, "New input for task ") {
		return
	}
	switch m := roundsPrompt.FindStringSubmatch(p); {
	case m != nil:
		s.next = s.roundSteps(atoiOr0(m[1]), cmp.Or(atoiOr0(m[2]), atoiOr0(m[1])))
		s.advance()
	case restackNote.MatchString(p):
		s.to(stepRestacks)
	case strings.Contains(p, "You are the implementer for task "):
		s.to(stepImplementing)
	}
}

// beforePR is the steps of a first run that come before evidence and PR.
var beforePR = []string{stepImplementing, stepExhale, stepPrePRReview, stepSecurityReview}

// toolUse follows a tool call of the worker's.
func (s *stepper) toolUse(u toolUse) {
	var in struct {
		Skill        string
		SubagentType string `json:"subagent_type"`
		Command      string
	}
	json.Unmarshal(u.Input, &in)
	skill := in.Skill[strings.LastIndex(in.Skill, ":")+1:] // "crew:pr-review" when it's a plugin's
	switch {
	case u.Name == "Skill" && skill == "simplify":
		s.to(stepExhale)
	case u.Name == "Skill" && skill == "security-review":
		s.to(stepSecurityReview)
	case u.Name == "Skill" && skill == "pr-review":
		s.to(stepAutomatedReview)
	case (u.Name == "Agent" || u.Name == "Task") && in.SubagentType == "casual-observer":
		s.to(stepPrePRReview)
	case slices.Contains(beforePR, s.step) && (strings.Contains(in.Command, "gh pr create") ||
		bytes.Contains(u.Input, []byte("/tasks/")) && bytes.Contains(u.Input, []byte("### 1. "))):
		s.to(stepEvidenceAndPR)
	// A round's conflict is done when the merge is pushed, and its red CI when
	// the worker posts what it found.
	case s.step == stepRestacks && strings.Contains(in.Command, "git push"),
		s.step == stepCIRounds && strings.Contains(in.Command, "crew ci-summary"):
		s.advance()
	}
}

func (s *stepper) to(step string) { s.step, s.next = step, nil }

func (s *stepper) advance() {
	if len(s.next) > 0 {
		s.step, s.next = s.next[0], s.next[1:]
	}
}

// roundSteps is the steps the worker takes for review rounds from to to, in
// the order its review-round workflow takes them: a conflict with the base,
// red CI, comments, and last another automated review, which its skill marks.
func (s stepper) roundSteps(from, to int) []string {
	var conflict, redCI, comments, reviewRequested bool
	for _, r := range s.rounds {
		if r.Number >= from && r.Number <= to {
			conflict, redCI, reviewRequested = conflict || r.Conflict, redCI || r.RedCI, reviewRequested || r.ReviewRequested
			comments = comments || len(r.Comments) > 0
		}
	}
	var steps []string
	if conflict {
		steps = append(steps, stepRestacks)
	}
	if redCI {
		steps = append(steps, stepCIRounds)
	}
	if comments || len(steps) == 0 && !reviewRequested {
		steps = append(steps, stepReviewRounds)
	}
	if len(steps) == 0 {
		steps = append(steps, stepAutomatedReview)
	}
	return steps
}

// briefTask is the task file a brief reviewer's prompt names, under whichever
// home it ran in: ".../projects/<project>/tasks/<task ID>.md".
var briefTask = regexp.MustCompile(`/projects/([^/\s]+)/tasks/([^/\s]+)\.md`)

// briefReviews is the transcript of every brief review on this machine, by the
// task it reviewed: "<project>/<task ID>". A lead or a goal lead runs them, so
// they can be in any folder. The agent was called plan-reviewer at first.
func briefReviews(transcripts string) (map[string][]string, error) {
	reviews := map[string][]string{}
	metas, _ := filepath.Glob(filepath.Join(transcripts, "*", "*", "subagents", "agent-*.meta.json"))
	for _, meta := range metas {
		path := strings.TrimSuffix(meta, ".meta.json") + ".jsonl"
		if a := subagentOf(path).AgentType; a != "brief-reviewer" && a != "plan-reviewer" {
			continue
		}
		review, err := readTranscript(path)
		if err != nil {
			return nil, err
		}
		for _, l := range review.lines {
			if p := l.prompt(); p != "" {
				if m := briefTask.FindStringSubmatch(p); m != nil {
					reviews[m[1]+"/"+m[2]] = append(reviews[m[1]+"/"+m[2]], path)
				}
				break
			}
		}
	}
	return reviews, nil
}
