package main

// crew status's live view: the lines the plain text prints (reportLines), in a
// terminal, built again every few seconds, with folds the user opens and closes,
// and actions the user takes on the task under the cursor.

import (
	"bytes"
	"cmp"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// How often the live view builds its report from the ledger and what it last
// read, and how often it reads again: the workers claude agents lists, the
// tasks' branches in git, the PRs on GitHub and, on a machine that doesn't run
// the crew, the ledger. A build starts no process, so it's cheap to run often.
// A beat every second starts whichever is due.
const (
	buildEvery        = 3 * time.Second
	listWorkersEvery  = 15 * time.Second
	readBranchesEvery = 15 * time.Second
	readGitHubEvery   = 30 * time.Second
	pullLedgerEvery   = time.Minute
	beatEvery         = time.Second
)

// statusFeed is what crew status runs: pulling the ledger, listing the
// workers, reading the tasks' branches, reading GitHub for the open PRs, and
// building a report from those reads; and, for the user's actions, opening a PR
// and sending a worker a note. The live view runs each in the background, on
// its own, so one that hangs holds back none of the others.
type statusFeed struct {
	pullLedger   func()
	listWorkers  func() *workerList
	readBranches func() branchReads
	readPRs      func() prReads
	// build's error says the shared settings couldn't be read again, so the
	// report went by the ones crew started with.
	build func(statusReads) (statusReport, error)
	// openPR's and sendNote's errors are a line each, for the view to show,
	// and so is the warning sendNote gives when crew sent the note but warned.
	openPR   func(url string) error
	sendNote func(t Task, note string) (warning string, err error)
}

// feedOf is crew status's feed for the scope it takes: one project, or "" for
// all of them. Each read and build asks which projects that is, as they may
// change while the view is open.
func feedOf(c *Crew, scope string, doneToo bool) statusFeed {
	return statusFeed{
		pullLedger:   c.catchUpLedger,
		listWorkers:  c.listWorkers,
		readBranches: func() branchReads { return c.readBranches(c.projects(scope)) },
		readPRs:      func() prReads { return c.readPRs(c.projects(scope)) },
		build: func(reads statusReads) (statusReport, error) {
			// The shared settings are read again for each report, so a change
			// to crew.env shows while the view is open.
			now := *c
			s, err := sharedSettings(c.Home)
			if err == nil {
				now.Settings = s
			}
			return buildReport(&now, c.projects(scope), doneToo, reads), err
		},
		openPR: browsePR,
		// The note goes through crew followup, in a process of its own, so
		// the ledger's lock is held only while it runs.
		sendNote: func(t Task, note string) (string, error) {
			var errs strings.Builder
			failed := c.step(&errs, "followup", t.Path, "--note", note, "--from-user") != 0
			return followupOutcome(failed, errs.String())
		},
	}
}

// followupOutcome reads what crew followup wrote to stderr: why it refused,
// when it failed, or else every warning crew gave, in one line. claude's own
// notes on starting a session aren't crew's warnings, so they're left out.
func followupOutcome(failed bool, stderr string) (warning string, err error) {
	if failed {
		_, why := splitRefusal(stderr)
		return "", errors.New(cmp.Or(why, "crew followup failed"))
	}
	var warnings []string
	for _, line := range strings.Split(stderr, "\n") {
		if w, ok := strings.CutPrefix(line, "crew: "); ok {
			warnings = append(warnings, w)
		}
	}
	return strings.Join(warnings, "; "), nil
}

// browsePR opens a PR in the browser, with gh's output kept off the screen. A
// browser that gh starts may hold on to gh's output, so browsePR stops waiting
// for it a second after gh ends.
func browsePR(url string) error {
	var out bytes.Buffer
	cmd := exec.Command("gh", "pr", "view", url, "--web")
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil || errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	_, why := splitRefusal(out.String())
	return errors.New(cmp.Or(why, err.Error()))
}

// watchStatus runs the live view until the user quits. Its beat comes from
// outside the view, so a beat with nothing due gives no command, and tests can
// beat it on their own clock without waiting a real second.
func watchStatus(feed statusFeed) error {
	p := tea.NewProgram(newLiveStatus(feed))
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(beatEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.Send(beat{})
			case <-stop:
				return
			}
		}
	}()
	_, err := p.Run()
	return err
}

