package main

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// liveOn is the live view showing r, as it is once its first report comes in.
func liveOn(r statusReport) *liveStatus {
	m := newLiveStatus(statusFeed{})
	m.Update(reportBuilt{report: r})
	return m
}

// keyCodes are the keys press takes by name.
var keyCodes = map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"enter": tea.KeyEnter, "end": tea.KeyEnd, "home": tea.KeyHome, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"esc": tea.KeyEscape}

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

// typeText types text into the view, a key for each character.
func typeText(m *liveStatus, text string) tea.Cmd {
	var cmd tea.Cmd
	for _, r := range text {
		_, cmd = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		if quits(cmd) {
			return cmd
		}
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

// openAll opens every line.
func openAll(m *liveStatus) {
	_, keys := treeOf(m.lines)
	for _, k := range keys {
		m.open[k] = true
	}
	m.layOut()
}

// screen is what the view shows, a line each, with the cursor's style left in.
func screen(m *liveStatus) []string {
	return strings.Split(m.View().Content, "\n")
}

// ansiCodes are the escapes that style the cursor's line.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// words is a line's words, without the tree's markers or the cursor's style.
func words(line string) string {
	line = ansiCodes.ReplaceAllString(line, "")
	return strings.Join(strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '▸' || r == '▾'
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

// tenOClock is when the tests' views open.
var tenOClock = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

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
	// Every line of the tree, the ones the text only counts too, with its
	// columns, under the line it's under in the tree.
	parents, _ := treeOf(reportLines(r))
	for i, l := range m.shown {
		if !strings.HasSuffix(view[i], words(l.tail)) {
			t.Errorf("%q isn't in the view with its columns %q", l.head, l.tail)
		}
		parent := ""
		if j := m.above(i); j >= 0 {
			parent = m.shown[j].head
		}
		if want := parents[l.head]; parent != want {
			t.Errorf("%q is under %q, want %q", l.head, parent, want)
		}
	}
	if len(m.shown) != len(parents) {
		t.Errorf("the view shows %d lines, want the tree's %d", len(m.shown), len(parents))
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
	var pulls, listings, branchesRead, reads atomic.Int32
	feed := feedOf(c, "p", false)
	build := feed.build
	feed.pullLedger = func() { pulls.Add(1); <-hang }
	feed.listWorkers = func() *workerList { listings.Add(1); <-hang; return nil }
	feed.readBranches = func() branchReads { branchesRead.Add(1); <-hang; return nil }
	feed.readPRs = func() prReads { reads.Add(1); <-hang; return nil }
	feed.build = func(r statusReads) (statusReport, error) { <-letBuild; return build(r) }
	clock := tenOClock
	m := newLiveStatus(feed)
	m.now = func() time.Time { return clock }
	msgs := make(chan tea.Msg, 8)

	inBackground(m.Init(), msgs)
	if got := m.bar(); !strings.HasPrefix(got, "loading…") {
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
	clock = clock.Add(buildEvery)
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
	clock = clock.Add(time.Hour)
	if _, later := m.Update(beat{}); refresh != nil || later != nil {
		t.Error("the view started a job while the same one was under way")
	}
	if pulls.Load() != 1 || listings.Load() != 1 || branchesRead.Load() != 1 || reads.Load() != 1 {
		t.Errorf("the view pulled the ledger %d times, listed the workers %d times, read the branches %d times and read GitHub %d times, want once each",
			pulls.Load(), listings.Load(), branchesRead.Load(), reads.Load())
	}
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit while a report is being built")
	}
	release(t, letBuild)
}

// TestLiveStatusKeepsBuildingWhileItsReadsHang changes a task while the view's
// first reads (GitHub, the workers and the branches) and its pull of the ledger
// hang, and sees it on screen within 5 seconds.
func TestLiveStatusKeepsBuildingWhileItsReadsHang(t *testing.T) {
	c := statusLedger(t)
	hang := make(chan struct{})
	defer close(hang)
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() { <-hang }
	feed.listWorkers = func() *workerList { <-hang; return nil }
	feed.readBranches = func() branchReads { <-hang; return nil }
	feed.readPRs = func() prReads { <-hang; return nil }
	clock := tenOClock
	m := newLiveStatus(feed)
	m.now = func() time.Time { return clock }
	msgs := make(chan tea.Msg, 8)
	inBackground(m.Init(), msgs)
	m.Update(next(t, msgs)) // the first report; the reads and the pull still hang

	reportTask(t, c, "p", "010-parent", false, "status: needs-input", "base: main", "branch: agent/parent")
	for range 5 {
		clock = clock.Add(time.Second)
		_, cmd := m.Update(beat{})
		if cmd != nil {
			inBackground(cmd, msgs)
			m.Update(next(t, msgs))
		}
	}
	if i := lineOf(m, "010-parent"); i < 0 || !strings.Contains(m.shown[i].tail, "needs-input") {
		t.Error("5 seconds after a task changed, while the reads and the pull hang, the view doesn't show it")
	}
}

func TestLiveStatusReadsTheSharedSettingsForEachReport(t *testing.T) {
	c := statusLedger(t)
	writeFile(t, c, "MAX_WORKERS=3\nGOAL_MAX_IN_FLIGHT=2\n", "config", "defaults.env")
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() {}
	feed.readPRs = func() prReads { return nil }
	m := newLiveStatus(feed)
	settle(m, m.Init())
	writeFile(t, c, "MAX_WORKERS=6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if l := m.shown[0].head; !strings.Contains(l, "1/6") {
		t.Errorf("after MAX_WORKERS went from 3 to 6 in crew.env, the view says %q", l)
	}
}

func TestLiveStatusSaysWhenItCantReadTheSharedSettings(t *testing.T) {
	c := statusLedger(t)
	writeFile(t, c, "MAX_WORKERS=3\nGOAL_MAX_IN_FLIGHT=2\n", "config", "defaults.env")
	feed := feedOf(c, "p", false)
	feed.pullLedger = func() {}
	feed.readPRs = func() prReads { return nil }
	m := newLiveStatus(feed)
	m.width = 200
	settle(m, m.Init())
	writeFile(t, c, "MAX_WORKERS = 6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if got := m.bar(); !strings.Contains(got, "crew.env:1") {
		t.Errorf("with a line crew.env can't parse, the bar says %q, want it to name crew.env:1", got)
	}
	writeFile(t, c, "MAX_WORKERS=6\n", "config", "crew.env")
	settle(m, press(m, "r"))
	if got := m.bar(); strings.Contains(got, "crew.env") {
		t.Errorf("once crew.env parses again, the bar still says %q", got)
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

// actionsLedger is statusLedger with tasks for each of the view's actions: one
// with a PR and a worker, one stacked on it with a worker, and a merged one with
// a PR.
func actionsLedger(t *testing.T) *Crew {
	t.Helper()
	c := statusLedger(t)
	reportTask(t, c, "p", "016-both", false, "status: pr-open", "pr: https://github.com/o/r/pull/16", "worktree: /wt/both", "base: main", "branch: agent/both")
	reportTask(t, c, "p", "017-stacked", false, "status: working", "worktree: /wt/stacked", "base: agent/both", "branch: agent/stacked")
	reportTask(t, c, "p", "018-merged", false, "status: merged", "pr: https://github.com/o/r/pull/18", "worktree: /wt/merged")
	return c
}

// actionsReport is the report crew status --done builds from actionsLedger, so
// the merged task is in it.
func actionsReport(c *Crew) statusReport {
	return buildReport(c, []string{"p"}, true, statusReads{prs: c.readPRs([]string{"p"}), workers: c.listWorkers(), branches: c.readBranches([]string{"p"})})
}

// actionsRun is what the view's actions were given to run, in place of gh and
// crew followup.
type actionsRun struct {
	opened   []string // the PRs opened, by URL
	notes    []string // the notes sent: "<task ID>: <note>"
	err      error    // what each action returns
	warnings string   // what crew warned of when it sent a note
}

// actingOn is the live view showing r, with its actions recorded in run rather
// than run.
func actingOn(r statusReport) (*liveStatus, *actionsRun) {
	m, run := liveOn(r), &actionsRun{}
	m.width, m.height = 200, 60
	openAll(m)
	m.feed.openPR = func(url string) error { run.opened = append(run.opened, url); return run.err }
	m.feed.sendNote = func(t Task, note string) (string, error) {
		run.notes = append(run.notes, t.ID()+": "+note)
		return run.warnings, run.err
	}
	return m, run
}

// finish runs the command an action started, and gives the view its message.
func finish(t *testing.T, m *liveStatus, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("the action started nothing")
	}
	m.Update(cmd())
}

// offered is what the bar offers to do to the line under the cursor.
func offered(m *liveStatus) string {
	_, keys, _ := strings.Cut(m.bar(), "←→ fold")
	keys, _, _ = strings.Cut(keys, "r refresh")
	return strings.TrimSpace(keys)
}

// cursorOnScreen is the words of the line in the cursor's style, looked for in
// the body above what the view says, on a screen as high as the terminal. It's
// "" when the cursor's line isn't there.
func cursorOnScreen(t *testing.T, m *liveStatus) string {
	t.Helper()
	lines := screen(m)
	if len(lines) != m.height {
		t.Fatalf("the view is %d lines high on a terminal %d lines high", len(lines), m.height)
	}
	for _, line := range lines[:len(lines)-1-len(m.footLines())] {
		if strings.HasPrefix(line, "\x1b[7m") {
			return words(line)
		}
	}
	return ""
}

// footOnScreen is the lines above the bar, as the screen shows them, without
// their style or the spaces after them.
func footOnScreen(m *liveStatus) string {
	lines := screen(m)
	foot := lines[len(lines)-1-len(m.footLines()) : len(lines)-1]
	for i, line := range foot {
		foot[i] = strings.TrimRight(ansiCodes.ReplaceAllString(line, ""), " ")
	}
	return strings.Join(foot, "\n")
}

func TestLiveStatusBarOffersTheActionsTheLineTakes(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	// Every kind of line the view shows, to check that each is on show.
	for _, head := range []string{"workers running: 1/3", "p", "goal 001-launch", "in flight", "next, in dispatch order",
		"parked", "1  002-next", "… (2 more)", "010-parent", "└ 011-child", "1 draft(s)  (crew list --project p --status draft)",
		"012-idea", "to review: #5 low, #12 low, #9 high (no security review yet), #16 no risk tier", elsewhereHeading} {
		if !slices.ContainsFunc(m.shown, func(l shownLine) bool { return l.head == head }) {
			t.Errorf("no line %q on show", head)
		}
	}
	takes := map[string]string{ // by the line's head; every other line takes neither
		"001-in-flight": "",                  // in a goal's group, with a busy worker and no PR
		"016-both":      "o open PR  n note", // a PR and a worker
		"└ 017-stacked": "n note",            // stacked, with a worker
		"013-risky":     "o open PR",         // a PR and no worker
		"018-merged":    "o open PR",         // finished, with a PR
		"014-later":     "o open PR",
		"015-earlier":   "o open PR",
	}
	for i, l := range m.shown {
		m.moveTo(i)
		if got := offered(m); got != takes[l.head] {
			t.Errorf("on %q, the bar offers %q, want %q", l.head, got, takes[l.head])
		}
		for _, a := range actions {
			if strings.Contains(takes[l.head], a.key+" ") {
				continue
			}
			before := screen(m)
			cmd := press(m, a.key)
			after := screen(m)
			// The line that says why takes the place of the report's last line.
			if cmd != nil || m.note != nil || len(run.opened)+len(run.notes) > 0 || !slices.Equal(before[:len(after)-2], after[:len(after)-2]) {
				t.Errorf("%s on %q, which doesn't take it, changed something", a.key, l.head)
			}
			if footOnScreen(m) != m.said || m.said == "" {
				t.Errorf("%s on %q doesn't say why in a line: %q", a.key, l.head, footOnScreen(m))
			}
		}
	}
	for _, tc := range []struct{ head, key, says string }{
		{"workers running: 1/3", "o", "put the cursor on a task to open its PR"},
		{"goal 001-launch", "n", "put the cursor on a task to send its worker a note"},
		{"001-in-flight", "o", "001-in-flight has no PR"},
		{"001-in-flight", "n", "001-in-flight's worker is busy (working), so it can't take a note now"},
		{"012-idea", "n", "012-idea has no worker: it hasn't been dispatched"},
		{"018-merged", "n", "018-merged is merged, so its worker is done"},
	} {
		goTo(t, m, tc.head)
		press(m, tc.key)
		if m.said != tc.says {
			t.Errorf("%s on %q says %q, want %q", tc.key, tc.head, m.said, tc.says)
		}
	}
}

func TestLiveStatusOnAMachineThatDoesntRunTheCrewOpensPRsButSendsNoNotes(t *testing.T) {
	r := actionsReport(actionsLedger(t))
	r.ReadOnlyOn = "laptop"
	m, run := actingOn(r)
	goTo(t, m, "read-only: crew runs on laptop")
	if got := offered(m); got != "" {
		t.Errorf("the read-only line offers %q", got)
	}
	goTo(t, m, "016-both")
	if got := offered(m); got != "o open PR" {
		t.Errorf("a task with a PR and a worker offers %q, want only to open its PR", got)
	}
	if press(m, "n") != nil || m.note != nil || m.said != "this machine can only read: crew runs on laptop" {
		t.Errorf("n on a machine that doesn't run the crew: note %v, says %q", m.note, m.said)
	}
	finish(t, m, press(m, "o"))
	if !slices.Equal(run.opened, []string{"https://github.com/o/r/pull/16"}) || len(run.notes) > 0 {
		t.Errorf("opened %v and sent %v, want only the PR opened", run.opened, run.notes)
	}
}

func TestLiveStatusOpensTheTasksPR(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "013-risky")
	finish(t, m, press(m, "o"))
	if !slices.Equal(run.opened, []string{"https://github.com/o/r/pull/9"}) {
		t.Errorf("opened %v, want 013-risky's PR", run.opened)
	}
	if got := footOnScreen(m); got != "opened 013-risky's PR in the browser" {
		t.Errorf("after opening the PR, the view says %q", got)
	}

	run.err = errors.New("no browser found")
	finish(t, m, press(m, "o"))
	if got := footOnScreen(m); got != "couldn't open 013-risky's PR: no browser found" {
		t.Errorf("when opening the PR fails, the view says %q", got)
	}
	if !strings.Contains(screen(m)[len(screen(m))-1], "o open PR") {
		t.Error("the message took the bar's place")
	}

	goTo(t, m, "001-in-flight")
	if press(m, "o") != nil || len(run.opened) != 2 {
		t.Errorf("o on a task with no PR ran something: opened %v", run.opened)
	}
}

func TestLiveStatusSendsANoteAsTyped(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "016-both")
	press(m, "n")
	if m.note == nil || !strings.HasPrefix(footOnScreen(m), "note to 016-both:") {
		t.Fatalf("n doesn't ask for a note: the line above the bar is %q", footOnScreen(m))
	}
	if got := offered(m); !strings.HasSuffix(m.bar(), "enter send  esc cancel") || got != "" {
		t.Errorf("while a note is typed, the bar is %q", m.bar())
	}
	cursor := m.cursor
	const note = "q r j k h l g G, then space: r refresh"
	if cmd := typeText(m, note); quits(cmd) {
		t.Fatal("typing a note quit the view")
	}
	if m.cursor != cursor || run.opened != nil || run.notes != nil {
		t.Errorf("typing a note moved the cursor or acted: at %d, want %d; opened %v", m.cursor, cursor, run.opened)
	}
	m.Update(tea.PasteMsg{Content: " (pasted)"})
	finish(t, m, press(m, "enter"))
	if want := "016-both: " + note + " (pasted)"; !slices.Equal(run.notes, []string{want}) {
		t.Errorf("sent %q, want %q", run.notes, want)
	}
	if m.note != nil || footOnScreen(m) != "sent your note to 016-both's worker" {
		t.Errorf("after sending, the view says %q", footOnScreen(m))
	}

	run.notes, run.warnings = nil, "couldn't resume session 1b2a; starting a fresh session instead\nledger commit failed (continuing)"
	press(m, "n")
	typeText(m, "and one more thing")
	finish(t, m, press(m, "enter"))
	if got, want := footOnScreen(m), "sent your note to 016-both's worker, but:\n"+run.warnings; got != want {
		t.Errorf("when crew warns as it sends a note, the view says %q, want %q", got, want)
	}

	run.notes = nil
	press(m, "n")
	typeText(m, "never mind")
	if press(m, "esc") != nil || m.note != nil {
		t.Error("esc leaves the note open")
	}
	press(m, "n")
	typeText(m, "   ")
	if press(m, "enter") != nil || m.note != nil {
		t.Error("enter on a blank note leaves it open, or sends it")
	}
	if run.notes != nil {
		t.Errorf("esc or a blank note sent %q", run.notes)
	}
}

func TestLiveStatusKeepsANoteCrewDidntSend(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "016-both")
	run.err = errors.New("p is paused; 'crew resume --project p' lets its work go on")
	press(m, "n")
	typeText(m, "the tests first")
	finish(t, m, press(m, "enter"))
	if got, want := footOnScreen(m), "crew followup failed; n on 016-both brings your note back:\n"+run.err.Error(); got != want {
		t.Errorf("when crew refuses a note, the view says %q, want %q", got, want)
	}

	goTo(t, m, "└ 017-stacked")
	press(m, "n")
	if got := m.note.input.Value(); got != "" {
		t.Errorf("a note to another task starts with %q", got)
	}
	press(m, "esc")
	goTo(t, m, "016-both")
	press(m, "n")
	typeText(m, ", please")
	run.err, run.notes = nil, nil
	finish(t, m, press(m, "enter"))
	if want := []string{"016-both: the tests first, please"}; !slices.Equal(run.notes, want) {
		t.Errorf("sent %q, want %q", run.notes, want)
	}
	press(m, "n")
	if got := m.note.input.Value(); got != "" {
		t.Errorf("once the note is sent, n starts with %q", got)
	}
}

func TestLiveStatusTakesNoNoteForATaskWhileOneIsOnItsWay(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "016-both")
	run.err = errors.New("p is paused; 'crew resume --project p' lets its work go on")
	press(m, "n")
	typeText(m, "the tests first")
	sending := press(m, "enter")

	if got := offered(m); got != "o open PR" {
		t.Errorf("while a note to 016-both is on its way, the bar offers %q", got)
	}
	press(m, "n")
	if m.note != nil {
		t.Fatal("n started a second note to 016-both while one was on its way")
	}
	if got, want := footOnScreen(m), "your note to 016-both's worker is still on its way"; got != want {
		t.Errorf("n says %q, want %q", got, want)
	}

	finish(t, m, sending)
	press(m, "n")
	if got := m.note.input.Value(); got != "the tests first" {
		t.Errorf("once crew refuses the note, n starts with %q", got)
	}
}

// TestLiveStatusGivesTheNoteWhatItsCommandsSendBack presses ctrl+v, which has
// the note read the clipboard. With no clipboard tool on PATH, the read fails,
// and the note has the error.
func TestLiveStatusGivesTheNoteWhatItsCommandsSendBack(t *testing.T) {
	m, _ := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "016-both")
	press(m, "n")
	t.Setenv("PATH", t.TempDir())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	finish(t, m, cmd)
	if m.note == nil || m.note.input.Err == nil {
		t.Error("the clipboard's answer to ctrl+v didn't reach the note")
	}
}

