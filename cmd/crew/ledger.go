package main

// The ledger's files: tasks, goals and proposals. Each is Markdown with flat
// `key: value` front matter between the first two `---` lines (there's no YAML
// parser), then `## ` sections, the last of which is `## Log`.
//
// A Doc reads its file afresh for every value and writes it back at once, under
// crew's ledger lock: agents edit these files too, so nothing is held in memory.

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Doc struct{ Path string }

// ID is the file's name without .md: 007-saved-filters, say.
func (d Doc) ID() string { return strings.TrimSuffix(filepath.Base(d.Path), ".md") }

// Get is the value of a front-matter key, or nothing.
func (d Doc) Get(key string) string {
	v, _ := d.Lookup(key)
	return v
}

// Lookup is the value of a front-matter key, and whether the key is there. A
// value in double quotes is unquoted.
func (d Doc) Lookup(key string) (string, bool) {
	ls := d.lines()
	if len(ls) == 0 || ls[0] != "---" {
		return "", false
	}
	for _, l := range ls[1:] {
		if l == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			v = strings.Trim(v, " \t")
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
			}
			return v, true
		}
	}
	return "", false
}

// Set writes a front-matter key, adding it before the closing --- when it's
// missing. A value goes on one line, and in double quotes when it has ": ", " #"
// or a quote in it.
func (d Doc) Set(key, value string) error {
	value = strings.ReplaceAll(value, "\n", " ")
	if strings.Contains(value, ": ") || strings.Contains(value, " #") || strings.Contains(value, `"`) {
		value = `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	line := key + ": " + value
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	inFM, done := false, false
	for i, l := range ls {
		switch {
		case i == 0 && l == "---":
			inFM = true
		case inFM && l == "---":
			if !done {
				out = append(out, line)
			}
			inFM = false
		case inFM && strings.HasPrefix(l, key+":"):
			if !done {
				out = append(out, line)
				done = true
			}
			continue
		}
		out = append(out, l)
	}
	return d.writeLines(out)
}

// Touch sets updated: to now.
func (d Doc) Touch() error { return d.Set("updated", now()) }

// Log appends a dated line to the end of the file, which is the Log section's.
func (d Doc) Log(msg string) error {
	return d.appendRaw("- " + time.Now().Format("2006-01-02 15:04") + " — " + msg + "\n")
}

// Section is a section's text, without HTML comments and blank lines.
func (d Doc) Section(heading string) string {
	var out []string
	on, inComment := false, false
	for _, l := range d.lines() {
		if !on {
			on = l == heading
			continue
		}
		if l == heading {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			break
		}
		var b strings.Builder
		for l != "" {
			if inComment {
				i := strings.Index(l, "-->")
				if i < 0 {
					l = ""
					break
				}
				l, inComment = l[i+3:], false
			} else {
				i := strings.Index(l, "<!--")
				if i < 0 {
					b.WriteString(l)
					l = ""
				} else {
					b.WriteString(l[:i])
					l, inComment = l[i+4:], true
				}
			}
		}
		if strings.Trim(b.String(), " \t") != "" {
			out = append(out, b.String())
		}
	}
	return strings.Join(out, "\n")
}

// OpenQuestions counts the unchecked items under Open questions: each is a
// question still waiting on the user.
func (d Doc) OpenQuestions() int {
	n := 0
	for _, l := range strings.Split(d.Section("## Open questions"), "\n") {
		if strings.HasPrefix(l, "- [ ]") {
			n++
		}
	}
	return n
}

// InsertBefore puts text, and a blank line, before the first line that is
// heading, or at the end of a file without one.
func (d Doc) InsertBefore(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	for i, l := range ls {
		if l == heading {
			out := append(append(append([]string{}, ls[:i]...), splitLines(text)...), "")
			return d.writeLines(append(out, ls[i:]...))
		}
	}
	return d.appendRaw(text)
}

// AppendToSection adds text at the end of a section, just before the next
// heading. A file without the section gets it, before ## Log.
func (d Doc) AppendToSection(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	on, done := false, false
	put := func() {
		out = append(append(out, splitLines(text)...), "")
		done = true
	}
	for _, l := range ls {
		if on && !done && strings.HasPrefix(l, "## ") {
			put()
		}
		out = append(out, l)
		if l == heading {
			on = true
		}
	}
	if !on {
		return d.InsertBefore("## Log", heading+"\n\n"+text)
	}
	if !done {
		out = append(out, "")
		put()
	}
	return d.writeLines(out)
}

// ReplaceSection replaces a section's body with text, keeping a blank line
// before the next heading.
func (d Doc) ReplaceSection(heading, text string) error {
	ls, err := d.read()
	if err != nil {
		return err
	}
	var out []string
	skip := false
	for _, l := range ls {
		switch {
		case l == heading:
			out = append(append(out, l), splitLines(text)...)
			skip = true
			continue
		case skip && strings.HasPrefix(l, "## "):
			out = append(out, "")
			skip = false
		}
		if !skip {
			out = append(out, l)
		}
	}
	return d.writeLines(out)
}

// lines is the file's lines, or none when it can't be read.
func (d Doc) lines() []string {
	ls, _ := d.read()
	return ls
}

func (d Doc) read() ([]string, error) {
	b, err := os.ReadFile(d.Path)
	return splitLines(string(b)), err
}

// splitLines splits text into lines the way awk reads them: a final newline ends
// the last line rather than starting another.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// writeLines rewrites the file in place, each line ending in a newline.
func (d Doc) writeLines(ls []string) error {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l + "\n")
	}
	return os.WriteFile(d.Path, []byte(b.String()), 0o644)
}

// appendRaw adds text at the end of the file, after a newline if the file
// doesn't end with one.
func (d Doc) appendRaw(text string) error {
	b, err := os.ReadFile(d.Path)
	if err != nil {
		return err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		text = "\n" + text
	}
	f, err := os.OpenFile(d.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func now() string { return time.Now().Format("2006-01-02T15:04:05-07:00") }

// A task's status. "In flight" means working, needs-input, addressing-feedback
// or pr-open.
type Status string

const (
	Draft              Status = "draft"
	Ready              Status = "ready"
	Working            Status = "working"
	NeedsInput         Status = "needs-input"
	PROpen             Status = "pr-open"
	AddressingFeedback Status = "addressing-feedback"
	Merged             Status = "merged"
	Abandoned          Status = "abandoned"
)

var taskStatuses = []Status{Draft, Ready, Working, NeedsInput, PROpen, AddressingFeedback, Merged, Abandoned}

func (s Status) InFlight() bool {
	return s == Working || s == NeedsInput || s == AddressingFeedback || s == PROpen
}

// Finished is merged or abandoned: nothing more happens to the task.
func (s Status) Finished() bool { return s == Merged || s == Abandoned }

// A goal's status.
type GoalStatus string

const (
	GoalDraft     GoalStatus = "draft"
	GoalRunning   GoalStatus = "running"
	GoalDone      GoalStatus = "done"
	GoalAbandoned GoalStatus = "abandoned"
)

var goalStatuses = []GoalStatus{GoalDraft, GoalRunning, GoalDone, GoalAbandoned}

type Task struct{ Doc }

func (t Task) Status() Status    { return Status(t.Get("status")) }
func (t Task) Project() string   { return t.Get("project") }
func (t Task) Branch() string    { return t.Get("branch") }
func (t Task) Base() string      { return t.Get("base") }
func (t Task) PR() string        { return t.Get("pr") }
func (t Task) Worktree() string  { return t.Get("worktree") }
func (t Task) WorkerJob() string { return t.Get("worker_job") }
func (t Task) Goal() string      { return t.Get("goal") }

// PRNumber is the number at the end of the task's PR URL.
func (t Task) PRNumber() string {
	_, n := splitLast(t.PR())
	return n
}

// Dispatched: crew records a worker or a worktree for it.
func (t Task) Dispatched() bool { return t.WorkerJob() != "" || t.Worktree() != "" }

type Goal struct{ Doc }

func (g Goal) Status() GoalStatus { return GoalStatus(g.Get("status")) }
func (g Goal) Project() string    { return g.Get("project") }
func (g Goal) LeadJob() string    { return g.Get("lead_job") }

type Proposal struct{ Doc }