// job is one of the view's background jobs: building the report, or one of the
// reads it's built from. It runs once at a time, at most every so often,
// unless it's asked for sooner.
type job struct {
	every   time.Duration
	running bool
	last    time.Time // when it last started; zero before the first time
	asked   bool      // wanted before its time (see ask)
}

// ask has the job start as soon as it isn't running: once it's done, if it is.
func (j *job) ask() { j.asked = true }

// starts says whether the job starts now, and marks it running if it does.
// One that's asked for while it runs starts once it's done.
func (j *job) starts(now time.Time) bool {
	if j.running || (!j.asked && now.Sub(j.last) < j.every) {
		return false
	}
	j.running, j.last, j.asked = true, now, false
	return true
}

// liveStatus is the live view: the latest report's lines, which of them are on
// show, and the line the cursor is on.
type liveStatus struct {
	feed statusFeed
	now  func() time.Time
	// reads is what the reports are built from besides the ledger. Each is nil
	// before its first read ends, so the first reports say what isn't read
	// yet, until the read that ends builds another.
	reads statusReads

	building, listingWorkers, readingBranches, readingGitHub, pulling job

	lines   []statusLine // the latest report's; nil before the first
	builtAt time.Time
	// readOnlyOn is the machine that runs the crew, as of the latest report,
	// when it isn't this one; empty when this machine runs it.
	readOnlyOn string
	// settingsErr is why the latest report went by the settings crew started
	// with: the shared settings couldn't be read again.
	settingsErr error
	// open is whether the user left a line open, by its key. A line the user
	// hasn't opened or closed is open unless the plain text folds it.
	open          map[string]bool
	shown         []shownLine // every line not under a closed one, in order
	cursor        int         // the line it's on, in shown
	top           int         // the first line of shown on the screen
	width, height int

	note *noteDraft // the note the user is typing, if they are
	// held is the notes the user finished that crew hasn't sent, by their
	// task's path: one on its way, or one crew refused, for n on the task to
	// bring back.
	held map[string]heldNote
	said string // a line on what an action did, or why it couldn't
}

// noteDraft is a note the user is typing to a task's worker.
type noteDraft struct {
	to    Task // the task the note was started on, wherever its line goes
	input textinput.Model
}

// heldNote is a note the user finished that crew hasn't sent.
type heldNote struct {
	text    string
	sending bool // crew followup has it now
}

// action is something the user can do to the task under the cursor, by its key.
type action struct {
	key  string
	name string // what the bar calls it
	// refuses says why the line's task doesn't take the action, or "" when it
	// does. row is nil on a line that isn't a task's.
	refuses func(m *liveStatus, row *taskRow) string
	start   func(m *liveStatus, task Task) tea.Cmd
}

var actions = []action{
	{key: "o", name: "open PR", refuses: (*liveStatus).refusesOpenPR, start: (*liveStatus).openPR},
	{key: "n", name: "note", refuses: (*liveStatus).refusesNote, start: (*liveStatus).startNote},
}

// shownLine is a line on show, with how deep it is in the tree.
type shownLine struct {
	statusLine
	depth int
	open  bool
}

// The messages the view's jobs and its beat send it.
type (
	beat          struct{}
	ledgerPulled  struct{}
	workersListed struct{ workers *workerList }
	branchesRead  struct{ branches branchReads }
	prsRead       struct{ reads prReads }
	reportBuilt   struct {
		report      statusReport
		settingsErr error
	}
	prOpened struct {
		task Task
		err  error
	}
	noteSent struct {
		to            Task
		text, warning string
		err           error
	}
	// noteInput is what a note's own command sent back (the clipboard, say),
	// for the draft that started it.
	noteInput struct {
		draft *noteDraft
		msg   tea.Msg
	}
)

func newLiveStatus(feed statusFeed) *liveStatus {
	return &liveStatus{
		feed: feed, now: time.Now,
		building: job{every: buildEvery}, listingWorkers: job{every: listWorkersEvery},
		readingBranches: job{every: readBranchesEvery}, readingGitHub: job{every: readGitHubEvery}, pulling: job{every: pullLedgerEvery},
		open: map[string]bool{}, held: map[string]heldNote{}, width: 80, height: 24,
	}
}

func (m *liveStatus) Init() tea.Cmd { return m.startDue() }

