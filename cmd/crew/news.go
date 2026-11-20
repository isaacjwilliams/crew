package main

// News: what crew did, for a lead to hear with the user's next message, without
// running crew tick or being asked. It lives in the home's news/, which is this
// machine's own, like logs/:
//
//	news/<day>.log      a line for each thing crew tick did, and for each status
//	                    a worker or goal lead set
//	news/held-back      what the last tick held back, and why
//	news/sessions/<id>  what a lead's session has been told
//
// Each file has a line per item, its fields separated by tabs: the day files
// start with the time, then the project, then the text; the others start with
// the project. A session's file has the time of the newest line it's had on
// its first line.
//
// The lead's hooks (crew lead --hook) tell each session what's new since its
// previous prompt: the lines written since, and what's held back that it hasn't
// been told yet.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// newsKept is how long news stays: crew tick removes the day files older than
// that, and the files of sessions that haven't been prompted since.
const newsKept = 30 * 24 * time.Hour

const newsDay = "2006-01-02"

// newsItem is one line of news. Its text starts with the task or goal it's
// about, and project is "" when it's about every project.
type newsItem struct {
	at      time.Time
	project string
	text    string
}

// recordNews adds a line to today's news. News is never worth failing a
// command for, so a line that can't be written is only warned of.
func (c *Crew) recordNews(project, text string) {
	now := time.Now()
	f, err := openLog(c.path("news", now.Format(newsDay)+".log"))
	if err == nil {
		_, err = fmt.Fprintf(f, "%s\t%s\t%s\n", now.Format(time.RFC3339Nano), project, oneLine(text))
		f.Close()
	}
	if err != nil {
		warn("couldn't record news: %v", err)
	}
}

// newsSince is the news written after since, oldest first.
func (c *Crew) newsSince(since time.Time) []newsItem {
	var items []newsItem
	files, _ := filepath.Glob(c.path("news", "*.log"))
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), ".log") < since.Format(newsDay) {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, line := range splitLines(string(b)) {
			at, rest, _ := strings.Cut(line, "\t")
			project, text, _ := strings.Cut(rest, "\t")
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil && t.After(since) {
				items = append(items, newsItem{t, project, text})
			}
		}
	}
	return items
}

// tickNews is what one crew tick did and held back. It prints each line, as
// tick always has, and keeps it for the leads: what it did at once, what it
// held back when the tick is over (save).
type tickNews struct {
	c    *Crew
	held []newsItem
}

func (n *tickNews) did(project, format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Println(line)
	n.c.recordNews(project, line)
}

func (n *tickNews) heldBack(project, format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Println(line)
	n.held = append(n.held, newsItem{project: project, text: line})
}

// save replaces news/held-back with what this tick held back.
func (n *tickNews) save() {
	var b strings.Builder
	for _, h := range n.held {
		fmt.Fprintf(&b, "%s\t%s\n", h.project, h.text)
	}
	if err := replaceNewsFile(n.c.path("news", "held-back"), b.String()); err != nil {
		warn("couldn't record what this tick held back: %v", err)
	}
}

// replaceNewsFile writes a news file in one step, so a hook reading it
// meanwhile reads the old one or the new one, never half of one.
func replaceNewsFile(path, text string) error {
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path+".new", []byte(text), 0o644)
	}
	if err == nil {
		err = os.Rename(path+".new", path)
	}
	return err
}

// pruneNews removes the day files older than newsKept, and the files of the
// sessions that haven't been prompted since.
func (c *Crew) pruneNews() {
	old := time.Now().Add(-newsKept)
	days, _ := filepath.Glob(c.path("news", "*.log"))
	for _, d := range days {
		if strings.TrimSuffix(filepath.Base(d), ".log") < old.Format(newsDay) {
			os.Remove(d)
		}
	}
	sessions, _ := filepath.Glob(c.path("news", "sessions", "*"))
	for _, s := range sessions {
		if fi, err := os.Stat(s); err == nil && fi.ModTime().Before(old) {
			os.Remove(s)
		}
	}
}

// heldBack is what the last tick held back.
func (c *Crew) heldBack() []newsItem {
	var items []newsItem
	b, _ := os.ReadFile(c.path("news", "held-back"))
	for _, line := range splitLines(string(b)) {
		if project, text, ok := strings.Cut(line, "\t"); ok {
			items = append(items, newsItem{project: project, text: text})
		}
	}
	return items
}

