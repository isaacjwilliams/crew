package main

// crew costs: what each step of crew's process cost over a range of days,
// priced at API rates, read from this machine's Claude Code transcripts.

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The steps a message is charged to, besides the review steps review_runs.go
// names. A subagent a worker starts for itself is a step of its own, named by
// its agent: "general-purpose", say, or a repo's own "test-review".
const (
	stepWorker   = "Worker sessions"
	stepLead     = "Lead sessions"
	stepGoalLead = "Goal-lead sessions"
	stepGardener = "Gardener"
	// The agents a lead or goal lead starts, apart from brief reviews.
	stepLeadAgents     = "Lead's agents"
	stepGoalLeadAgents = "Goal leads' agents"
	stepOther          = "Other Claude Code sessions"
)

// The report lists the steps of a task in the order it meets them, then the
// agents its worker started, then the spend that's no task's.
var (
	taskSteps   = []string{stepBriefReview, stepWorker, stepExhale, stepPrePRReview, stepSecurityReview, stepAutomatedReview}
	sharedSteps = []string{stepLead, stepLeadAgents, stepGoalLead, stepGoalLeadAgents, stepGardener, stepOther}
)

// The agents crew has a worker start, by the step each is, and the skills
// whose agents are a step.
var (
	agentSteps = map[string]string{"casual-observer": stepPrePRReview, "reviewer": stepAutomatedReview}
	// The step of a lead's or goal lead's agents, by the step of their session.
	leadAgentSteps = map[string]string{stepLead: stepLeadAgents, stepGoalLead: stepGoalLeadAgents}
	skillSteps     = map[string]string{"simplify": stepExhale, "security-review": stepSecurityReview}
)

// rate is a model's API price, in dollars per million tokens. A cache write
// kept for five minutes costs 1.25 times input, and one kept for an hour twice.
type rate struct{ input, output, cacheRead float64 }

// apiRates are Anthropic's API prices on the date ratesDate. A model missing
// here is reported unpriced, which is the sign to add it.
var apiRates = map[string]rate{
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-sonnet-5-5": {2, 10, 0.10},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01},
	"claude-fable-5-2":  {8, 40, 0.20},
}

const ratesDate = "2026-11-03"

// dollars is what usage u cost at rate r.
func (r rate) dollars(u claudeUsage) float64 {
	oneHour := float64(u.CacheWriteKept.OneHour)
	fiveMinutes := float64(u.CacheWrite) - oneHour
	return (float64(u.Input)*r.input + fiveMinutes*r.input*1.25 + oneHour*r.input*2 +
		float64(u.CacheRead)*r.cacheRead + float64(u.Output)*r.output) / 1e6
}