// startDue starts, in the background, each job that's due and not running.
func (m *liveStatus) startDue() tea.Cmd {
	now, feed := m.now(), m.feed
	var cmds []tea.Cmd
	if m.pulling.starts(now) {
		cmds = append(cmds, func() tea.Msg { feed.pullLedger(); return ledgerPulled{} })
	}
	if m.listingWorkers.starts(now) {
		cmds = append(cmds, func() tea.Msg { return workersListed{feed.listWorkers()} })
	}
	if m.readingBranches.starts(now) {
		cmds = append(cmds, func() tea.Msg { return branchesRead{feed.readBranches()} })
	}
	if m.readingGitHub.starts(now) {
		cmds = append(cmds, func() tea.Msg { return prsRead{feed.readPRs()} })
	}
	if m.building.starts(now) {
		reads := m.reads
		cmds = append(cmds, func() tea.Msg {
			r, err := feed.build(reads)
			return reportBuilt{r, err}
		})
	}
	return tea.Batch(cmds...)
}

// readEnded marks a pull or a read done, and asks for a build to show what it
// brought in.
func (m *liveStatus) readEnded(j *job) tea.Cmd {
	j.running = false
	m.building.ask()
	return m.startDue()
}

// Update starts a build as soon as a pull or a read ends, so what it brought in
// shows at once; the beat starts each job again when it's due. What it does may
// change how many lines of the report fit, so it scrolls each time.
func (m *liveStatus) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	m.scroll()
	return m, cmd
}

// update does what msg asks, and returns what to run next.
func (m *liveStatus) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.note != nil {
			m.note.input.SetWidth(m.noteWidth())
		}
	case beat:
		return m.startDue()
	case ledgerPulled:
		return m.readEnded(&m.pulling)
	case workersListed:
		m.reads.workers = msg.workers
		return m.readEnded(&m.listingWorkers)
	case branchesRead:
		m.reads.branches = msg.branches
		return m.readEnded(&m.readingBranches)
	case prsRead:
		m.reads.prs = msg.reads
		return m.readEnded(&m.readingGitHub)
	case reportBuilt:
		m.building.running = false
		m.settingsErr = msg.settingsErr
		m.show(msg.report, m.building.last)
		return m.startDue()
	case prOpened:
		if msg.err != nil {
			m.said = "couldn't open " + msg.task.ID() + "'s PR: " + msg.err.Error()
		} else {
			m.said = "opened " + msg.task.ID() + "'s PR in the browser"
		}
	case noteSent:
		delete(m.held, msg.to.Path)
		switch {
		case msg.err != nil:
			m.held[msg.to.Path] = heldNote{text: msg.text}
			m.said = "note not sent, n on " + msg.to.ID() + " brings it back: " + msg.err.Error()
		case msg.warning != "":
			m.said = "sent your note to " + msg.to.ID() + "'s worker, but: " + msg.warning
		default:
			m.said = "sent your note to " + msg.to.ID() + "'s worker"
		}
		// The worker's line shows it at work on the note.
		m.listingWorkers.ask()
		return m.startDue()
	case tea.KeyPressMsg:
		if m.note != nil {
			return m.typeNote(msg)
		}
		return m.press(msg.String())
	case tea.PasteMsg:
		if m.note != nil {
			return m.typeNote(msg)
		}
	case noteInput:
		// A draft that's gone since doesn't pass what it asked for to the next.
		if msg.draft == m.note {
			return m.typeNote(msg.msg)
		}
	}
	return nil
}

// show puts a new report on screen, with the cursor on the line it was on, or
// as near to where it was as there's a line, when that one's gone.
func (m *liveStatus) show(r statusReport, at time.Time) {
	var key string
	if l, ok := m.current(); ok {
		key = l.key
	}
	m.lines, m.builtAt, m.readOnlyOn = reportLines(r), at, r.ReadOnlyOn
	m.layOut()
	i := slices.IndexFunc(m.shown, func(l shownLine) bool { return l.key == key })
	if i < 0 {
		i = m.cursor
	}
	m.moveTo(i)
}

func (m *liveStatus) press(key string) tea.Cmd {
	m.said = ""
	switch key {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		m.moveTo(m.cursor - 1)
	case "down", "j":
		m.moveTo(m.cursor + 1)
	case "pgup":
		m.moveTo(m.cursor - m.bodyHeight())
	case "pgdown":
		m.moveTo(m.cursor + m.bodyHeight())
	case "home", "g":
		m.moveTo(0)
	case "end", "G":
		m.moveTo(len(m.shown) - 1)
	case "left", "h":
		m.closeOrUp()
	case "right", "l":
		m.setOpen(true)
	case "enter", "space":
		if l, ok := m.current(); ok {
			m.setOpen(!l.open)
		}
	case "r":
		// A read or pull under way may have started before what the user
		// wants to see, so another follows it. Each builds when it ends.
		m.listingWorkers.ask()
		m.readingBranches.ask()
		m.readingGitHub.ask()
		m.pulling.ask()
		return m.startDue()
	default:
		for _, a := range actions {
			if key == a.key {
				return m.act(a)
			}
		}
	}
	return nil
}

