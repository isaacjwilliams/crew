package main

// crew status's live view: the lines the plain text prints (reportLines), in a
// terminal, built again every few seconds, with folds the user opens and closes.
// Task and goal lines are a table with its headings pinned at the top, or cards
// on a screen too narrow for it (status_table.go). Their notes are short there
// (status_wording.go), so a strip at the bottom words the cursor's line's notes
// in full.

import (
	"slices"
	"strings"
	"time"

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
// building a report from those reads. The live view runs each in the
// background, on its own, so one that hangs holds back none of the others.
type statusFeed struct {
	pullLedger   func()
	listWorkers  func() *workerList
	readBranches func() branchReads
	readPRs      func() prReads
	// build's error says the shared settings couldn't be read again, so the
	// report went by the ones crew started with.
	build func(statusReads) (statusReport, error)
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
	}
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
	// settingsErr is why the latest report went by the settings crew started
	// with: the shared settings couldn't be read again.
	settingsErr error
	// open is whether the user left a line open, by its key. A line the user
	// hasn't opened or closed is open unless the plain text folds it.
	open   map[string]bool
	shown  []shownLine // every line not under a closed one, in order
	cursor int         // the line it's on, in shown
	top    int         // how many screen lines of shown are above the screen
	// table is the report's columns fitted to the screen, and inColumns whether
	// they fit; when they don't, each task and goal is a card. headings are
	// the table's, pinned at the top of the screen: none for cards.
	table         table
	inColumns     bool
	headings      []string
	width, height int
}

// shownLine is a line on show, with how deep it is in the tree, and how the
// screen draws it: on more than one line when it wraps, or as a card.
type shownLine struct {
	statusLine
	depth int
	open  bool
	drawn []string
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
)

func newLiveStatus(feed statusFeed) *liveStatus {
	return &liveStatus{
		feed: feed, now: time.Now,
		building: job{every: buildEvery}, listingWorkers: job{every: listWorkersEvery},
		readingBranches: job{every: readBranchesEvery}, readingGitHub: job{every: readGitHubEvery}, pulling: job{every: pullLedgerEvery},
		open: map[string]bool{}, width: 80, height: 24,
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
// shows at once; the beat starts each job again when it's due.
func (m *liveStatus) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layOut()
		m.scroll()
	case beat:
		return m, m.startDue()
	case ledgerPulled:
		return m, m.readEnded(&m.pulling)
	case workersListed:
		m.reads.workers = msg.workers
		return m, m.readEnded(&m.listingWorkers)
	case branchesRead:
		m.reads.branches = msg.branches
		return m, m.readEnded(&m.readingBranches)
	case prsRead:
		m.reads.prs = msg.reads
		return m, m.readEnded(&m.readingGitHub)
	case reportBuilt:
		m.building.running = false
		m.settingsErr = msg.settingsErr
		m.show(msg.report, m.building.last)
		return m, m.startDue()
	case tea.KeyPressMsg:
		return m, m.press(msg.String())
	}
	return m, nil
}

// show puts a new report on screen, with the cursor on the line it was on, or
// as near to where it was as there's a line, when that one's gone.
func (m *liveStatus) show(r statusReport, at time.Time) {
	var key string
	if l, ok := m.current(); ok {
		key = l.key
	}
	m.lines, m.builtAt = reportLines(r), at
	m.layOut()
	i := slices.IndexFunc(m.shown, func(l shownLine) bool { return l.key == key })
	if i < 0 {
		i = m.cursor
	}
	m.moveTo(i)
}

