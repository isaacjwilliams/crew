package main

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// liveOn is the live view showing r, as it is once its first report comes in.
func liveOn(r statusReport) *liveStatus {
	m := newLiveStatus(statusFeed{})
	m.Update(reportBuilt{report: r, at: time.Now()})
	return m
}

// keyCodes are the keys press takes by name.
var keyCodes = map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"enter": tea.KeyEnter, "end": tea.KeyEnd, "home": tea.KeyHome, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown}

// press sends keys to the view, as a person types them, and returns the last
// key's command.
func press(m *liveStatus, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		key := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		if code, ok := keyCodes[k]; ok {
			key = tea.KeyPressMsg{Code: code}
		}
		_, cmd = m.Update(key)
	}
	return cmd
}

// lineOf is where the line that ends with text is on show, or -1.
func lineOf(m *liveStatus, text string) int {
	return slices.IndexFunc(m.shown, func(l shownLine) bool { return strings.HasSuffix(l.head, text) })
}

// goTo puts the cursor on the line that ends with text.
func goTo(t *testing.T, m *liveStatus, text string) {
	t.Helper()
	i := lineOf(m, text)
	if i < 0 {
		t.Fatalf("no line %q on show", text)
	}
	m.moveTo(i)
}

// parentOf is the head of the line the line at i is under, or "" at the top.
func parentOf(m *liveStatus, i int) string {
	for j := i - 1; j >= 0; j-- {
		if m.shown[j].depth < m.shown[i].depth {
			return m.shown[j].head
		}
	}
	return ""
}

// openAll opens every line that has lines under it.
func openAll(m *liveStatus) {
	var open func(lines []statusLine)
	open = func(lines []statusLine) {
		for _, l := range lines {
			if len(l.kids) > 0 {
				m.open[l.key] = true
				open(l.kids)
			}
		}
	}
	open(m.lines)
	m.layOut()
}

// screen is what the view shows, a line each, with the cursor's style left in.
func screen(m *liveStatus) []string {
	return strings.Split(m.View().Content, "\n")
}

// bar is the line at the bottom of the screen.
func bar(m *liveStatus) string {
	s := screen(m)
	return s[len(s)-1]
}

// ansiCodes are the escapes that style the cursor's line.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// words is a line's words, without the tree's markers or the cursor's style.
func words(line string) string {
	line = ansiCodes.ReplaceAllString(line, "")
	return strings.Join(strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '▸' || r == '▾' || r == '└'
	}), " ")
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// settle runs the commands the view gives one at a time, and hands the view
// their messages, until it gives no more.
func settle(m *liveStatus, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cmd, queue = queue[0], queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		_, then := m.Update(msg)
		queue = append(queue, then)
	}
}

// inBackground runs the commands the view gives each on its own, as bubbletea
// does, and sends their messages to msgs.
func inBackground(cmd tea.Cmd, msgs chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				inBackground(c, msgs)
			}
			return
		}
		msgs <- msg
	}()
}

// next is the next message a command in the background sends.
func next(t *testing.T, msgs <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-msgs:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no command the view gave has finished")
		return nil
	}
}

// release lets one of the feed's builds finish.
func release(t *testing.T, letBuild chan<- struct{}) {
	t.Helper()
	select {
	case letBuild <- struct{}{}:
	case <-time.After(10 * time.Second):
		t.Fatal("the view isn't building a report")
	}
}

