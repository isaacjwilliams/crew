package main

// crew status's live view: the lines the plain text prints (reportLines), in a
// terminal, built again every few seconds, with folds the user opens and closes.

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// How often the live view builds its report, and how often it brings in what
// changed elsewhere: the PRs on GitHub and, on a machine that doesn't run the
// crew, the ledger.
const (
	buildEvery      = 3 * time.Second
	readGitHubEvery = 30 * time.Second
	pullLedgerEvery = time.Minute
)

// statusFeed builds crew status's reports. It reads GitHub and pulls the ledger
// only when they're due, and builds every report between from its last read of
// the PRs.
type statusFeed struct {
	c        *Crew
	scope    string // as crew status takes it: one project, or "" for all
	doneToo  bool
	now      func() time.Time
	prs      map[string]prRead
	readAt   time.Time // when GitHub was last read; zero before the first time
	pulledAt time.Time // when the ledger was last pulled; zero before the first time
}

func newStatusFeed(c *Crew, scope string, doneToo bool) *statusFeed {
	return &statusFeed{c: c, scope: scope, doneToo: doneToo, now: time.Now}
}

// report builds the next report. fresh reads GitHub and pulls the ledger
// whether they're due or not, as when the user asks for a refresh.
func (f *statusFeed) report(fresh bool) statusReport {
	now := f.now()
	if fresh || now.Sub(f.pulledAt) >= pullLedgerEvery {
		f.c.catchUpLedger()
		f.pulledAt = now
	}
	inScope := f.c.projects(f.scope)
	if fresh || now.Sub(f.readAt) >= readGitHubEvery {
		f.prs, f.readAt = f.c.readPRs(inScope), now
	}
	return buildReport(f.c, inScope, f.doneToo, f.prs)
}

// watchStatus runs the live view until the user quits.
func watchStatus(feed *statusFeed) error {
	_, err := tea.NewProgram(newLiveStatus(feed.report)).Run()
	return err
}

// liveStatus is the live view: the latest report's lines, which of them are on
// show, and the line the cursor is on.
type liveStatus struct {
	source  func(fresh bool) statusReport // builds a report; it may take a while
	lines   []statusLine                  // the latest report's; nil before the first
	builtAt time.Time
	// open is whether the user left a line open, by its key. A line the user
	// hasn't opened or closed is open unless the plain text folds it.
	open          map[string]bool
	shown         []shownLine // every line not under a closed one, in order
	cursor        int         // the line it's on, in shown
	top           int         // the first line of shown on the screen
	width, height int
	building      bool // a report is being built
	refreshing    bool // the user asked for a refresh that hasn't come in yet
}

// shownLine is a line on show, with how deep it is in the tree.
type shownLine struct {
	statusLine
	depth int
	open  bool
}

// reportBuilt brings a report built in the background: when it was, and
// whether it was a refresh, with GitHub read and the ledger pulled.
type reportBuilt struct {
	report statusReport
	at     time.Time
	fresh  bool
}

// buildDue says it's time to build the next report.
type buildDue struct{}

func newLiveStatus(source func(fresh bool) statusReport) *liveStatus {
	return &liveStatus{source: source, open: map[string]bool{}, width: 80, height: 24}
}

func (m *liveStatus) Init() tea.Cmd {
	return tea.Batch(m.build(false), nextBuild())
}

// build starts building a report, in the background.
func (m *liveStatus) build(fresh bool) tea.Cmd {
	m.building = true
	source := m.source
	return func() tea.Msg { return reportBuilt{source(fresh), time.Now(), fresh} }
}

func nextBuild() tea.Cmd {
	return tea.Tick(buildEvery, func(time.Time) tea.Msg { return buildDue{} })
}

func (m *liveStatus) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
	case buildDue:
		// A build still under way leaves this one out: they'd only queue up
		// behind a slow GitHub.
		if m.building {
			return m, nextBuild()
		}
		return m, tea.Batch(m.build(false), nextBuild())
	case reportBuilt:
		m.building = false
		m.refreshing = m.refreshing && !msg.fresh
		m.show(msg.report, msg.at)
		if m.refreshing {
			return m, m.build(true)
		}
	case tea.KeyPressMsg:
		return m, m.press(msg.String())
	}
	return m, nil
}

// show puts a new report on screen, with the cursor on the line it was on, or
// as near to where it was as there's a line, when that one's gone.
func (m *liveStatus) show(r statusReport, at time.Time) {
	var key string
	if m.cursor < len(m.shown) {
		key = m.shown[m.cursor].key
	}
	m.lines, m.builtAt = reportLines(r), at
	m.layOut()
	m.cursor = min(m.cursor, len(m.shown)-1)
	for i, l := range m.shown {
		if l.key == key {
			m.cursor = i
			break
		}
	}
	m.scroll()
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
		// A refresh under way, or asked for, answers this one too. A build
		// under way makes it wait.
		if m.refreshing {
			return nil
		}
		m.refreshing = true
		if !m.building {
			return m.build(true)
		}
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
		return
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.shown[i].depth < l.depth {
			m.moveTo(i)
			return
		}
	}
}

// layOut works out which lines are on show: every one not under a closed one.
func (m *liveStatus) layOut() {
	m.shown = m.shown[:0]
	var add func(lines []statusLine, depth int)
	add = func(lines []statusLine, depth int) {
		for _, l := range lines {
			open, ok := m.open[l.key]
			if !ok {
				open = !l.folded
			}
			open = open && len(l.kids) > 0
			m.shown = append(m.shown, shownLine{l, depth, open})
			if open {
				add(l.kids, depth+1)
			}
		}
	}
	add(m.lines, 0)
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
	var state string
	switch {
	case m.lines == nil:
		state = "loading…"
	case m.refreshing:
		state = "refreshing…"
	default:
		state = "updated " + m.builtAt.Format("15:04:05")
	}
	return state + "   ↑↓ move  ←→ fold  r refresh  q quit"
}

// fit cuts a line to the screen's width.
func fit(line string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}
