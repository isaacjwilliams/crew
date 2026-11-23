package main

// crew costs: what each step of crew's process cost over a range of days,
// priced at API rates, read from this machine's Claude Code transcripts.

import (
	"bufio"
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
	stepOther    = "Other Claude Code sessions"
)

// The report lists a task's steps in the order it meets them, then the agents
// its worker started, then the spend that's no task's.
var (
	taskSteps   = []string{stepBriefReview, stepWorker, stepExhale, stepPrePRReview, stepSecurityReview, stepAutomatedReview}
	sharedSteps = []string{stepLead, stepGoalLead, stepGardener, stepOther}
)

// The agents crew has a worker start, by the step each is, and the skills
// whose agents are a step.
var (
	agentSteps = map[string]string{"casual-observer": stepPrePRReview, "reviewer": stepAutomatedReview}
	skillSteps = map[string]string{"simplify": stepExhale, "security-review": stepSecurityReview}
)

// rate is a model's API price, in dollars per million tokens. A cache write
// kept for five minutes costs 1.25 times input, and one kept for an hour twice.
type rate struct{ input, output, cacheRead float64 }

// apiRates are Anthropic's API prices on the date ratesDate.
var apiRates = map[string]rate{
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-sonnet-5-5": {2, 10, 0.10},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01},
	"claude-fable-5-2":  {8, 40, 0.20},
}

const ratesDate = "2026-11-03"

// dollars is what usage u cost at rate r.
func (r rate) dollars(u claudeUsage) float64 {
	oneHour := float64(u.CacheWrites.OneHour)
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
	owners, tasks, err := c.transcriptOwners()
	if err != nil {
		return err
	}
	charges, unread, err := scanTranscripts(claudeTranscripts(), owners)
	if err != nil {
		return err
	}
	if unread > 0 {
		warn("%d transcript lines didn't read, and aren't counted: a session may be writing them", unread)
	}
	costsIn(charges, tasks, from, to).print(os.Stdout)
	return nil
}

// owner is who a transcript folder's sessions are charged to: a step, and for a
// task's worktree the task, as "<project>/<task ID>".
type owner struct{ step, task string }

// transcriptOwners is the owner of each folder Claude Code keeps crew's
// transcripts in, and every task in the ledger, by the name its owner gives it.
// A folder not listed is no part of crew's.
func (c *Crew) transcriptOwners() (map[string]owner, map[string]Task, error) {
	projects, err := c.ledgerProjects("")
	if err != nil {
		return nil, nil, err
	}
	owners := map[string]owner{transcriptFolder(c.path("lead")): {step: stepLead}}
	tasks := map[string]Task{}
	for _, p := range projects {
		owners[transcriptFolder(gardenerCheckout(p))] = owner{step: stepGardener}
		for _, g := range c.goals(p.Name) {
			owners[transcriptFolder(goalCheckout(p, g))] = owner{step: stepGoalLead}
		}
		for _, t := range c.tasks(p.Name) {
			id := p.Name + "/" + t.ID()
			tasks[id] = t
			owners[transcriptFolder(p.taskWorktree(t))] = owner{stepWorker, id}
		}
	}
	return owners, tasks, nil
}

// transcriptFolder is the folder Claude Code keeps the transcripts of sessions
// started in dir in: dir with every character but a letter or digit made "-".
func transcriptFolder(dir string) string {
	return notLetterOrDigit.ReplaceAllString(dir, "-")
}

var notLetterOrDigit = regexp.MustCompile(`[^A-Za-z0-9]`)

// A charge is one API message: the step and task it's charged to, its model,
// when it was sent and the tokens it used.
type charge struct {
	owner
	model string
	at    time.Time
	usage claudeUsage
}

// scanTranscripts charges every message in the transcripts under dir, each
// once, to its owner. It counts the lines that didn't read.
func scanTranscripts(dir string, owners map[string]owner) ([]*charge, int, error) {
	s := costScan{byID: map[string]*charge{}}
	folders, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range folders {
		if !f.IsDir() {
			continue
		}
		o, ok := owners[f.Name()]
		if !ok {
			o = owner{step: stepOther}
		}
		if err := s.folder(filepath.Join(dir, f.Name()), o); err != nil {
			return nil, 0, err
		}
	}
	return s.charges, s.unread, nil
}