// act starts an action on the task under the cursor or, when its line doesn't
// take the action, says why.
func (m *liveStatus) act(a action) tea.Cmd {
	l, _ := m.current()
	if why := a.refuses(m, l.row); why != "" {
		m.said = why
		return nil
	}
	return a.start(m, l.row.Task)
}

func (m *liveStatus) refusesOpenPR(r *taskRow) string {
	switch {
	case r == nil:
		return "put the cursor on a task to open its PR"
	case r.Task.PR() == "":
		return r.Task.ID() + " has no PR"
	}
	return ""
}

func (m *liveStatus) openPR(t Task) tea.Cmd {
	open := m.feed.openPR
	return func() tea.Msg { return prOpened{t, open(t.PR())} }
}

func (m *liveStatus) refusesNote(r *taskRow) string {
	if r == nil {
		return "put the cursor on a task to send its worker a note"
	}
	t := r.Task
	switch {
	case !t.Dispatched():
		return t.ID() + " has no worker: it hasn't been dispatched"
	case t.Status().Finished():
		return t.ID() + " is " + string(t.Status()) + ", so its worker is done"
	case m.readOnlyOn != "":
		return "this machine can only read: crew runs on " + m.readOnlyOn
	case m.held[t.Path].sending:
		return "your note to " + t.ID() + "'s worker is still on its way"
	case r.Worker.busy():
		// crew followup would refuse it too.
		return t.ID() + "'s worker is busy (" + r.Worker.stateText() + "), so it can't take a note now"
	}
	return ""
}

// startNote opens a line to type a note to t's worker in, with the note crew
// didn't send to it, if there's one.
func (m *liveStatus) startNote(t Task) tea.Cmd {
	input := textinput.New()
	input.Prompt = "note to " + t.ID() + ": "
	// No styles: the terminal's own colours, like the rest of the view, and a
	// cursor that doesn't blink (Cursor.Blink is false).
	input.SetStyles(textinput.Styles{})
	input.SetValue(m.held[t.Path].text)
	delete(m.held, t.Path)
	m.note = &noteDraft{to: t, input: input}
	m.note.input.SetWidth(m.noteWidth())
	return m.note.own(m.note.input.Focus())
}

// noteWidth is how much of the note shows after its prompt: the rest of the
// screen's width, less a column for the cursor.
func (m *liveStatus) noteWidth() int {
	return max(1, m.width-lipgloss.Width(m.note.input.Prompt)-1)
}

// typeNote takes a key, a paste, or what the note's own commands send back (a
// paste from the clipboard, say) while the user types a note. Every key goes
// into the note, the view's own keys included, but for enter, which sends it,
// esc, which drops it, and ctrl+c, which quits.
func (m *liveStatus) typeNote(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "ctrl+c":
			return tea.Quit
		case "esc":
			m.note = nil
			return nil
		case "enter":
			return m.sendNote()
		}
	}
	var cmd tea.Cmd
	m.note.input, cmd = m.note.input.Update(msg)
	return m.note.own(cmd)
}

// own has what cmd sends back come to this draft only.
func (d *noteDraft) own(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return noteInput{d, cmd()} }
}

// sendNote sends the note the user typed, unless it's empty.
func (m *liveStatus) sendNote() tea.Cmd {
	to, text, send := m.note.to, strings.TrimSpace(m.note.input.Value()), m.feed.sendNote
	m.note = nil
	if text == "" {
		return nil
	}
	m.held[to.Path] = heldNote{text: text, sending: true}
	m.said = "sending your note to " + to.ID() + "'s worker…"
	return func() tea.Msg {
		warning, err := send(to, text)
		return noteSent{to, text, warning, err}
	}
}

func (m *liveStatus) current() (shownLine, bool) {
	if m.cursor < 0 || m.cursor >= len(m.shown) {
		return shownLine{}, false
	}
	return m.shown[m.cursor], true
}

func (m *liveStatus) moveTo(i int) {
	m.cursor = max(0, min(i, len(m.shown)-1))
	m.scroll()
}