func (m *liveStatus) press(key string) tea.Cmd {
	switch key {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		m.step(-1)
	case "down", "j":
		m.step(1)
	case "pgup":
		m.page(-1)
	case "pgdown":
		m.page(1)
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
	}
	return nil
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

// layOut works out which lines are on show, every one not under a closed one,
// and draws them for the screen.
func (m *liveStatus) layOut() {
	m.shown = m.shown[:0]
	var add func(lines []statusLine, depth int)
	add = func(lines []statusLine, depth int) {
		for _, l := range lines {
			open := m.isOpen(l)
			m.shown = append(m.shown, shownLine{statusLine: l, depth: depth, open: open})
			if open {
				add(l.kids, depth+1)
			}
		}
	}
	add(m.lines, 0)
	m.table, m.inColumns = measure(m.lines, indentWidth, true).fit(m.width)
	m.headings = nil
	if m.inColumns && slices.ContainsFunc(m.lines, statusLine.hasRows) {
		m.headings = m.table.row(strings.Repeat(" ", indentWidth(1)), headings())
	}
	for i, l := range m.shown {
		m.shown[i].drawn = m.draw(l)
	}
}

// draw is how the screen shows a line: a task or a goal as a row of the table
// or as a card, and any other line wrapped to the screen.
func (m *liveStatus) draw(l shownLine) []string {
	switch {
	case !l.hasFields():
		return hang(l.indent(), l.head, m.width)
	case m.inColumns:
		return m.table.row(l.indent(), l.statusLine)
	}
	return card(l.indent(), l.statusLine, m.width)
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

// bodyHeight is how many screen lines the report's lines get: the screen less
// the headings, the strip and the bar under it. It's at least 1, so a short
// screen still shows the cursor's line.
func (m *liveStatus) bodyHeight() int { return max(1, m.height-1-len(m.headings)-len(m.strip())) }

// strip is the cursor's line's notes as the plain text words them, wrapped to
// the screen, between the report's lines and the bar: one empty line for a
// line without notes. It leaves the report's lines at least one screen line,
// cutting its last line short with … when it has to.
func (m *liveStatus) strip() []string {
	room := m.height - 1 - len(m.headings) - 1 // the bar, and a line of the report
	if room <= 0 {
		return nil
	}
	notes := ""
	if m.cursor < len(m.shown) && m.shown[m.cursor].hasFields() {
		notes = m.shown[m.cursor].fields[notesField].text
	}
	lines := strings.Split(lipgloss.Wrap(notes, m.width, ""), "\n")
	if len(lines) > room {
		lines = lines[:room]
		lines[room-1] = cut(lines[room-1], m.width-1) + "…"
	}
	return lines
}

// drawnHeight is how many screen lines the shown lines from i to j take.
func (m *liveStatus) drawnHeight(i, j int) int {
	n := 0
	for _, l := range m.shown[i:j] {
		n += len(l.drawn)
	}
	return n
}

// cursorSpan is the screen lines the cursor's line takes, from start up to
// end, counted from the first shown line.
func (m *liveStatus) cursorSpan() (start, end int) {
	if m.cursor >= len(m.shown) {
		return 0, 0
	}
	start = m.drawnHeight(0, m.cursor)
	return start, start + len(m.shown[m.cursor].drawn)
}

// lineAt is the shown line that takes screen line n, counted from the first
// shown line.
func (m *liveStatus) lineAt(n int) int {
	for i, l := range m.shown {
		if n < len(l.drawn) {
			return i
		}
		n -= len(l.drawn)
	}
	return len(m.shown) - 1
}

// step moves the cursor one line up or down. On a line taller than the
// screen, it scrolls through that line first.
func (m *liveStatus) step(by int) {
	start, end := m.cursorSpan()
	switch {
	case by > 0 && end > m.top+m.bodyHeight():
		m.top++
	case by < 0 && start < m.top:
		m.top--
	default:
		m.moveTo(m.cursor + by)
		return
	}
	m.scroll()
}

// page moves the screen a screenful up or down, and the cursor to the line on
// the screen line just past the old screen's edge, so every line shows on one
// screen or the next. The new screen starts or ends at that edge, as tall as
// the strip of the cursor's new line leaves it, so the cursor moves first.
func (m *liveStatus) page(by int) {
	edge := m.top + m.bodyHeight()
	if by < 0 {
		edge = m.top - 1
	}
	m.cursor = max(0, m.lineAt(edge))
	if by < 0 {
		m.top = edge + 1 - m.bodyHeight()
	} else {
		m.top = edge
	}
	m.scroll()
}

// scroll moves the screen as little as keeps the cursor's line on it: all of
// it when it fits, and otherwise as much as fits, the screen not leaving it.
// The screen never leaves room empty at the bottom while lines are above it.
func (m *liveStatus) scroll() {
	body := m.bodyHeight()
	m.top = max(0, min(m.top, m.drawnHeight(0, len(m.shown))-body))
	// A cursor's line that fits stays all on screen: the screen starts between
	// end-body and start. One taller than the screen fills it: the screen
	// starts between start and end-body.
	start, end := m.cursorSpan()
	low, high := min(start, end-body), max(start, end-body)
	m.top = min(max(m.top, low), high)
}

// highlight shows the cursor's line reversed across the screen. A colored part
// of the line ends by resetting every style (lipgloss writes \x1b[m), which ends
// the reverse too, so it turns the reverse back on after each.
func highlight(line string, width int) string {
	const reset, reverse = "\x1b[m", "\x1b[7m"
	return lipgloss.NewStyle().Reverse(true).Width(width).Render(strings.ReplaceAll(line, reset, reset+reverse))
}

// View is the headings, the lines on show from the top, the strip and the bar.
// The cursor's line is highlighted across the screen, all of it when it wraps.
func (m *liveStatus) View() tea.View {
	body := m.bodyHeight()
	var screen []string
	for _, line := range m.headings {
		screen = append(screen, fit(line, m.width))
	}
	n := 0 // how many screen lines of shown come before this one
	for i, l := range m.shown {
		for _, line := range l.drawn {
			if n >= m.top && n < m.top+body {
				line = fit(line, m.width)
				if i == m.cursor {
					line = highlight(line, m.width)
				}
				screen = append(screen, line)
			}
			n++
		}
	}
	for len(screen) < len(m.headings)+body {
		screen = append(screen, "")
	}
	screen = append(screen, m.strip()...)
	v := tea.NewView(strings.Join(append(screen, fit(m.bar(), m.width)), "\n"))
	v.AltScreen = true
	return v
}

// indent is what goes before the line's head: two spaces for each level
// it's under, and a marker for whether it's open, closed, or has nothing under
// it.
func (l shownLine) indent() string {
	marker := " "
	if len(l.kids) > 0 {
		marker = "▸"
		if l.open {
			marker = "▾"
		}
	}
	return strings.Repeat("  ", l.depth) + marker + " "
}

// indentWidth is how wide a line's indent is at a depth: two spaces a level,
// then the fold marker and a space (shownLine.indent).
func indentWidth(depth int) int { return 2*depth + 2 }

// bar is the line at the bottom: how fresh the report is, and the keys.
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
	return state + "   ↑↓ move  ←→ fold  r refresh  q quit"
}

// fit cuts a line to the screen's width: the bar, or a line indented past the
// width of a very narrow screen.
func fit(line string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}
