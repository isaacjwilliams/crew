package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// files lists the ledger files of a kind (tasks, goals or proposals) in a project,
// or in every project when project is empty, in order.
func (c *Crew) files(kind, project string) []string {
	if project == "" {
		project = "*"
	}
	matches, _ := filepath.Glob(c.path("projects", project, kind, "*.md"))
	var files []string
	for _, m := range matches {
		if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() {
			files = append(files, m)
		}
	}
	sort.Strings(files)
	return files
}

func (c *Crew) tasks(project string) []Task {
	var ts []Task
	for _, f := range c.files("tasks", project) {
		ts = append(ts, Task{Doc{f}})
	}
	return ts
}

func (c *Crew) goals(project string) []Goal {
	var gs []Goal
	for _, f := range c.files("goals", project) {
		gs = append(gs, Goal{Doc{f}})
	}
	return gs
}

var allDigits = regexp.MustCompile(`^[0-9]+$`)

// resolveIn picks the file arg names: "#<pr>" (the number of the file's pr), its
// exact ID, a number (7 is 007-…), or a unique substring of its ID. When more than
// one matches, it returns them all and no file.
func resolveIn(arg string, files []string) (string, []string) {
	var matches []string
	if n, ok := strings.CutPrefix(arg, "#"); ok {
		for _, f := range files {
			if pr := (Doc{f}).Get("pr"); pr != "" {
				if _, num := splitLast(pr); num == n {
					matches = append(matches, f)
				}
			}
		}
	} else {
		for _, f := range files {
			if (Doc{f}).ID() == arg {
				return f, nil
			}
		}
		if n, err := strconv.Atoi(arg); err == nil && allDigits.MatchString(arg) {
			prefix := fmt.Sprintf("%03d-", n)
			for _, f := range files {
				if strings.HasPrefix((Doc{f}).ID(), prefix) {
					matches = append(matches, f)
				}
			}
		} else {
			for _, f := range files {
				if strings.Contains((Doc{f}).ID(), arg) {
					matches = append(matches, f)
				}
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", matches
}

// ambiguous says that arg matches more than one file, and lists them.
func ambiguous(arg string, matches []string) string {
	s := `"` + arg + `" is ambiguous:`
	for _, m := range matches {
		s += "\n  " + filepath.Base(m)
	}
	return s
}

// resolve finds the file of a task, goal or proposal: a path, or what resolveIn
// takes, looked up in the current directory's project first, then in all of them.
func (c *Crew) resolve(kind, arg string) (string, error) {
	if isFile(arg) {
		abs, err := filepath.Abs(arg)
		if err == nil {
			abs, err = filepath.EvalSymlinks(abs)
		}
		return abs, err
	}
	scopes := []string{""}
	if p, ok := c.projectFromCwd(); ok {
		scopes = []string{p, ""}
	}
	for _, p := range scopes {
		if files := c.files(kind, p); len(files) > 0 {
			f, matches := resolveIn(arg, files)
			if f != "" {
				return f, nil
			}
			if len(matches) > 1 {
				return "", fail("%s", ambiguous(arg, matches))
			}
		}
	}
	return "", fail("no %s matches '%s'", strings.TrimSuffix(kind, "s"), arg)
}

func (c *Crew) resolveTask(arg string) (Task, error) {
	f, err := c.resolve("tasks", arg)
	return Task{Doc{f}}, err
}

func (c *Crew) resolveGoal(arg string) (Goal, error) {
	f, err := c.resolve("goals", arg)
	return Goal{Doc{f}}, err
}
