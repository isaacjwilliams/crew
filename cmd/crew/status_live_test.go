package main

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// liveOn is the live view showing r, as it is once its first report comes in.
func liveOn(r statusReport) *liveStatus {
	m := newLiveStatus(nil)
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

// parentOf is the head of the line the line ending with text is under.
func parentOf(m *liveStatus, text string) string {
	i := lineOf(m, text)
	if i < 0 {
		return ""
	}
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

// ansiCodes are the escapes that style the cursor's line.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// words is a line's words, without the tree's markers or the cursor's style.
func words(line string) string {
	line = ansiCodes.ReplaceAllString(line, "")
	return strings.Join(strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '▸' || r == '▾' || r == '└'
	}), " ")
}

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
	for _, l := range strings.Split(plainText(r), "\n") {
		if l != "" && !slices.Contains(view, words(l)) {
			t.Errorf("the text's line %q isn't in the view:\n%s", l, strings.Join(screen(m), "\n"))
		}
	}
	// Every task the text lists or only counts, with its columns.
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
	for text, under := range map[string]string{
		"001-in-flight":           "in flight",
		"in flight":               "goal 001-launch",
		"007-next":                "… (2 more)",
		"… (2 more)":              "next, in dispatch order",
		"next, in dispatch order": "goal 001-launch",
		"009-parked":              "parked",
		"goal 001-launch":         "p",
		"011-child":               "010-parent",
		"012-idea":                "1 draft(s)  (crew list --project p --status draft)",
		"1 draft(s)  (crew list --project p --status draft)":                          "p",
		"q: 1 task(s) with open questions, 1 PR(s) to review, 1 memory suggestion(s)": elsewhereHeading,
	} {
		if got := parentOf(m, text); !strings.HasSuffix(got, under) {
			t.Errorf("%q is under %q, want %q", text, got, under)
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
	if buildEvery > 5*time.Second {
		t.Errorf("the view builds a report every %s, want at least every 5s", buildEvery)
	}
	c := statusLedger(t)
	m := liveOn(readReport(c, "p"))
	m.Update(buildDue{})
	if !m.building {
		t.Fatal("the view doesn't build a report when one is due")
	}
	m.Update(reportBuilt{report: readReport(c, "p")})

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

// blockingSource builds reports from r, each only once the test lets it.
type blockingSource struct {
	r     statusReport
	go_   chan struct{}
	fresh chan bool
}

func (s *blockingSource) report(fresh bool) statusReport {
	s.fresh <- fresh
	<-s.go_
	return s.r
}

// building runs a command the view gave in the background, as bubbletea does,
// and returns where its report will come.
func building(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 1)
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			msg = batch[0]() // the build; the second is the timer for the next
		}
		out <- msg
	}()
	return out
}

// startsABuild is whether a command the view gave on buildDue starts a build
// with its timer for the next. The timer alone takes buildEvery to answer; the
// two together answer at once, as a batch.
func startsABuild(cmd tea.Cmd) bool {
	msg := make(chan tea.Msg, 1)
	go func() { msg <- cmd() }()
	select {
	case m := <-msg:
		_, batch := m.(tea.BatchMsg)
		return batch
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestLiveStatusStaysUsableWhileAReportHangs(t *testing.T) {
	s := &blockingSource{r: readReport(statusLedger(t), "p"), go_: make(chan struct{}), fresh: make(chan bool, 4)}
	defer close(s.go_)
	m := newLiveStatus(s.report)

	first := building(m.Init())
	<-s.fresh
	if bar := screen(m)[m.bodyHeight()]; !strings.HasPrefix(bar, "loading…") {
		t.Errorf("before its first report, the view says %q, want loading…", bar)
	}
	press(m, "j", "down", "l", "enter")
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit before the first report")
	}

	s.go_ <- struct{}{}
	m.Update(<-first)
	if lineOf(m, "010-parent") < 0 {
		t.Fatal("the first report isn't on show")
	}
	next := building(m.build(false))
	<-s.fresh
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
	if _, cmd := m.Update(buildDue{}); startsABuild(cmd) {
		t.Error("a build fell due while one was under way, and the view started a second")
	}
	if cmd := press(m, "r"); cmd != nil || !m.refreshing {
		t.Error("r starts a second build alongside the one under way, or isn't kept for when it's done")
	}
	if !quits(press(m, "q")) {
		t.Error("q doesn't quit while a report is being built")
	}

	s.go_ <- struct{}{}
	_, cmd := m.Update(<-next)
	if cmd == nil || !m.refreshing {
		t.Fatal("the refresh asked for during a build doesn't follow it")
	}
	refresh := building(cmd)
	if !<-s.fresh {
		t.Error("the refresh asked for doesn't read GitHub and pull the ledger")
	}
	s.go_ <- struct{}{}
	m.Update(<-refresh)
	if m.refreshing {
		t.Error("the view still says it's refreshing once the refresh is in")
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

// TestStatusFeedReadsGitHubAndPullsTheLedgerWhenDue watches a feed left open on
// a machine that doesn't run the crew, building a report every buildEvery, with
// a refresh asked for along the way.
func TestStatusFeedReadsGitHubAndPullsTheLedgerWhenDue(t *testing.T) {
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

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	start := now
	feed := newStatusFeed(c, "p", false)
	feed.now = func() time.Time { return now }
	var reads, pulls []time.Duration
	var notes []string
	calls := 0
	for now.Sub(start) <= 2*time.Minute {
		fresh := now.Sub(start) == 99*time.Second
		if now.Sub(start) == 3*time.Second {
			if err := os.WriteFile(ghFails, nil, 0o644); err != nil { // GitHub stops answering, from here on
				t.Fatal(err)
			}
		}
		r := feed.report(fresh)
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for _, l := range lines[calls:] {
			switch {
			case strings.HasPrefix(l, "gh pr view https://github.com/o/r/pull/9 "):
				reads = append(reads, now.Sub(start))
			case strings.HasPrefix(l, "git ") && strings.Contains(l, " pull "):
				pulls = append(pulls, now.Sub(start))
			}
		}
		calls = len(lines)
		for _, row := range r.Projects[0].Rows {
			if row.Task.ID() == "013-risky" {
				notes = append(notes, row.Merge.String())
			}
		}
		now = now.Add(buildEvery)
	}
	s := time.Second
	if want := []time.Duration{0, 30 * s, 60 * s, 90 * s, 99 * s}; !slices.Equal(reads, want) {
		t.Errorf("GitHub was read at %v, want %v", reads, want)
	}
	if want := []time.Duration{0, 60 * s, 99 * s}; !slices.Equal(pulls, want) {
		t.Errorf("the ledger was pulled at %v, want %v", pulls, want)
	}
	for i, note := range notes {
		failed := time.Duration(i)*buildEvery >= 30*s
		if strings.Contains(note, "as of the last sync") != failed {
			t.Errorf("at %s the merge check says %q: as of the last sync only once a read has failed", time.Duration(i)*buildEvery, note)
		}
	}
	if after := homeState(t, c); after != before {
		t.Errorf("the feed changed the home:\nbefore %s\nafter  %s", before, after)
	}
}

// homeState is what git says of the home: what's changed in it, and its HEAD.
func homeState(t *testing.T, c *Crew) string {
	t.Helper()
	return gitOut(t, c.Home, "status", "--porcelain") + " " + gitOut(t, c.Home, "rev-parse", "HEAD")
}