// TestLiveStatusGivesANoteOnlyWhatItsOwnCommandsSendBack drops the note that
// pressed ctrl+v and starts another before the clipboard answers.
func TestLiveStatusGivesANoteOnlyWhatItsOwnCommandsSendBack(t *testing.T) {
	m, _ := actingOn(actionsReport(actionsLedger(t)))
	goTo(t, m, "016-both")
	press(m, "n")
	t.Setenv("PATH", t.TempDir())
	_, paste := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	press(m, "esc")
	goTo(t, m, "└ 017-stacked")
	press(m, "n")
	finish(t, m, paste)
	if m.note.input.Err != nil {
		t.Error("the clipboard's answer to a note that's gone reached the next one")
	}
}

func TestLiveStatusSendsANoteToTheTaskItWasStartedOn(t *testing.T) {
	c := actionsLedger(t)
	m, run := actingOn(actionsReport(c))
	goTo(t, m, "└ 017-stacked")
	press(m, "n")
	typeText(m, "for the stacked task")
	finish(t, m, press(m, "enter"))

	goTo(t, m, "016-both")
	press(m, "n")
	typeText(m, "for the PR's ")
	// 016-both is stacked on 010-parent while the note is typed, so its line
	// moves, and then merges, so its line goes and the cursor goes to the line
	// that took its place.
	reportTask(t, c, "p", "016-both", false, "status: pr-open", "pr: https://github.com/o/r/pull/16", "worktree: /wt/both", "base: agent/parent", "branch: agent/both")
	m.Update(reportBuilt{report: actionsReport(c)})
	if m.shown[m.cursor].head != "└ 016-both" {
		t.Fatalf("016-both didn't move under 010-parent: the cursor is on %q", m.shown[m.cursor].head)
	}
	typeText(m, "own ")
	reportTask(t, c, "p", "016-both", false, "status: merged", "pr: https://github.com/o/r/pull/16", "worktree: /wt/both")
	m.Update(reportBuilt{report: readReport(c, "p")})
	if l := m.shown[m.cursor]; l.row == nil || l.row.Task.ID() == "016-both" {
		t.Fatalf("the cursor is on %q, want another task's line", l.head)
	}
	typeText(m, "worker")
	finish(t, m, press(m, "enter"))

	want := []string{"017-stacked: for the stacked task", "016-both: for the PR's own worker"}
	if !slices.Equal(run.notes, want) {
		t.Errorf("sent %q, want %q", run.notes, want)
	}
}