// setOpen opens or closes the line the cursor is on, if it has lines under it.
func (m *liveStatus) setOpen(open bool) {
	l, ok := m.current()
	if !ok || len(l.kids) == 0 {
		return
	}
	m.open[l.key] = open
	m.layOut()
	m.scroll()
}

// closeOrUp closes the line the cursor is on or, when it's closed already or
// has nothing under it, goes up to the line it's under.
func (m *liveStatus) closeOrUp() {
	l, ok := m.current()
	if !ok {
		return
	}
	if l.open {
		m.setOpen(false)
	} else if i := m.above(m.cursor); i >= 0 {
		m.moveTo(i)
	}
}

// above is where the line that the line at i is under is on show, or -1 for a
// line at the top.
func (m *liveStatus) above(i int) int {
	for j := i - 1; j >= 0; j-- {
		if m.shown[j].depth < m.shown[i].depth {
			return j
		}
	}
	return -1
}

// layOut works out which lines are on show: every one not under a closed one.
func (m *liveStatus) layOut() {
	m.shown = m.shown[:0]
	var add func(lines []statusLine, depth int)
	add = func(lines []statusLine, depth int) {
		for _, l := range lines {
			open := m.isOpen(l)
			m.shown = append(m.shown, shownLine{l, depth, open})
			if open {
				add(l.kids, depth+1)
			}
		}
	}
	add(m.lines, 0)
}

// isOpen is whether the lines under l are on show: as the user left it, or
// unless the plain text folds it. A line with nothing under it is never open.
func (m *liveStatus) isOpen(l statusLine) bool {
	open, ok := m.open[l.key]
	if !ok {
		open = !l.folded
	}
	return open && len(l.kids) > 0
}

// bodyHeight is how many lines of the report fit on screen, above the bar and
// the line above it, when there's one.
func (m *liveStatus) bodyHeight() int {
	if m.footLine() != "" {
		return max(1, m.height-2)
	}
	return max(1, m.height-1)
}

// footLine is the line above the bar: the note the user is typing or, when
// they aren't, what an action did; "" when there's neither.
func (m *liveStatus) footLine() string {
	if m.note != nil {
		return m.note.input.View()
	}
	return m.said
}

// scroll moves the screen as little as keeps the cursor on it.
func (m *liveStatus) scroll() {
	body := m.bodyHeight()
	m.top = min(m.top, m.cursor)
	m.top = max(m.top, m.cursor-body+1)
	m.top = max(0, min(m.top, len(m.shown)-body))
}

var cursorStyle = lipgloss.NewStyle().Reverse(true)

func (m *liveStatus) View() tea.View {
	var b strings.Builder
	for i := m.top; i < m.top+m.bodyHeight(); i++ {
		line := ""
		if i < len(m.shown) {
			line = fit(m.shown[i].text(), m.width)
			if i == m.cursor {
				line = cursorStyle.Render(line)
			}
		}
		b.WriteString(line + "\n")
	}
	if foot := m.footLine(); foot != "" {
		b.WriteString(fit(foot, m.width) + "\n")
	}
	b.WriteString(fit(m.bar(), m.width))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// text is the line as the live view shows it: indented by its depth, with a
// marker for whether it's open, closed, or has nothing under it. The marker
// takes two columns more than the text's lines, so the columns start two later.
func (l shownLine) text() string {
	marker := " "
	if len(l.kids) > 0 {
		marker = "▸"
		if l.open {
			marker = "▾"
		}
	}
	return l.statusLine.text(strings.Repeat("  ", l.depth)+marker+" ", idColumn+2)
}

// bar is the line at the bottom: how fresh the report is, and the keys, with
// the actions the line under the cursor takes.
func (m *liveStatus) bar() string {
	state := "loading…"
	if m.lines != nil {
		state = "updated " + m.builtAt.Format("15:04:05")
	}
	if m.readingGitHub.running {
		state += ", reading GitHub…"
	}
	if m.settingsErr != nil {
		state += ", on the settings crew started with: " + m.settingsErr.Error()
	}
	if m.note != nil {
		return state + "   enter send  esc cancel"
	}
	keys := "↑↓ move  ←→ fold"
	l, _ := m.current()
	for _, a := range actions {
		if a.refuses(m, l.row) == "" {
			keys += "  " + a.key + " " + a.name
		}
	}
	return state + "   " + keys + "  r refresh  q quit"
}

// fit cuts a line to the screen's width.
func fit(line string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}
