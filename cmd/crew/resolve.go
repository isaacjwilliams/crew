package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// files lists the ledger files of a kind (tasks, goals or proposals) in a project,
// or in every project when project is empty. They're ordered by project, then by
// number, so 1000-… comes after 999-….
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
	slices.SortFunc(files, func(a, b string) int {
		na, _ := fileNumber(a)
		nb, _ := fileNumber(b)
		return cmp.Or(cmp.Compare(filepath.Dir(a), filepath.Dir(b)), cmp.Compare(na, nb), cmp.Compare(a, b))
	})
	return files
}

// fileNumber is the NNN of a file named NNN-….md, if it has one.
func fileNumber(f string) (int, bool) {
	lead, _, _ := strings.Cut(filepath.Base(f), "-")
	n, err := strconv.Atoi(lead)
	return n, err == nil && allDigits.MatchString(lead)
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

// resolveIn picks the file arg names: a path to it, "#<pr>" (the number of the
// file's pr), its exact ID, a number (7 is 007-…), or a unique substring of its
// ID. When more than one matches, it returns them all and no file.
func resolveIn(arg string, files []string) (string, []string) {
	if fi, err := os.Stat(arg); err == nil {
		for _, f := range files {
			if ff, err := os.Stat(f); err == nil && os.SameFile(fi, ff) {
				return f, nil
			}
		}
	}
	var matches []string
	add := func(match func(f string) bool) {
		for _, f := range files {
			if match(f) {
				matches = append(matches, f)
			}
		}
	}
	isID := func(f string) bool { return (Doc{f}).ID() == arg }
	prNumber, isPR := strings.CutPrefix(arg, "#")
	n, err := strconv.Atoi(arg)
	isNumber := err == nil && allDigits.MatchString(arg)
	switch {
	case isPR:
		add(func(f string) bool {
			pr := (Doc{f}).Get("pr")
			_, num := splitLast(pr)
			return pr != "" && num == prNumber
		})
	case slices.ContainsFunc(files, isID):
		// Every project numbers its own tasks, so two projects can share an ID.
		add(isID)
	case isNumber:
		prefix := fmt.Sprintf("%03d-", n)
		add(func(f string) bool { return strings.HasPrefix((Doc{f}).ID(), prefix) })
	default:
		add(func(f string) bool { return strings.Contains((Doc{f}).ID(), arg) })
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", matches
}

// ambiguous says that arg matches more than one file, and lists them with their
// projects, since two projects can have files of the same name.
func ambiguous(arg string, matches []string) string {
	s := `"` + arg + `" is ambiguous:`
	for _, m := range matches {
		s += "\n  " + filepath.Base(m) + " (" + (Doc{m}).Get("project") + ")"
	}
	return s
}

// resolve finds the file of a task, goal or proposal: a path, or what resolveIn
// takes, looked up in the default project first (defaultProject), then in all
// of them.
func (c *Crew) resolve(kind, arg string) (string, error) {
	if isFile(arg) {
		return realPath(arg)
	}
	for _, p := range c.searchOrder() {
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

// freeFiles are the free-form files a user reads, for a project or every project
// when it's empty: those directly inside the home's reports/ and logs/, each
// news/<date>.log, and those directly inside the project's review-eval/.
func (c *Crew) freeFiles(project string) []string {
	if project == "" {
		project = "*"
	}
	globs := []string{c.path("reports", "*"), c.path("logs", "*"), c.path("news", "*.log"), c.path("projects", project, "review-eval", "*")}
	var files []string
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() {
				files = append(files, m)
			}
		}
	}
	return files
}

// resolveName picks the file arg names: one whose name is arg, with or without
// its extension, or else one whose name contains arg. When more than one
// matches, it returns them all and no file. Unlike resolveIn, it knows nothing of
// task numbers or PRs: these files are named freely.
func resolveName(arg string, files []string) (string, []string) {
	var exact, partial []string
	for _, f := range files {
		name := filepath.Base(f)
		switch {
		case name == arg || strings.TrimSuffix(name, filepath.Ext(name)) == arg:
			exact = append(exact, f)
		case strings.Contains(name, arg):
			partial = append(partial, f)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", matches
}

// resolveFile finds a free-form file (freeFiles): a path, or what resolveName
// takes. It looks in the home's folders and those of project, or of the
// default project first, then all of them.
func (c *Crew) resolveFile(arg, project string) (string, error) {
	if isFile(arg) {
		return realPath(arg)
	}
	var scopes []string
	if project == "" {
		scopes = c.searchOrder()
	} else {
		p, err := c.scope(project, false)
		if err != nil {
			return "", err
		}
		scopes = []string{p}
	}
	for _, p := range scopes {
		f, matches := resolveName(arg, c.freeFiles(p))
		if f != "" {
			return f, nil
		}
		if len(matches) > 1 {
			s := `"` + arg + `" is ambiguous:`
			for _, m := range matches {
				rel, _ := filepath.Rel(c.Home, m)
				s += "\n  " + rel
			}
			return "", fail("%s", s)
		}
	}
	return "", fail("no file matches '%s'", arg)
}

// realPath is the absolute path a file's path leads to.
func realPath(f string) (string, error) {
	abs, err := filepath.Abs(f)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// searchOrder is the scopes a lookup tries in turn: the default project
// (defaultProject), then every project.
func (c *Crew) searchOrder() []string {
	if p, _ := c.defaultProject(); p != "" {
		return []string{p, ""}
	}
	return []string{""}
}

// isProposal says whether f, a file resolve found, is a proposal's: it's in a
// project's proposals directory.
func isProposal(f string) bool { return filepath.Base(filepath.Dir(f)) == "proposals" }

func (c *Crew) resolveTask(arg string) (Task, error) {
	f, err := c.resolve("tasks", arg)
	return Task{Doc{f}}, err
}

// taskArg names t for a crew command run from here: by its ID when that resolves
// back to t, or else by its file's path, quoted for the shell. Another project
// can have a task with the same ID, and it would win as the default project.
func (c *Crew) taskArg(t Task) string {
	if u, err := c.resolveTask(t.ID()); err == nil && sameFile(u.Path, t.Path) {
		return t.ID()
	}
	return "'" + strings.ReplaceAll(t.Path, "'", `'\''`) + "'"
}

// sameFile says whether paths a and b lead to the same file.
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func (c *Crew) resolveGoal(arg string) (Goal, error) {
	f, err := c.resolve("goals", arg)
	return Goal{Doc{f}}, err
}
