package main

// crew status's live view: the lines the plain text prints (reportLines), in a
// terminal, built again every few seconds, with folds the user opens and closes.

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
// reads it's built from. It runs once at a time, at most every so
// often, unless it's asked for sooner.
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
	open          map[string]bool
	shown         []shownLine // every line not under a closed one, in order
	cursor        int         // the line it's on, in shown
	top           int         // the first line of shown on the screen
	width, height int
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

// bodyHeight is how many lines of the report fit on screen, above the bar.
func (m *liveStatus) bodyHeight() int { return max(1, m.height-1) }

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

// fit cuts a line to the screen's width.
func fit(line string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}