// sessionNews is what a lead's session has been told: the time of the newest
// line of news it has had, and the held-back lines it has been told, which it
// isn't told again while they're held back for the same reason. Its file has
// the time on its first line, then one held-back line per line.
type sessionNews struct {
	path  string
	since time.Time
	told  map[string]bool // project, a tab, then the line
}

// readSessionNews is the session's record, and whether it has one yet.
func (c *Crew) readSessionNews(id string) (s sessionNews, ok bool) {
	s = sessionNews{path: c.path("news", "sessions", id), told: map[string]bool{}}
	b, _ := os.ReadFile(s.path)
	lines := splitLines(string(b))
	if len(lines) == 0 {
		return s, false
	}
	var err error
	if s.since, err = time.Parse(time.RFC3339Nano, lines[0]); err != nil {
		return s, false
	}
	for _, l := range lines[1:] {
		s.told[l] = true
	}
	return s, true
}

func (s sessionNews) save() {
	b := s.since.Format(time.RFC3339Nano) + "\n"
	for l := range s.told {
		b += l + "\n"
	}
	replaceNewsFile(s.path, b)
}

// leadNews is what a lead's session is told with a prompt: the news since its
// previous prompt, and what's held back that it hasn't been told, for the
// projects in scope ("" for every project). A session's first prompt, or its
// start (starting), only begins its record: it hears nothing from before. What
// doesn't fit in room characters is left out, oldest first, and the message
// says how much and where to read it.
func (c *Crew) leadNews(id, scope string, starting bool, room int) string {
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return "" // not a session ID that could name its record
	}
	s, ok := c.readSessionNews(id)
	if starting && ok {
		return ""
	}
	inScope := func(i newsItem) bool { return scope == "" || i.project == "" || i.project == scope }
	held := map[string]bool{}
	var newlyHeld []newsItem
	for _, h := range c.heldBack() {
		if key := h.project + "\t" + h.text; inScope(h) {
			held[key] = true
			if !s.told[key] {
				newlyHeld = append(newlyHeld, h)
			}
		}
	}
	if !ok {
		s.since, s.told = time.Now(), held
		s.save()
		return ""
	}
	// since moves to the newest line read, not to now, so a line written while
	// this runs is heard with the next prompt. It takes the newest, not the
	// last: two commands can append their lines a little out of order.
	var did []newsItem
	for _, i := range c.newsSince(s.since) {
		if i.at.After(s.since) {
			s.since = i.at
		}
		if inScope(i) {
			did = append(did, i)
		}
	}
	s.told = held
	s.save()
	return c.newsMessage(did, newlyHeld, room)
}

// newsMessage lists what crew did, then what it's holding back. When that's
// more than room characters, the oldest lines are left out, and a note at the
// top says how many and where to read them.
func (c *Crew) newsMessage(did, held []newsItem, room int) string {
	const didHeader, heldHeader = "What crew did since the user's previous message:\n", "What crew tick is holding back, and why:\n"
	var lines []string
	for _, i := range did {
		lines = append(lines, "- "+i.at.Format(logTime)+" "+i.text+"\n")
	}
	for _, h := range held {
		lines = append(lines, "- "+h.text+"\n")
	}
	if len(lines) == 0 {
		return ""
	}
	note := func(n int) string {
		return fmt.Sprintf("%d earlier lines left out: this machine's news is in %s, a file a day, and what crew tick holds back is in %s.\n",
			n, c.path("news"), c.path("news", "held-back"))
	}
	// Leave out lines from the front until the rest, the headers and the note
	// fit. lines has did's lines first, so left cuts into did before held.
	size, left := len(didHeader)+len(heldHeader), 0
	for _, l := range lines {
		size += utf8.RuneCountInString(l)
	}
	if size > room {
		size += utf8.RuneCountInString(note(len(lines)))
		for size > room && left < len(lines) {
			size -= utf8.RuneCountInString(lines[left])
			left++
		}
	}
	var b strings.Builder
	if left > 0 {
		b.WriteString(note(left))
	}
	if left < len(did) {
		b.WriteString(didHeader + strings.Join(lines[left:len(did)], ""))
	}
	if rest := lines[max(left, len(did)):]; len(rest) > 0 {
		b.WriteString(heldHeader + strings.Join(rest, ""))
	}
	return b.String()
}
