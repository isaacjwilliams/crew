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
	"unicode/utf16"
)

// The steps a message is charged to, besides the review steps review_runs.go
// names. A subagent a worker starts for itself is a step of its own, named by
// its agent: "general-purpose", say, or a repo's own "test-review".
const (
	stepWorker   = "Worker sessions"
	stepLead     = "Lead sessions"
	stepGoalLead = "Goal-lead sessions"
	stepGardener = "Gardener"
	// crew review-eval's reviewer reruns, and the agents they start.
	stepReviewEval = "Review eval"
	// The agents a lead or goal lead starts, apart from brief reviews.
	stepLeadAgents     = "Lead's agents"
	stepGoalLeadAgents = "Goal leads' agents"
	stepOther          = "Other Claude Code sessions"
)

// The report lists the steps of a task in the order it meets them, then the
// agents its worker started, then the spend that's no task's.
var (
	taskSteps   = []string{stepBriefReview, stepWorker, stepExhale, stepPrePRReview, stepSecurityReview, stepAutomatedReview}
	sharedSteps = []string{stepLead, stepLeadAgents, stepGoalLead, stepGoalLeadAgents, stepGardener, stepReviewEval, stepOther}
)

var (
	// The agents crew has a worker start, by their step.
	agentSteps = map[string]string{"casual-observer": stepPrePRReview, "reviewer": stepAutomatedReview}
	// The step of a lead's or goal lead's agents, by the step of their session.
	leadAgentSteps = map[string]string{stepLead: stepLeadAgents, stepGoalLead: stepGoalLeadAgents}
	// The skills whose agents are a step, by the skill's name.
	skillSteps = map[string]string{"simplify": stepExhale, "security-review": stepSecurityReview}
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
	owners, tasks, err := c.transcriptOwners()
	if err != nil {
		return err
	}
	ts, err := scanTranscripts(claudeTranscripts(), owners)
	if err != nil {
		return err
	}
	elsewhere, err := c.tenuresElsewhere()
	if err != nil {
		return err
	}
	if ts.unread > 0 {
		warn("%d transcript lines didn't read, and aren't counted: a session may be writing them", ts.unread)
	}
	costsIn(ts, tasks, elsewhere, from, to).print(os.Stdout)
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
		owners[transcriptFolder(evalCheckout(p))] = owner{step: stepReviewEval}
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
// A name over 200 characters is cut there, and a hash of the whole path
// follows, so two long paths with the same start stay apart.
func transcriptFolder(dir string) string {
	const longest = 200
	units := utf16.Encode([]rune(dir)) // Claude Code counts and hashes UTF-16 units
	name := make([]byte, len(units))
	var hash int32
	for i, u := range units {
		name[i] = '-'
		if u < 0x80 && isLetterOrDigit(byte(u)) {
			name[i] = byte(u)
		}
		hash = hash*31 + int32(u)
	}
	if len(name) <= longest {
		return string(name)
	}
	return fmt.Sprintf("%s-%s", name[:longest], strconv.FormatInt(abs(int64(hash)), 36))
}

func isLetterOrDigit(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// A charge is one API message, and the step and task it's charged to.
type charge struct {
	owner
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
// once, to the owner of its folder in owners.
func scanTranscripts(dir string, owners map[string]owner) (transcripts, error) {
	s := costScan{transcripts: transcripts{completeFrom: time.Now()}}
	folders, err := os.ReadDir(dir)
	if err != nil {
		return transcripts{}, err
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

// folder charges the sessions in a transcript folder to its owner o: its main
// sessions, then their subagents. In a worker's folder, each subagent is
// charged to its own step (subagentStep), which needs the main sessions read
// first: they show which Agent calls a skill made.
func (s *costScan) folder(dir string, o owner) error {
	skillAgents := map[string]string{} // the step of each Agent call a skill made, by the call's ID
	mains, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range mains {
		var run skillRun
		err := s.read(path, func(l transcriptLine) {
			s.charge(o, l)
			if o.step == stepWorker {
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

// charge charges the message on line l to o, unless it was charged already.
func (s *costScan) charge(o owner, l transcriptLine) {
	if m, isNew := s.messages.add(l); isNew {
		s.charges = append(s.charges, &charge{o, m})
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
//
// For example, in the calls Skill(simplify), Agent, Agent, Bash, Agent, the
// first two Agent calls are simplify's. The Bash call closes the window, so
// the last one is the worker's own.
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
		r.step = "" // any other tool call, after the skill's agents, closes the window
	}
}

// subagentOwner is who the subagent whose transcript is at path is charged to,
// when it ran in a folder owned by o: its step (subagentStep), and o's task. A
// brief review is charged to the task whose file its prompt names instead,
// wherever it ran: a lead or a goal lead gives it the file's path and nothing
// else.
func subagentOwner(path string, o owner, skillAgents map[string]string) (owner, error) {
	step := subagentStep(path, o.step, skillAgents)
	if step != stepBriefReview {
		return owner{step, o.task}, nil
	}
	prompt, err := firstPrompt(path)
	if err != nil {
		return owner{}, err
	}
	if m := briefTask.FindStringSubmatch(prompt); m != nil {
		return owner{step, m[1] + "/" + m[2]}, nil
	}
	return owner{step: step}, nil
}

// briefTask is the task file a brief reviewer's prompt names, under whichever
// home it ran in: ".../projects/<project>/tasks/<task ID>.md".
var briefTask = regexp.MustCompile(`/projects/([^/\s]+)/tasks/([^/\s]+)\.md`)

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

// costReport is what the steps cost over a range of days, and what they cost
// each task that finished in it.
type costReport struct {
	from, to     time.Time // the first and last day
	completeFrom time.Time // from when this machine's transcripts hold every message
	steps        map[string]float64
	total        float64
	unpriced     map[string]int // the tokens sent in the range of each model there's no rate for
	rated        []string       // the models priced, for the rates the report gives
	// What each task the medians count cost in each step it ran, and in all.
	perTask    map[string][]float64
	taskTotals []float64
	leftOut    leftOut
}

// leftOut is the tasks that finished in the range but that the medians leave
// out, by why.
type leftOut struct {
	begunEarly   int      // begun before the transcripts are complete
	underWay     int      // under way while another machine ran the crew
	machines     []string // those other machines
	partlyPriced int      // with a message there's no rate for
}

// costsIn is the report on the transcripts from the first day to the last,
// both included. A total counts what was sent in the range. A median counts
// all that each of the wholeTasks spent, whenever it was sent, over the tasks
// that ran the step, but not a task with a message there's no rate for.
func costsIn(ts transcripts, tasks map[string]Task, elsewhere []tenure, from, to time.Time) costReport {
	end := to.AddDate(0, 0, 1)
	r := costReport{from: from, to: to, completeFrom: ts.completeFrom,
		steps: map[string]float64{}, unpriced: map[string]int{}, perTask: map[string][]float64{}}
	whole := r.wholeTasks(tasks, elsewhere, from, end)
	for _, c := range ts.charges {
		sent, task := !c.at.Before(from) && c.at.Before(end), whole[c.task]
		if !sent && task == nil {
			continue
		}
		rt, priced := apiRates[c.model]
		if !priced {
			if sent {
				r.unpriced[c.model] += c.usage.total()
			}
			if task != nil {
				task.unpriced = true
			}
			continue
		}
		if !slices.Contains(r.rated, c.model) {
			r.rated = append(r.rated, c.model)
		}
		cost := rt.dollars(c.usage)
		if sent {
			r.steps[c.step] += cost
			r.total += cost
		}
		if task != nil {
			task.steps[c.step] += cost
		}
	}
	for _, t := range whole {
		if t.unpriced {
			r.leftOut.partlyPriced++
			continue
		}
		total := 0.0
		for step, cost := range t.steps {
			r.perTask[step] = append(r.perTask[step], cost)
			total += cost
		}
		r.taskTotals = append(r.taskTotals, total)
	}
	slices.Sort(r.rated)
	slices.Sort(r.leftOut.machines)
	r.leftOut.machines = slices.Compact(r.leftOut.machines)
	return r
}

// taskSpend is what a task cost, by step, and whether a message of its has no
// rate.
type taskSpend struct {
	steps    map[string]float64
	unpriced bool
}

// wholeTasks are the tasks that merged or were done from one time to another
// whose spend this machine's transcripts hold whole, by task ID
// ("<project>/<task>"), each with nothing spent yet. It counts the others in
// r.leftOut: a task begun before the transcripts are complete may have lost
// sessions to Claude Code's age cleanup (a task with no created date counts as
// one), and one under way while another machine ran the crew has sessions there.
func (r *costReport) wholeTasks(tasks map[string]Task, elsewhere []tenure, from, to time.Time) map[string]*taskSpend {
	whole := map[string]*taskSpend{}
	for id, t := range tasks {
		finished := finishedAt(t)
		if finished.Before(from) || !finished.Before(to) {
			continue
		}
		created, _ := time.Parse(time.RFC3339, t.Get("created"))
		var during []string // the machines that ran the crew while the task was under way
		for _, e := range elsewhere {
			if e.during(created, finished) {
				during = append(during, e.holder)
			}
		}
		switch {
		case created.Before(r.completeFrom):
			r.leftOut.begunEarly++
		case len(during) > 0:
			r.leftOut.underWay++
			r.leftOut.machines = append(r.leftOut.machines, during...)
		default:
			whole[id] = &taskSpend{steps: map[string]float64{}}
		}
	}
	return whole
}

// finishedAt is when the task last merged or was done, or zero if it isn't
// merged or done. Its Log runs oldest first, so the last change wins.
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
// all spend and its median per task, then which tasks the medians count, how
// far back the data goes, what wasn't priced and the rates used.
func (r costReport) print(w io.Writer) {
	fmt.Fprintf(w, "Spend from %s to %s, at API rates, from this machine's transcripts:\n\n",
		r.from.Format("2006-01-02"), r.to.Format("2006-01-02"))
	// A step shows when something was spent on it in the range, or a task the
	// medians count ran it.
	agents := slices.Concat(slices.Collect(maps.Keys(r.steps)), slices.Collect(maps.Keys(r.perTask)))
	agents = slices.DeleteFunc(agents, func(name string) bool {
		return slices.Contains(taskSteps, name) || slices.Contains(sharedSteps, name)
	})
	slices.Sort(agents)
	agents = slices.Compact(agents)
	// A row's median, and how many tasks it's over, show only when there are any.
	row := func(name string, total float64, perTask []float64) []string {
		cells := []string{name, formatDollars(total), r.share(total)}
		if len(perTask) > 0 {
			cells = append(cells, strconv.Itoa(len(perTask)), formatDollars(median(perTask)))
		}
		return cells
	}
	var rows [][]string
	for _, group := range [][]string{slices.Concat(taskSteps, agents), sharedSteps} {
		var groupRows [][]string
		for _, name := range group {
			total, spent := r.steps[name]
			if perTask := r.perTask[name]; spent || len(perTask) > 0 {
				groupRows = append(groupRows, row(name, total, perTask))
			}
		}
		if len(rows) > 0 && len(groupRows) > 0 {
			rows = append(rows, nil)
		}
		rows = append(rows, groupRows...)
	}
	rows = append(rows, nil, row("All", r.total, r.taskTotals))
	printTable(w, [][2]string{{"", "step"}, {"", "total"}, {"", "share"}, {"", "tasks"}, {"median", "per task"}}, rows)

	fmt.Fprintln(w, "\nA median is over the tasks that merged or were done in the range, counting all they spent, whenever they spent it: each step's over the tasks that ran it.")
	fmt.Fprintf(w, "It counts only tasks this machine's transcripts hold whole: those begun since %s, from when they're complete, and not under way while another machine ran the crew.",
		r.completeFrom.Local().Format(logTime))
	if left := r.leftOut.reasons(); len(left) > 0 {
		fmt.Fprintf(w, " Left out: %s.", andList(left))
	}
	fmt.Fprintln(w)
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

// reasons says how many tasks were left out for each reason there is.
func (l leftOut) reasons() []string {
	var reasons []string
	if l.begunEarly > 0 {
		reasons = append(reasons, fmt.Sprintf("%d begun before then", l.begunEarly))
	}
	if l.underWay > 0 {
		reasons = append(reasons, fmt.Sprintf("%d under way while %s ran the crew", l.underWay, orList(l.machines)))
	}
	if l.partlyPriced > 0 {
		reasons = append(reasons, fmt.Sprintf("%d with a message there's no rate for", l.partlyPriced))
	}
	return reasons
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

// formatDollars is an amount as a person needs it: "$1,675", or "$4.27" under $10.
func formatDollars(x float64) string {
	if x < 10 {
		return fmt.Sprintf("$%.2f", x)
	}
	return "$" + thousands(int(math.Round(x)))
}