// costScan is the messages charged so far, in the order they were first read
// and by their ID, so a message Claude Code wrote more than once counts once.
type costScan struct {
	charges []*charge
	byID    map[string]*charge
	unread  int
}

// folder charges the sessions in a transcript folder to its owner o: its main
// sessions, then their subagents. In a task's folder, each subagent is charged
// to its own step (subagentOwner).
func (s *costScan) folder(dir string, o owner) error {
	skillAgents := map[string]string{} // the step of each Agent call a skill made, by the call's ID
	mains, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range mains {
		var run skillRun
		err := s.read(path, func(l transcriptLine) {
			s.charge(o, l)
			if o.task != "" {
				for _, u := range l.toolUses() {
					run.follow(u, skillAgents)
				}
			}
		})
		if err != nil {
			return err
		}
	}
	agents, _ := filepath.Glob(filepath.Join(dir, "*", "subagents", "agent-*.jsonl"))
	for _, path := range agents {
		ao, err := subagentOwner(path, o, skillAgents)
		if err != nil {
			return err
		}
		if err := s.read(path, func(l transcriptLine) { s.charge(ao, l) }); err != nil {
			return err
		}
	}
	return nil
}

// charge charges the message on line l to o, unless it was charged already:
// then only its usage is updated, since Claude Code writes the usage again with
// each of the message's content blocks, the last time in full. Claude Code's
// own messages, such as an API error, name the model <synthetic> and cost
// nothing.
func (s *costScan) charge(o owner, l transcriptLine) {
	m := l.Message
	if m.Usage == nil || m.Model == "<synthetic>" {
		return
	}
	if c, ok := s.byID[m.ID]; ok && m.ID != "" {
		c.usage = *m.Usage
		return
	}
	c := &charge{o, m.Model, l.Timestamp, *m.Usage}
	s.charges = append(s.charges, c)
	if m.ID != "" {
		s.byID[m.ID] = c
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

// scanLines calls f with each line of the transcript at path that holds want,
// read, in order, until f returns false. Lines without want aren't read, which
// keeps the scan quick, and a line that doesn't read is counted rather than
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
		var l transcriptLine
		if !bytes.Contains(lines.Bytes(), []byte(want)) {
			continue
		} else if json.Unmarshal(lines.Bytes(), &l) != nil {
			unread++
		} else if !f(l) {
			break
		}
	}
	if err := lines.Err(); err != nil {
		return unread, fmt.Errorf("%s: %v", path, err)
	}
	return unread, nil
}

// transcriptLine is what crew costs reads from a line of a transcript.
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