func TestFollowupOutcomeTellsCrewsWarningsFromClaudesNotes(t *testing.T) {
	note := "Note: session 7f3c started in the background\n"
	slow := "crew: job w9 hasn't reported its session after 30s, so crew can't confirm it started\n"
	resumed := "crew: couldn't resume session 1b2a; starting a fresh session instead\n"
	claudeFailed := "Error: not logged in\n"
	for _, tc := range []struct {
		failed                bool
		stderr, warnings, err string
	}{
		{false, "", "", ""},
		{false, note, "", ""},
		{false, note + slow, "job w9 hasn't reported its session after 30s, so crew can't confirm it started", ""},
		{false, resumed + note + slow, "couldn't resume session 1b2a; starting a fresh session instead\njob w9 hasn't reported its session after 30s, so crew can't confirm it started", ""},
		// A refusal shows whole, with the output of claude's it points to.
		{true, resumed + claudeFailed + "crew: could not start a session (see output above)\n", "",
			"crew: couldn't resume session 1b2a; starting a fresh session instead\nError: not logged in\ncrew: could not start a session (see output above)"},
		{true, "crew: claude started a session but crew can't read its job ID from this output; find it in 'claude agents' and record it with 'crew set':\nbackgrounded job ???\n", "",
			"crew: claude started a session but crew can't read its job ID from this output; find it in 'claude agents' and record it with 'crew set':\nbackgrounded job ???"},
		{true, "", "", "crew followup failed"},
	} {
		warnings, err := followupOutcome(tc.failed, tc.stderr)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if warnings != tc.warnings || got != tc.err {
			t.Errorf("followupOutcome(%v, %q) = %q, %v; want %q, %q", tc.failed, tc.stderr, warnings, err, tc.warnings, tc.err)
		}
	}
}