// fakeClock is the time as a test has it, from 10:00.
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func TestLiveStatusShowsWhatTheTextShows(t *testing.T) {
	r := readReport(fullStatusLedger(t), "p")
	r.ReadOnlyOn = "laptop"
	m := liveOn(r)
	m.width, m.height = 400, 200
	openAll(m)
	var view []string
	for _, l := range screen(m) {
		view = append(view, words(l))
	}
	// Every line the text prints, with its columns: the header, each project's
	// and goal's line, every task with its status, worker, PR, merge check and
	// notes, the counts and what waits in the other projects.
	for _, l := range strings.Split(plainText(r), "\n") {
		if l != "" && !slices.Contains(view, words(l)) {
			t.Errorf("the text's line %q isn't in the view:\n%s", l, strings.Join(screen(m), "\n"))
		}
	}
	// Every line of the tree, the ones the text only counts too, under the line
	// it's under in the tree.
	parents, _ := treeOf(reportLines(r))
	for i, l := range m.shown {
		if got, want := parentOf(m, i), parents[l.head]; got != want {
			t.Errorf("%q is under %q, want %q", l.head, got, want)
		}
	}
	if len(m.shown) != len(parents) {
		t.Errorf("the view shows %d lines, want the tree's %d", len(m.shown), len(parents))
	}
	// Every task, the ones the text only counts too, with its columns.
	var rows []taskRow
	var each func(rs []taskRow)
	each = func(rs []taskRow) {
		for _, row := range rs {
			rows = append(rows, row)
			each(row.Kids)
		}
	}
	for _, p := range r.Projects {
		for _, g := range p.Goals {
			for _, group := range g.Groups {
				each(group.Rows)
			}
		}
		each(p.Rows)
	}
	for _, row := range rows {
		if i := lineOf(m, row.Task.ID()); i < 0 || !strings.HasSuffix(view[i], words(rowColumns(row))) {
			t.Errorf("%s isn't in the view with its columns %q", row.Task.ID(), rowColumns(row))
		}
	}
}

func TestLiveStatusFoldsGoalsStacksAndWhatTheTextCounts(t *testing.T) {
	m := liveOn(readReport(statusLedger(t), "p"))
	for _, folded := range []string{"007-next", "008-next", "012-idea"} {
		if lineOf(m, folded) >= 0 {
			t.Errorf("%s is on show before its fold is opened", folded)
		}
	}
	goTo(t, m, "… (2 more)")
	press(m, "right")
	goTo(t, m, "1 draft(s)  (crew list --project p --status draft)")
	press(m, "enter")
	for _, folded := range []string{"007-next", "008-next", "012-idea"} {
		if lineOf(m, folded) < 0 {
			t.Errorf("%s isn't on show once its fold is opened", folded)
		}
	}

	goTo(t, m, "goal 001-launch")
	press(m, "left")
	if lineOf(m, "001-in-flight") >= 0 || lineOf(m, "009-parked") >= 0 {
		t.Error("a closed goal still shows its tasks")
	}
	press(m, "right")
	if lineOf(m, "001-in-flight") < 0 || lineOf(m, "007-next") < 0 {
		t.Error("an opened goal doesn't show its tasks as they were")
	}

	goTo(t, m, "010-parent")
	press(m, "h")
	if lineOf(m, "011-child") >= 0 {
		t.Error("a closed stack still shows the task stacked on it")
	}
	press(m, "h")
	if got := m.shown[m.cursor].head; got != "p" {
		t.Errorf("left on a closed line goes to %q, want the line it's under", got)
	}
	goTo(t, m, "010-parent")
	press(m, "space")
	if lineOf(m, "011-child") < 0 {
		t.Error("an opened stack doesn't show the task stacked on it")
	}
}

func TestLiveStatusKeepsItsPlaceAcrossReports(t *testing.T) {
	c := statusLedger(t)
	m := liveOn(readReport(c, "p"))
	goTo(t, m, "goal 001-launch")
	press(m, "left")
	goTo(t, m, "014-later")
	before := m.cursor
	reportTask(t, c, "p", "010-also", false, "status: working", "base: main")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if got := m.shown[m.cursor].head; got != "014-later" || m.cursor != before+1 {
		t.Errorf("after a task came in above it, the cursor is on %q at %d, want 014-later at %d", got, m.cursor, before+1)
	}
	if lineOf(m, "001-in-flight") >= 0 {
		t.Error("a closed goal opened again with a new report")
	}

	at := m.cursor
	reportTask(t, c, "p", "014-later", false, "status: merged")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if m.cursor != at || m.shown[m.cursor].head != "015-earlier" {
		t.Errorf("once its task was gone, the cursor went to %q at %d, want the next line, at %d", m.shown[m.cursor].head, m.cursor, at)
	}
}