// crew costs [--from YYYY-MM-DD] [--to YYYY-MM-DD]
//
// Prints what each step cost from the first day to the last, both included and
// in local time: the last 7 days by default. It only reads, so it needs no lock
// and runs on any machine, from the transcripts that machine has.
func cmdCosts(c *Crew, args []string) error {
	y, m, d := time.Now().Date()
	to := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	from := to.AddDate(0, 0, -6)
	for len(args) > 1 && (args[0] == "--from" || args[0] == "--to") {
		day, err := time.ParseInLocation("2006-01-02", args[1], time.Local)
		if err != nil {
			return fail("%s takes a date, YYYY-MM-DD, not '%s'", args[0], args[1])
		}
		if args[0] == "--from" {
			from = day
		} else {
			to = day
		}
		args = args[2:]
	}
	if len(args) > 0 {
		return usage("crew costs [--from YYYY-MM-DD] [--to YYYY-MM-DD]")
	}
	if to.Before(from) {
		return fail("--to %s is before --from %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	steps, err := c.transcriptSteps()
	if err != nil {
		return err
	}
	ts, err := scanTranscripts(claudeTranscripts(), steps)
	if err != nil {
		return err
	}
	if ts.unread > 0 {
		warn("%d transcript lines didn't read, and aren't counted: a session may be writing them", ts.unread)
	}
	costsIn(ts, from, to).print(os.Stdout)
	return nil
}

// transcriptSteps is the step each folder Claude Code keeps crew's transcripts
// in is charged to. A folder not listed is no part of crew's.
func (c *Crew) transcriptSteps() (map[string]string, error) {
	projects, err := c.ledgerProjects("")
	if err != nil {
		return nil, err
	}
	steps := map[string]string{transcriptFolder(c.path("lead")): stepLead}
	for _, p := range projects {
		steps[transcriptFolder(gardenerCheckout(p))] = stepGardener
		for _, g := range c.goals(p.Name) {
			steps[transcriptFolder(goalCheckout(p, g))] = stepGoalLead
		}
		for _, t := range c.tasks(p.Name) {
			steps[transcriptFolder(p.taskWorktree(t))] = stepWorker
		}
	}
	return steps, nil
}

// transcriptFolder is the folder Claude Code keeps the transcripts of sessions
// started in dir in: dir with every character but a letter or digit made "-".
func transcriptFolder(dir string) string {
	return notLetterOrDigit.ReplaceAllString(dir, "-")
}

var notLetterOrDigit = regexp.MustCompile(`[^A-Za-z0-9]`)

// A charge is one API message, and the step it's charged to.
type charge struct {
	step string
	*message
}

// transcripts is what this machine's transcripts hold: each message, charged
// once, and the time from which they hold every message.
type transcripts struct {
	charges []*charge
	// completeFrom is when the oldest session Claude Code deletes by age was
	// last written (sessionWritten). It deletes a session, its subagents'
	// transcripts with it, once the session's own transcript has gone unwritten
	// for cleanupPeriodDays, so every message sent since then is here. With no
	// such session, it's when the scan ran: there's no telling.
	completeFrom time.Time
	unread       int // the lines that didn't read
}

// scanTranscripts charges every message in the transcripts under dir, each
// once, to the step of its folder in steps.
func scanTranscripts(dir string, steps map[string]string) (transcripts, error) {
	s := costScan{transcripts: transcripts{completeFrom: time.Now()}}
	folders, err := os.ReadDir(dir)
	if err != nil {
		return transcripts{}, err
	}
	for _, f := range folders {
		if !f.IsDir() {
			continue
		}
		step, ok := steps[f.Name()]
		if !ok {
			step = stepOther
		}
		if err := s.folder(filepath.Join(dir, f.Name()), step); err != nil {
			return transcripts{}, err
		}
	}
	return s.transcripts, nil
}

// costScan is the transcripts read so far, with their messages, so a message
// Claude Code wrote more than once counts once.
type costScan struct {
	transcripts
	messages messages
}

// folder charges the sessions in a transcript folder to its step: its main
// sessions, then their subagents. In a worker's folder, each subagent is
// charged to its own step (subagentStep), which needs the main sessions read
// first: they show which Agent calls a skill made.
func (s *costScan) folder(dir, step string) error {
	skillAgents := map[string]string{} // the step of each Agent call a skill made, by the call's ID
	mains, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range mains {
		var run skillRun
		err := s.read(path, func(l transcriptLine) {
			s.charge(step, l)
			if step == stepWorker {
				for _, u := range l.toolUses() {
					run.follow(u, skillAgents)
				}
			}
		})
		if err != nil {
			return err
		}
		if err := s.sessionWritten(path); err != nil {
			return err
		}
	}
	agents, _ := filepath.Glob(filepath.Join(dir, "*", "subagents", "agent-*.jsonl"))
	for _, path := range agents {
		agentStep := subagentStep(path, step, skillAgents)
		if err := s.read(path, func(l transcriptLine) { s.charge(agentStep, l) }); err != nil {
			return err
		}
	}
	return nil
}

// charge charges the message on line l to step, unless it was charged already.
func (s *costScan) charge(step string, l transcriptLine) {
	if m, isNew := s.messages.add(l); isNew {
		s.charges = append(s.charges, &charge{step, m})
	}
}

// read calls f with each assistant line of the transcript at path, in order.
func (s *costScan) read(path string, f func(transcriptLine)) error {
	unread, err := scanLines(path, `"type":"assistant"`, func(l transcriptLine) bool {
		if l.Type == "assistant" {
			f(l)
		}
		return true
	})
	s.unread += unread
	return err
}

// sessionWritten takes the transcripts to be complete only from when the
// session at path was last written, if Claude Code deletes it by age: if it
// ran only from the command line. Claude Code keeps a session started or last
// continued in Claude Desktop or Cowork at any age, and an empty transcript
// too, so those don't count.
func (s *costScan) sessionWritten(path string) error {
	ran := false // whether every line that says how the session ran says the command line
	_, err := scanLines(path, `"entrypoint":`, func(l transcriptLine) bool {
		ran = l.Entrypoint == "cli" || l.Entrypoint == "sdk-cli"
		return ran
	})
	if err != nil || !ran {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.ModTime().Before(s.completeFrom) {
		s.completeFrom = info.ModTime()
	}
	return nil
}

// A tool call of the model's: its ID, the tool, and what it was given.
type toolUse struct {
	ID, Name string
	Input    json.RawMessage
}

// toolUses is the tool calls an assistant line holds.
func (l transcriptLine) toolUses() []toolUse {
	if !bytes.Contains(l.Message.Content, []byte(`"tool_use"`)) {
		return nil // most lines are text or thinking: no need to decode them
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

// skillRun picks out, in a worker's session, the Agent calls that may be a
// skill's. A Skill call to /simplify or /security-review opens a window. It
// stays open until the first Agent call, then for as long as Agent calls come
// one after another, and closes at the next other tool call: a skill starts
// its agents together. Agents the worker starts in that window are no skill's;
// subagentStep tells them apart, as a skill starts only built-in agents.
type skillRun struct {
	step          string // the step of the skill whose window is open, or ""
	agentsStarted bool   // an Agent call has come since the Skill call
}

// follow takes the session's next tool call, u, and records in agents the
// step of an Agent call made while a skill's window is open.
func (r *skillRun) follow(u toolUse, agents map[string]string) {
	switch {
	case u.Name == "Skill":
		var in struct{ Skill string }
		json.Unmarshal(u.Input, &in)
		r.step, r.agentsStarted = skillSteps[in.Skill], false
	case (u.Name == "Agent" || u.Name == "Task") && r.step != "":
		agents[u.ID], r.agentsStarted = r.step, true
	case r.agentsStarted:
		r.step = ""
	}
}

// subagentStep is the step the subagent whose transcript is at path is charged
// to, when it ran in a folder charged to folderStep. A brief review is its own
// step, wherever it ran. In a worker's folder, a built-in agent started in a
// skill's window is charged to the skill's step (any other kind is the
// worker's own), one crew has the worker start to that step, and any other to
// a step named by its agent. A lead's or goal lead's other agents are charged
// to a step for them, and any other folder's to the folder's step.
func subagentStep(path, folderStep string, skillAgents map[string]string) string {
	meta := subagentOf(path)
	switch {
	// plan-reviewer is the brief reviewer's name before 2026-11-17. Drop it
	// after 2026-12-17, when Claude Code's 30-day cleanup has removed those
	// transcripts.
	case meta.AgentType == "brief-reviewer" || meta.AgentType == "plan-reviewer":
		return stepBriefReview
	case folderStep != stepWorker:
		return cmp.Or(leadAgentSteps[folderStep], folderStep)
	case meta.IsBuiltIn && skillAgents[meta.ToolUseID] != "":
		return skillAgents[meta.ToolUseID]
	case agentSteps[meta.AgentType] != "":
		return agentSteps[meta.AgentType]
	}
	return cmp.Or(meta.AgentType, "unnamed agent")
}

// subagentMeta is what Claude Code records of a subagent beside its
// transcript: its agent, whether that's one of Claude Code's own, and the tool
// call that started it.
type subagentMeta struct {
	AgentType, ToolUseID string
	IsBuiltIn            bool
}

// subagentOf is the subagent whose transcript is at path, or nothing when
// Claude Code recorded nothing of it.
func subagentOf(path string) subagentMeta {
	var meta subagentMeta
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		json.Unmarshal(b, &meta)
	}
	return meta
}

// costReport is what the steps cost over a range of days.
type costReport struct {
	from, to     time.Time // the first and last day
	completeFrom time.Time // from when this machine's transcripts hold every message
	steps        map[string]float64
	total        float64
	unpriced     map[string]int // the tokens sent in the range of each model there's no rate for
	rated        []string       // the models priced, for the rates the report gives
}

// costsIn is the report on the transcripts from the first day to the last,
// both included: it counts what was sent in the range.
func costsIn(ts transcripts, from, to time.Time) costReport {
	end := to.AddDate(0, 0, 1)
	r := costReport{from: from, to: to, completeFrom: ts.completeFrom,
		steps: map[string]float64{}, unpriced: map[string]int{}}
	for _, c := range ts.charges {
		if c.at.Before(from) || !c.at.Before(end) {
			continue
		}
		rt, priced := apiRates[c.model]
		if !priced {
			r.unpriced[c.model] += c.usage.total()
			continue
		}
		if !slices.Contains(r.rated, c.model) {
			r.rated = append(r.rated, c.model)
		}
		cost := rt.dollars(c.usage)
		r.steps[c.step] += cost
		r.total += cost
	}
	slices.Sort(r.rated)
	return r
}

// print writes the report: a row for each step, with its total and its share
// of all spend, then how far back the data goes, what wasn't priced and the
// rates used.
func (r costReport) print(w io.Writer) {
	fmt.Fprintf(w, "Spend from %s to %s, at API rates, from this machine's transcripts:\n\n",
		r.from.Format("2006-01-02"), r.to.Format("2006-01-02"))
	var agents []string
	for name := range r.steps {
		if !slices.Contains(taskSteps, name) && !slices.Contains(sharedSteps, name) {
			agents = append(agents, name)
		}
	}
	slices.Sort(agents)
	row := func(name string, total float64) []string { return []string{name, dollars(total), r.share(total)} }
	var rows [][]string
	for _, group := range [][]string{slices.Concat(taskSteps, agents), sharedSteps} {
		var groupRows [][]string
		for _, name := range group {
			if total, ok := r.steps[name]; ok {
				groupRows = append(groupRows, row(name, total))
			}
		}
		if len(rows) > 0 && len(groupRows) > 0 {
			rows = append(rows, nil)
		}
		rows = append(rows, groupRows...)
	}
	rows = append(rows, nil, row("All", r.total))
	printTable(w, [][2]string{{"", "step"}, {"", "total"}, {"", "share"}}, rows)

	if r.from.Before(r.completeFrom) {
		fmt.Fprintf(w, "\nThis machine's transcripts are complete only from %s: older sessions may have been deleted.\n", r.completeFrom.Local().Format(logTime))
	}
	for _, model := range slices.Sorted(maps.Keys(r.unpriced)) {
		fmt.Fprintf(w, "Not priced, as there's no rate for it: %s, %s tokens.\n", model, thousands(r.unpriced[model]))
	}
	fmt.Fprintf(w, "\nAnthropic's API rates of %s, in dollars per million tokens of input, output and cache reads:\n", ratesDate)
	for _, model := range r.rated {
		rt := apiRates[model]
		fmt.Fprintf(w, "  %s: %g, %g, %g\n", model, rt.input, rt.output, rt.cacheRead)
	}
	fmt.Fprintln(w, "A cache write costs 1.25 times input when it's kept for five minutes, and twice input for an hour.")
}

// share is the part of all spend that dollars is, as a person needs it: "61%",
// or "5.8%" under 10%.
func (r costReport) share(dollars float64) string {
	if r.total == 0 {
		return "-"
	}
	percent := 100 * dollars / r.total
	if percent < 10 {
		return decimal(percent) + "%"
	}
	return strconv.Itoa(int(math.Round(percent))) + "%"
}

// dollars is an amount as a person needs it: "$1,675", or "$4.27" under $10.
func dollars(x float64) string {
	if x < 10 {
		return fmt.Sprintf("$%.2f", x)
	}
	return "$" + thousands(int(math.Round(x)))
}