func TestLiveStatusShowsAllThatCrewSaidOfANoteAndKeepsTheCursorOnScreen(t *testing.T) {
	m, run := actingOn(actionsReport(actionsLedger(t)))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	goTo(t, m, "016-both")
	resumed := "couldn't resume session 1b2a7c4e-90f1-4d2b-8e3a-5c6d7e8f9a0b; starting a fresh session instead"
	run.warnings = resumed + "\nledger commit failed (continuing)"
	press(m, "n")
	typeText(m, "the tests first")
	finish(t, m, press(m, "enter"))
	// The first warning is wider than the screen, so it takes two lines.
	if got, want := strings.Join(strings.Fields(footOnScreen(m)), " "), "sent your note to 016-both's worker, but: "+resumed+" ledger commit failed (continuing)"; got != want {
		t.Errorf("at 80 columns, the view says %q, want %q", got, want)
	}
	if !strings.Contains(cursorOnScreen(t, m), "016-both") {
		t.Errorf("the cursor's line isn't on screen above what crew said: %q", screen(m))
	}

	// A refusal longer than half the screen keeps its first line and its last,
	// and says how many it leaves out.
	run.warnings, run.err = "", errors.New(strings.Repeat("claude's output\n", 20)+"crew: could not start a session (see output above)")
	press(m, "n")
	typeText(m, "and again")
	finish(t, m, press(m, "enter"))
	foot := strings.Split(footOnScreen(m), "\n")
	if len(foot) != 6 || foot[0] != "crew followup failed; n on 016-both brings your note back:" || foot[1] != "… 17 lines not shown" || foot[2] != "claude's output" || foot[5] != "crew: could not start a session (see output above)" {
		t.Errorf("on a screen 12 lines high, a long refusal shows as %q", foot)
	}
	if !strings.Contains(cursorOnScreen(t, m), "016-both") {
		t.Errorf("the cursor's line isn't on screen above a long refusal: %q", screen(m))
	}

	// On a screen 4 lines high, it leaves room for the cursor's line and the bar.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
	if !strings.Contains(cursorOnScreen(t, m), "016-both") {
		t.Errorf("on a screen 4 lines high, the cursor's line isn't on screen above a long refusal: %q", screen(m))
	}
}