// TestLiveStatusStaysUsableWhileItsFeedHangs runs the view on a feed whose
// every part hangs until the test lets a build finish.
func TestLiveStatusStaysUsableWhileItsFeedHangs(t *testing.T) {
	c := statusLedger(t)
	hang, letBuild := make(chan struct{}), make(chan struct{})
	defer close(hang)
	var pulls, reads atomic.Int32
	feed := feedOf(c, "p", false)
	build := feed.build
	feed.pullLedger = func() { pulls.Add(1); <-hang }
	feed.readPRs = func() prReads { reads.Add(1); <-hang; return nil }
	feed.build = func(r prReads) statusReport { <-letBuild; return build(r) }
	clock := newFakeClock()
	m := newLiveStatus(feed)
	m.now = clock.Now
	msgs := make(chan tea.Msg, 8)

	inBackground(m.Init(), msgs)
	if got := bar(m); !strings.HasPrefix(got, "loading…") {
		t.Errorf("before its first report, the view says %q, want loading…", got)
	}
	press(m, "j", "down", "l", "enter", "h")
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit before the first report")
	}

	release(t, letBuild)
	m.Update(next(t, msgs))
	if lineOf(m, "010-parent") < 0 {
		t.Fatal("the first report isn't on show")
	}
	clock.now = clock.now.Add(buildEvery)
	_, cmd := m.Update(beat{})
	if !m.building.running {
		t.Fatal("the view doesn't build a report when one is due")
	}
	inBackground(cmd, msgs)
	goTo(t, m, "goal 001-launch")
	press(m, "j")
	if got := m.shown[m.cursor].head; got != "in flight" {
		t.Errorf("down went to %q while a report was being built", got)
	}
	press(m, "h", "h")
	if lineOf(m, "001-in-flight") >= 0 || m.shown[m.cursor].head != "goal 001-launch" {
		t.Error("the goal didn't close while a report was being built")
	}
	if !slices.ContainsFunc(screen(m), func(l string) bool { return strings.Contains(l, "010-parent") }) {
		t.Error("the view doesn't show the last report while the next is built")
	}
	// A refresh, and a beat long after every job fell due again, start none of
	// them a second time while the first is under way.
	refresh := press(m, "r")
	clock.now = clock.now.Add(time.Hour)
	if _, later := m.Update(beat{}); refresh != nil || later != nil {
		t.Error("the view started a job while the same one was under way")
	}
	if pulls.Load() != 1 || reads.Load() != 1 {
		t.Errorf("the view pulled the ledger %d times and read GitHub %d times, want once each", pulls.Load(), reads.Load())
	}
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit while a report is being built")
	}
	release(t, letBuild)
}

// TestLiveStatusKeepsBuildingWhileGitHubAndTheLedgerHang changes a task while
// the view's first read of GitHub and pull of the ledger hang, and sees it on
// screen within 5 seconds.
func TestLiveStatusKeepsBuildingWhileGitHubAndTheLedgerHang(t *testing.T) {
	c := statusLedger(t)
	hang := make(chan struct{})
	defer close(hang)
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() { <-hang }
	feed.readPRs = func() prReads { <-hang; return nil }
	clock := newFakeClock()
	m := newLiveStatus(feed)
	m.now = clock.Now
	msgs := make(chan tea.Msg, 8)
	inBackground(m.Init(), msgs)
	m.Update(next(t, msgs)) // the first report; the read and the pull still hang

	reportTask(t, c, "p", "010-parent", false, "status: needs-input", "base: main", "branch: agent/parent")
	for range 5 {
		clock.now = clock.now.Add(time.Second)
		_, cmd := m.Update(beat{})
		if cmd != nil {
			inBackground(cmd, msgs)
			m.Update(next(t, msgs))
		}
	}
	if i := lineOf(m, "010-parent"); i < 0 || !strings.Contains(m.shown[i].tail, "needs-input") {
		t.Error("5 seconds after a task changed, while GitHub and the ledger hang, the view doesn't show it")
	}
}