// toolUses is the tool calls an assistant line holds.
func (l transcriptLine) toolUses() []toolUse {
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

// skillRun follows a worker's session for the agents a skill starts. After a
// Skill call to /simplify or /security-review, its first Agent call, and those
// straight after it with no other tool call between, may be the skill's: a
// skill starts its agents together, and the worker's own usually come later.
// The worker can start its own straight after, though, so subagentOwner gives
// a skill only Claude Code's built-in agents, the only kind skills start.
type skillRun struct {
	step    string // the step of the skill whose agents are expected
	started bool   // its agents have started
}

// follow follows the session's next tool call, u, recording in agents the step
// of an Agent call the skill made.
func (r *skillRun) follow(u toolUse, agents map[string]string) {
	switch {
	case u.Name == "Skill":
		var in struct{ Skill string }
		json.Unmarshal(u.Input, &in)
		r.step, r.started = skillSteps[in.Skill], false
	case (u.Name == "Agent" || u.Name == "Task") && r.step != "":
		agents[u.ID], r.started = r.step, true
	case r.started:
		r.step = ""
	}
}

// subagentOwner is who the subagent whose transcript is at path is charged to,
// when it ran in a folder owned by o. A brief review is charged to the task
// whose file its prompt names, wherever it ran. In a task's folder, a built-in
// agent a skill started is charged to the skill's step, one crew has the worker
// start to that step, and any other to a step named by its agent. Elsewhere
// it's charged to o.
func subagentOwner(path string, o owner, skillAgents map[string]string) (owner, error) {
	meta := subagentOf(path)
	switch {
	case meta.AgentType == "brief-reviewer" || meta.AgentType == "plan-reviewer": // plan-reviewer is its old name
		prompt, err := firstPrompt(path)
		if m := briefTask.FindStringSubmatch(prompt); m != nil {
			return owner{stepBriefReview, m[1] + "/" + m[2]}, err
		}
		return owner{step: stepBriefReview}, err
	case o.task == "":
		return o, nil
	case meta.IsBuiltIn && skillAgents[meta.ToolUseID] != "":
		return owner{skillAgents[meta.ToolUseID], o.task}, nil
	case agentSteps[meta.AgentType] != "":
		return owner{agentSteps[meta.AgentType], o.task}, nil
	}
	return owner{cmp.Or(meta.AgentType, "unnamed agent"), o.task}, nil
}

// briefTask is the task file a brief reviewer's prompt names, under whichever
// home it ran in: ".../projects/<project>/tasks/<task ID>.md".
var briefTask = regexp.MustCompile(`/projects/([^/\s]+)/tasks/([^/\s]+)\.md`)

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

// firstPrompt is the text of the first prompt in the transcript at path. Claude
// Code writes tool results and its own notices as user lines too, but only a
// prompt as plain text.
func firstPrompt(path string) (string, error) {
	var prompt string
	_, err := scanLines(path, `"type":"user"`, func(l transcriptLine) bool {
		return l.Type != "user" || json.Unmarshal(l.Message.Content, &prompt) != nil
	})
	return prompt, err
}

// costReport is what the steps cost over a range of days.
type costReport struct {
	from, to  time.Time // the first and last day
	dataStart time.Time // when this machine's first message was sent
	steps     map[string]*stepCost
	total     float64
	perTask   []float64      // what each finished task cost in all
	unpriced  map[string]int // the tokens of each model there's no rate for
	rated     []string       // the models priced, for the rates the report gives
	leftOut   int            // the finished tasks the medians leave out
}

// stepCost is what a step cost in the range, and what it cost each finished
// task that ran it, whenever that task spent it.
type stepCost struct {
	dollars float64
	perTask []float64
}

// costsIn is the report on the charges from the first day to the last, both
// included. A total counts what was sent in the range. A median counts whole
// tasks, those that merged or were done in the range, whenever they spent it:
// each step's over the tasks that ran it. A task that began before this
// machine's transcripts, or has none, is left out of the medians.
func costsIn(charges []*charge, tasks map[string]Task, from, to time.Time) costReport {
	r := costReport{from: from, to: to, steps: map[string]*stepCost{}, unpriced: map[string]int{}}
	if len(charges) > 0 {
		r.dataStart = slices.MinFunc(charges, func(a, b *charge) int { return a.at.Compare(b.at) }).at
	}
	end := to.AddDate(0, 0, 1)
	inRange := func(at time.Time) bool { return !at.Before(from) && at.Before(end) }
	finished := map[string]map[string]float64{} // what each task the medians count cost, by step
	for id, t := range tasks {
		if !inRange(finishedAt(t)) {
			continue
		}
		if created, _ := time.Parse(time.RFC3339, t.Get("created")); created.Before(r.dataStart) {
			r.leftOut++
		} else {
			finished[id] = map[string]float64{}
		}
	}
	for _, c := range charges {
		inTask := finished[c.task] != nil
		if !inRange(c.at) && !inTask {
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
		dollars := rt.dollars(c.usage)
		if inRange(c.at) {
			r.step(c.step).dollars += dollars
			r.total += dollars
		}
		if inTask {
			finished[c.task][c.step] += dollars
		}
	}
	for _, steps := range finished {
		if len(steps) == 0 {
			r.leftOut++ // its sessions ran on another machine
			continue
		}
		total := 0.0
		for name, dollars := range steps {
			r.step(name).perTask = append(r.step(name).perTask, dollars)
			total += dollars
		}
		r.perTask = append(r.perTask, total)
	}
	slices.Sort(r.rated)
	return r
}

// step is the named step's cost, which starts at nothing.
func (r costReport) step(name string) *stepCost {
	if r.steps[name] == nil {
		r.steps[name] = &stepCost{}
	}
	return r.steps[name]
}

// finishedAt is when the task last merged or was done, or zero if it isn't
// merged or done.
func finishedAt(t Task) time.Time {
	var at time.Time
	if s := t.Status(); s != Merged && s != Done {
		return at
	}
	for _, e := range withStatusLines(t.LogEntries()) {
		if changedTo(Merged)(e) || changedTo(Done)(e) {
			at = e.at
		}
	}
	return at
}

// print writes the report: a row for each step, with its total, its share of
// all spend and its median per task, then what the medians cover, how far back
// the data goes, what wasn't priced and the rates used.
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
	var rows [][]string
	for _, group := range [][]string{slices.Concat(taskSteps, agents), sharedSteps} {
		var groupRows [][]string
		for _, name := range group {
			s := r.steps[name]
			switch {
			case s != nil && len(s.perTask) > 0:
				groupRows = append(groupRows, []string{name, dollars(s.dollars), r.share(s.dollars),
					strconv.Itoa(len(s.perTask)), dollars(median(s.perTask))})
			case s != nil:
				groupRows = append(groupRows, []string{name, dollars(s.dollars), r.share(s.dollars)})
			}
		}
		if len(rows) > 0 && len(groupRows) > 0 {
			rows = append(rows, nil)
		}
		rows = append(rows, groupRows...)
	}
	all := []string{"All", dollars(r.total), r.share(r.total)}
	if len(r.perTask) > 0 {
		all = append(all, strconv.Itoa(len(r.perTask)), dollars(median(r.perTask)))
	}
	rows = append(rows, nil, all)
	printTable(w, [][2]string{{"", "step"}, {"", "total"}, {"", "share"}, {"", "tasks"}, {"median", "per task"}}, rows)

	fmt.Fprintf(w, "\nMedians are of whole tasks, over the %d that merged or were done in the range: each step's over the tasks that ran it.", len(r.perTask))
	if r.leftOut > 0 {
		fmt.Fprintf(w, " %d more are left out, as some of their sessions aren't in this machine's transcripts.", r.leftOut)
	}
	fmt.Fprintln(w)
	if r.from.Before(r.dataStart) {
		fmt.Fprintf(w, "This machine's transcripts begin at %s: there's nothing from before then.\n", r.dataStart.Local().Format(logTime))
	}
	for _, model := range slices.Sorted(maps.Keys(r.unpriced)) {
		fmt.Fprintf(w, "Not priced, as there's no rate for it: %s, %s tokens.\n", model, thousands(r.unpriced[model]))
	}
	fmt.Fprintf(w, "Anthropic's API rates of %s, in dollars per million tokens of input, output and cache reads:\n", ratesDate)
	for _, model := range r.rated {
		rt := apiRates[model]
		fmt.Fprintf(w, "  %s: %g, %g, %g\n", model, rt.input, rt.output, rt.cacheRead)
	}
	fmt.Fprintln(w, "A cache write costs 1.25 times input when it's kept for five minutes, and twice input for an hour.")
}

// share is the part of all spend that dollars is, as a percentage.
func (r costReport) share(dollars float64) string {
	if r.total == 0 {
		return "-"
	}
	return decimal(100*dollars/r.total) + "%"
}

// dollars is an amount as a person needs it: "$1,675", or "$4.27" under $10.
func dollars(x float64) string {
	if x < 10 {
		return fmt.Sprintf("$%.2f", x)
	}
	return "$" + thousands(int(math.Round(x)))
}