// TestFollowupCommandRunsInASessionOfItsOwn starts the note's command on a crew
// that says its arguments and waits for its input to close.
func TestFollowupCommandRunsInASessionOfItsOwn(t *testing.T) {
	exe := t.TempDir() + "/crew"
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"$*\" >&2\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := followupCommand(exe, Task{Doc{Path: "/l/tasks/016-both.md"}}, "the tests first")
	var said strings.Builder
	cmd.Stderr = &said
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Go's syscall has no Getsid on Linux.
	session, _, errno := syscall.Syscall(syscall.SYS_GETSID, uintptr(cmd.Process.Pid), 0, 0)
	input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the note's command failed: %v: %s", err, said.String())
	}
	if errno != 0 || int(session) != cmd.Process.Pid {
		t.Errorf("the note's crew followup runs in session %d (%v), not one of its own", session, errno)
	}
	if got, want := strings.TrimSpace(said.String()), "followup /l/tasks/016-both.md --note the tests first --from-user"; got != want {
		t.Errorf("the note's command runs crew %q, want %q", got, want)
	}
}

// TestBrowsePRKeepsGhOffTheScreen runs browsePR on a gh that prints, starts a
// "browser" that keeps gh's output open, and fails when told to.
func TestBrowsePRKeepsGhOffTheScreen(t *testing.T) {
	bin := t.TempDir()
	gh := `#!/bin/sh
echo "$@" >"$(dirname "$0")/args"
echo "Opening github.com/o/r/pull/9 in your browser."
if [ -e "$(dirname "$0")/fail" ]; then echo "some noise" >&2; echo "no pull requests found" >&2; exit 1; fi
sleep 30 &
`
	if err := os.WriteFile(bin+"/gh", []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	if err := browsePR("https://github.com/o/r/pull/9"); err != nil {
		t.Errorf("browsePR: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("browsePR waited %v on the browser gh started", took)
	}
	if args, _ := os.ReadFile(bin + "/args"); string(args) != "pr view https://github.com/o/r/pull/9 --web\n" {
		t.Errorf("gh ran with %q", args)
	}

	if err := os.WriteFile(bin+"/fail", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := browsePR("https://github.com/o/r/pull/9"); err == nil || err.Error() != "no pull requests found" {
		t.Errorf("when gh fails, browsePR says %v, want gh's last line", err)
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

	clock := tenOClock
	start := clock
	m := newLiveStatus(feedOf(c, "p", false))
	m.now = func() time.Time { return clock }
	var reads, pulls []time.Duration
	var lastSync []bool // for each second, whether 013-risky's merge check went by the last sync
	calls := 0
	settle(m, m.Init())
	for {
		since := clock.Sub(start)
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
		clock = clock.Add(time.Second)
		switch clock.Sub(start) {
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

// liveCrew is statusLedger with p's clone a real repo: a stack in flight whose
// parent tips are pushed, an open PR stacked on it, a PR that targets a branch
// other than its task's base, and a PR that can merge now. Stubs for claude, gh
// and git log every call to calls. claude lists the workers in workers, and
// fails while claudeFails exists.
type liveCrew struct {
	*Crew
	repo                        string
	calls, workers, claudeFails string
}

func newLiveCrew(t *testing.T) liveCrew {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	c := statusLedger(t)
	outside := t.TempDir() // the stubs' files, kept out of the home
	l := liveCrew{Crew: c, repo: stackRepo(t, "agent/lower", "agent/upper", "agent/top", "agent/moved", "agent/old", "agent/ready"),
		calls: outside + "/calls.log", workers: outside + "/workers.json", claudeFails: outside + "/claude-fails"}
	writeFile(t, c, "REPO="+l.repo+"\n", "projects", "p", "project.env")
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"idle","pid":3}`)
	writeFile(t, c, `#!/bin/sh
echo "claude $*" >>`+l.calls+`
[ -e `+l.claudeFails+` ] && exit 1
cat `+l.workers+`
`, "bin", "claude")
	writeFile(t, c, `#!/bin/sh
echo "gh $*" >>`+l.calls+`
echo '{"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}'
`, "bin", "gh")
	writeFile(t, c, `#!/bin/sh
echo "git $*" >>`+l.calls+`
exec `+realGit+` "$@"
`, "bin", "git")
	reportTask(t, c, "p", "020-lower", false, "status: working", "base: main", "branch: agent/lower", "worker_job: w20")
	reportTask(t, c, "p", "021-upper", false, "status: working", "base: agent/lower", "branch: agent/upper")
	reportTask(t, c, "p", "022-top", false, "status: pr-open", "base: agent/upper", "branch: agent/top",
		"pr: https://github.com/o/r/pull/22", "pr_state: open", "pr_base: agent/upper")
	reportTask(t, c, "p", "023-moved", false, "status: working", "base: main", "branch: agent/moved",
		"pr: https://github.com/o/r/pull/23", "pr_state: open", "pr_base: agent/old")
	reportTask(t, c, "p", "024-ready", false, "status: pr-open", "risk: low", "base: main", "branch: agent/ready",
		"pr: https://github.com/o/r/pull/24", "pr_state: open", "pr_base: main", "worker_job: w24")
	return l
}

// listWorkers has claude agents list the workers given, as JSON objects.
func (l liveCrew) listWorkers(t *testing.T, workers string) {
	t.Helper()
	if err := os.WriteFile(l.workers, []byte("["+workers+"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moveOn puts a new commit on a branch's tip on origin, as its worker would by
// pushing, and returns the tip it had.
func (l liveCrew) moveOn(t *testing.T, branch string) string {
	t.Helper()
	g := gitRepo{t, l.repo}
	was := g.git("rev-parse", "refs/remotes/origin/"+branch)
	g.git("update-ref", "refs/remotes/origin/"+branch, g.git("commit-tree", was+"^{tree}", "-p", was, "-m", "moved on"))
	return was
}

// callsSince is the calls the stubs logged after the first n.
func (l liveCrew) callsSince(n int) []string {
	b, _ := os.ReadFile(l.calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	return lines[min(n, len(lines)):]
}

// The calls a second of the live view made, counted by what they read.
type liveCalls struct{ builds, listings, branchCalls, ghCalls, pulls int }

// countCalls sorts calls by what they read: the workers, p's branches, GitHub,
// or the ledger.
func (l liveCrew) countCalls(calls []string) liveCalls {
	var n liveCalls
	for _, call := range calls {
		switch {
		case strings.HasPrefix(call, "claude agents "):
			n.listings++
		case strings.HasPrefix(call, "git -C "+l.repo+" "):
			n.branchCalls++
		case strings.HasPrefix(call, "gh "):
			n.ghCalls++
		case strings.HasPrefix(call, "git ") && strings.Contains(call, " pull "):
			n.pulls++
		}
	}
	return n
}

// secondsWhen is the seconds, from the start, in which a holds.
func secondsWhen(seconds []liveCalls, holds func(liveCalls) bool) []time.Duration {
	var at []time.Duration
	for i, n := range seconds {
		if holds(n) {
			at = append(at, time.Duration(i)*time.Second)
		}
	}
	return at
}

// For a minute, with a task file changing every few seconds, the live view
// lists the workers and reads the tasks' branches every 15 seconds, however
// often it builds. GitHub is read every 30 seconds and the ledger pulled every
// minute.
func TestLiveStatusListsWorkersAndReadsBranchesWhenDue(t *testing.T) {
	l := newLiveCrew(t)
	writeFile(t, l.Crew, "laptop\n", "host") // another machine runs the crew, so the ledger is pulled
	g := gitRepo{t, l.Home}
	g.git("init", "-q")
	g.git("add", "-A")
	g.git("commit", "-qm", "home")
	logged := len(l.callsSince(0))
	l.readBranches([]string{"p"})
	oneRead := l.countCalls(l.callsSince(logged)).branchCalls

	feed := feedOf(l.Crew, "p", false)
	var builds atomic.Int32
	build := feed.build
	feed.build = func(r statusReads) (statusReport, error) { builds.Add(1); return build(r) }
	logged = len(l.callsSince(0))
	var seconds []liveCalls
	run := newLiveRun(feed)
	for s := 0; ; s++ {
		calls := l.callsSince(logged)
		logged += len(calls)
		n := l.countCalls(calls)
		n.builds = int(builds.Swap(0))
		seconds = append(seconds, n)
		if s == 60 {
			break
		}
		if s%4 == 3 { // the ledger changes under the view
			reportTask(t, l.Crew, "p", "012-idea", s%8 == 3, "status: draft", "base: main")
		}
		run.second()
	}

	built := secondsWhen(seconds, func(n liveCalls) bool { return n.builds > 0 })
	listed := secondsWhen(seconds, func(n liveCalls) bool { return n.listings > 0 })
	branchesRead := secondsWhen(seconds, func(n liveCalls) bool { return n.branchCalls > 0 })
	gitHubRead := secondsWhen(seconds, func(n liveCalls) bool { return n.ghCalls > 0 })
	pulled := secondsWhen(seconds, func(n liveCalls) bool { return n.pulls > 0 })
	t.Logf("built in %d seconds; listed the workers at %v; read the branches at %v, %d git calls a read; read GitHub at %v; pulled at %v",
		len(built), listed, branchesRead, oneRead, gitHubRead, pulled)

	s := time.Second
	every15s := []time.Duration{0, 15 * s, 30 * s, 45 * s, 60 * s}
	if len(built) < 20 {
		t.Errorf("the view built its report in %d seconds of 61, want every 3s at least", len(built))
	}
	for i, n := range seconds {
		if n.listings > 1 || n.branchCalls > oneRead {
			t.Errorf("at %ds, the view listed the workers %d times and ran git %d times in the clone, want once and one read's %d at most", i, n.listings, n.branchCalls, oneRead)
		}
	}
	if !slices.Equal(listed, every15s) {
		t.Errorf("the workers were listed at %v, want every 15s", listed)
	}
	if !slices.Equal(branchesRead, every15s) {
		t.Errorf("the branches were read at %v, want every 15s", branchesRead)
	}
	if !slices.Equal(gitHubRead, []time.Duration{0, 30 * s, 60 * s}) {
		t.Errorf("GitHub was read at %v, want every 30s", gitHubRead)
	}
	if !slices.Equal(pulled, []time.Duration{0, 60 * s}) {
		t.Errorf("the ledger was pulled at %v, want every minute", pulled)
	}
}

// liveRun is a live view run second by second, as its beat runs it, with each
// command it gives run to the end before the next second.
type liveRun struct {
	m     *liveStatus
	clock time.Time
}

// newLiveRun opens a view on feed at tenOClock.
func newLiveRun(feed statusFeed) *liveRun {
	r := &liveRun{m: newLiveStatus(feed), clock: tenOClock}
	r.m.now = func() time.Time { return r.clock }
	settle(r.m, r.m.Init())
	return r
}

// second moves the clock on a second and lets the view do what's due.
func (r *liveRun) second() {
	r.clock = r.clock.Add(time.Second)
	_, cmd := r.m.Update(beat{})
	settle(r.m, cmd)
}

// open is how long the view has been open.
func (r *liveRun) open() time.Duration { return r.clock.Sub(tenOClock) }

// tail is what the view shows after a task's ID: its status, worker, PR and
// merge check or notes.
func (r *liveRun) tail(id string) string {
	if i := lineOf(r.m, id); i >= 0 {
		return r.m.shown[i].tail
	}
	return ""
}

// A change to a task file shows with the next build, within 3 seconds, and
// the view lists no workers and reads no branches to show it.
func TestLiveStatusShowsALedgerChangeWithoutReadingAgain(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for range 5 {
		run.second()
	}
	logged := len(l.callsSince(0))
	reportTask(t, l.Crew, "p", "020-lower", false, "status: needs-input", "base: main", "branch: agent/lower", "worker_job: w20")
	reportTask(t, l.Crew, "p", "024-ready", true, "status: pr-open", "risk: low", "base: main", "branch: agent/ready",
		"pr: https://github.com/o/r/pull/24", "pr_state: open", "pr_base: main", "worker_job: w24")
	changed := run.open()
	for !strings.HasPrefix(run.tail("020-lower"), "needs-input") || !strings.Contains(run.tail("024-ready"), "1 open question(s)") {
		if run.open()-changed == 3*time.Second {
			t.Fatalf("3s after the ledger changed, the view shows 020-lower %q and 024-ready %q", run.tail("020-lower"), run.tail("024-ready"))
		}
		run.second()
	}
	if calls := l.callsSince(logged); len(calls) > 0 {
		t.Errorf("to show the change, the view ran:\n%s", strings.Join(calls, "\n"))
	}
}

// A worker's change of state, and a restack becoming needed, show within 15
// seconds. After r, they show as soon as the reads it starts come back.
func TestLiveStatusShowsWorkersAndRestacksWithin15Seconds(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for range 5 {
		run.second()
	}
	// 024-ready's worker starts on a round, and 020-lower's branch moves on
	// under 021-upper.
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"working","pid":3}`)
	lowerWas := l.moveOn(t, "agent/lower")
	for range 15 {
		run.second()
	}
	if got := run.tail("024-ready"); !strings.Contains(got, "worker: working") || !strings.Contains(got, "not yet: worker busy") {
		t.Errorf("15s after its worker started, 024-ready shows %q", got)
	}
	if got := run.tail("021-upper"); !strings.Contains(got, "needs restack: 020-lower moved") {
		t.Errorf("15s after 020-lower moved on, 021-upper shows %q", got)
	}

	// Both go back; r shows it at once.
	l.listWorkers(t, `{"id":"w1","state":"working","pid":1},{"id":"w20","state":"working","pid":2},{"id":"w24","state":"idle","pid":3}`)
	gitRepo{t, l.repo}.git("update-ref", "refs/remotes/origin/agent/lower", lowerWas)
	settle(run.m, press(run.m, "r"))
	if got := run.tail("024-ready"); !strings.Contains(got, "worker: idle") || !strings.Contains(got, "merge now") {
		t.Errorf("after r, with its worker idle, 024-ready shows %q", got)
	}
	if got := run.tail("021-upper"); strings.Contains(got, "needs restack") {
		t.Errorf("after r, with 020-lower back where it was, 021-upper shows %q", got)
	}
}

// Until the workers are first listed and the branches first read, the view
// claims nothing they could contradict.
func TestLiveStatusClaimsNothingBeforeTheFirstReads(t *testing.T) {
	l := newLiveCrew(t)
	hold := make(chan struct{})
	feed := feedOf(l.Crew, "p", false)
	listWorkers, readBranches := feed.listWorkers, feed.readBranches
	feed.listWorkers = func() *workerList { <-hold; return listWorkers() }
	feed.readBranches = func() branchReads { <-hold; return readBranches() }
	m := newLiveStatus(feed)
	m.now = func() time.Time { return tenOClock }
	msgs := make(chan tea.Msg, 8)
	inBackground(m.Init(), msgs)
	handle := func() {
		_, cmd := m.Update(next(t, msgs))
		inBackground(cmd, msgs)
	}
	// Until a report built from GitHub's answer is on show.
	for m.reads.prs == nil || m.building.running || m.building.asked || len(m.shown) == 0 {
		handle()
	}

	openAll(m)
	for _, line := range m.shown {
		if strings.Contains(line.tail, "merge now") {
			t.Errorf("before the first listing, %s says %q", line.head, line.tail)
		}
	}
	if got := m.shown[0].head; got != "workers running: not listed yet" {
		t.Errorf("before the first listing, the workers line is %q", got)
	}
	for _, id := range []string{"001-in-flight", "020-lower", "024-ready"} {
		if got := lineOf(m, id); got < 0 || !strings.Contains(m.shown[got].tail, "worker: not listed yet") {
			t.Errorf("before the first listing, %s's worker isn't said to be not listed yet", id)
		}
	}
	for _, id := range []string{"021-upper", "023-moved"} {
		if got := lineOf(m, id); got < 0 || !strings.Contains(m.shown[got].tail, "branches not read yet") {
			t.Errorf("before the first branch read, %s doesn't say its branches aren't read yet", id)
		}
	}
	// git is never asked about 020-lower: it isn't stacked, and has no PR.
	if got := lineOf(m, "020-lower"); got < 0 || strings.Contains(m.shown[got].tail, "branches not read yet") {
		t.Errorf("before the first branch read, 020-lower is shown as %q", m.shown[got].tail)
	}

	close(hold) // the reads come back, and with them what they say
	tail := func(id string) string { return m.shown[lineOf(m, id)].tail }
	for !strings.Contains(tail("024-ready"), "merge now") || !strings.Contains(tail("023-moved"), "PR targets agent/old, not main") {
		handle()
	}
}

// When listing the workers starts failing, the build after says so, and no
// row says merge now until a listing succeeds again.
func TestLiveStatusSaysWhenTheWorkersCouldntBeListed(t *testing.T) {
	l := newLiveCrew(t)
	run := newLiveRun(feedOf(l.Crew, "p", false))
	for run.open() < 60*time.Second {
		run.second()
		switch run.open() {
		case 20 * time.Second: // claude agents fails from here, until 40s
			if err := os.WriteFile(l.claudeFails, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		case 40 * time.Second:
			if err := os.Remove(l.claudeFails); err != nil {
				t.Fatal(err)
			}
		}
		// The listings at 30s fails; the one at 45s succeeds.
		failed := run.open() >= 30*time.Second && run.open() < 45*time.Second
		got := run.tail("024-ready")
		if failed && (!strings.Contains(got, "workers couldn't be listed") || strings.Contains(got, "merge now")) {
			t.Errorf("at %v, after a listing failed, 024-ready shows %q", run.open(), got)
		}
		if !failed && !strings.Contains(got, "merge now") {
			t.Errorf("at %v, after a listing succeeded, 024-ready shows %q", run.open(), got)
		}
	}
}