func TestLiveStatusFitsTheScreen(t *testing.T) {
	m := liveOn(readReport(fullStatusLedger(t), "p"))
	openAll(m)
	for _, width := range []int{60, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		for _, l := range screen(m) {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("at width %d, a line is %d wide: %q", width, w, l)
			}
		}
	}
	for _, keys := range [][]string{{"end"}, {"pgup"}, {"home"}, {"pgdown"}, {"G", "k", "k"}} {
		press(m, keys...)
		on := screen(m)[m.cursor-m.top]
		if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() || !strings.Contains(on, m.shown[m.cursor].head) {
			t.Errorf("after %v, the cursor's line %q isn't on screen (lines %d to %d)", keys, m.shown[m.cursor].head, m.top, m.top+m.bodyHeight()-1)
		}
	}
}

// TestLiveStatusReadsGitHubAndPullsTheLedgerWhenDue leaves the view open for two
// minutes on a machine that doesn't run the crew, with a refresh asked for
// along the way, and GitHub failing from 3 seconds on.
func TestLiveStatusReadsGitHubAndPullsTheLedgerWhenDue(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	c := statusLedger(t)
	writeFile(t, c, "laptop\n", "host") // another machine runs the crew
	writeFile(t, c, "REPO="+t.TempDir()+"\n", "projects", "p", "project.env")
	outside := t.TempDir() // the stubs' notes, kept out of the home
	log, ghFails := outside+"/calls.log", outside+"/gh-fails"
	writeFile(t, c, `#!/bin/sh
echo "gh $*" >>`+log+`
[ -e `+ghFails+` ] && exit 1
echo '{"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}'
`, "bin", "gh")
	writeFile(t, c, `#!/bin/sh
case " $* " in *" pull "*) echo "git $*" >>`+log+`;; esac
exec `+realGit+` "$@"
`, "bin", "git")
	g := gitRepo{t, c.Home}
	g.git("init", "-q")
	g.git("add", "-A")
	g.git("commit", "-qm", "home")
	before := homeState(t, c)

	clock := newFakeClock()
	start := clock.now
	m := newLiveStatus(feedOf(c, "p", false))
	m.now = clock.Now
	var reads, pulls []time.Duration
	var lastSync []bool // for each second, whether 013-risky's merge check went by the last sync
	calls := 0
	settle(m, m.Init())
	for {
		since := clock.now.Sub(start)
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for _, l := range lines[calls:] {
			switch {
			case strings.HasPrefix(l, "gh pr view https://github.com/o/r/pull/9 "):
				reads = append(reads, since)
			case strings.HasPrefix(l, "git ") && strings.Contains(l, " pull "):
				pulls = append(pulls, since)
			}
		}
		calls = len(lines)
		i := lineOf(m, "013-risky")
		lastSync = append(lastSync, i >= 0 && strings.Contains(m.shown[i].tail, "as of the last sync"))

		if since == 2*time.Minute {
			break
		}
		clock.now = clock.now.Add(time.Second)
		switch clock.now.Sub(start) {
		case 3 * time.Second: // GitHub stops answering, from here on
			if err := os.WriteFile(ghFails, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		case 99 * time.Second:
			settle(m, press(m, "r"))
		}
		_, cmd := m.Update(beat{})
		settle(m, cmd)
	}
	s := time.Second
	if want := []time.Duration{0, 30 * s, 60 * s, 90 * s, 99 * s}; !slices.Equal(reads, want) {
		t.Errorf("GitHub was read at %v, want %v", reads, want)
	}
	if want := []time.Duration{0, 60 * s, 99 * s}; !slices.Equal(pulls, want) {
		t.Errorf("the ledger was pulled at %v, want %v", pulls, want)
	}
	for second, went := range lastSync {
		if failed := second >= 30; went != failed {
			t.Errorf("at %ds, 013-risky's merge check went by the last sync: %v, want %v (GitHub's latest read failed: %v)", second, went, failed, failed)
		}
	}
	if after := homeState(t, c); after != before {
		t.Errorf("the view changed the home:\nbefore %s\nafter  %s", before, after)
	}
}

// homeState is what git says of the home: what's changed in it, and its HEAD.
func homeState(t *testing.T, c *Crew) string {
	t.Helper()
	return gitOut(t, c.Home, "status", "--porcelain") + " " + gitOut(t, c.Home, "rev-parse", "HEAD")
}
