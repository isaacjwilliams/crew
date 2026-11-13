package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The ledger: what crew and the agents write in the home. The rest of the home is
// crew's own code and config, which comes from upstream.
var statePaths = []string{"projects", "reports"}

// crewHome is the crew home: a clone of the crew repo, with crew's code, its
// config and the ledger. It's $CREW_HOME, which bin/crew sets to its own checkout
// unless it's set already, else the checkout this binary is in.
func crewHome() string {
	if h := os.Getenv("CREW_HOME"); h != "" {
		return h
	}
	return checkoutOfBinary()
}

// checkoutOfBinary is the checkout this binary was run from: it's
// <checkout>/bin/release/crew-<version>.
func checkoutOfBinary() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(exe)))
}

// Crew is one run of crew in a home.
type Crew struct {
	Home     string
	Settings Settings // config/defaults.env, then the person's config/crew.env
	Host     string   // this machine's name: CREW_HOST, else machineName()

	ledgerLock *os.File // held from lock until unlock or the end of the process
	login      string   // the GitHub login crew acts for, once looked up
}

func newCrew() (*Crew, error) {
	c := &Crew{Home: crewHome(), Host: os.Getenv("CREW_HOST")}
	if c.Home == "" || !isFile(filepath.Join(c.Home, "config/defaults.env")) {
		return nil, fail("%s isn't a crew home (no config/defaults.env); set CREW_HOME?", c.Home)
	}
	// So the sessions, hooks and wt runs crew starts find it too.
	os.Setenv("CREW_HOME", c.Home)
	if c.Host == "" {
		c.Host = machineName()
	}
	// Every setting has its default in defaults.env; config/crew.env holds the person's own.
	c.Settings = Settings{}
	if err := readEnvFile(filepath.Join(c.Home, "config/defaults.env"), c.Settings); err != nil {
		return nil, err
	}
	if p := filepath.Join(c.Home, "config/crew.env"); isFile(p) {
		if err := readEnvFile(p, c.Settings); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// shortHostname is the hostname up to its first dot.
func shortHostname() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	return h
}

func (c *Crew) path(parts ...string) string {
	return filepath.Join(append([]string{c.Home}, parts...)...)
}

// Project is a repo the crew works on, with its settings: the shared ones, then
// its project.env (committed, the same on every machine: REMOTE, DEFAULT_BRANCH,
// overrides), then its local.env (this machine's REPO, never committed).
type Project struct {
	Name     string
	Dir      string
	Home     string // the crew home it's in
	Settings Settings
}

func (p *Project) Repo() string          { return p.Settings["REPO"] }
func (p *Project) DefaultBranch() string { return p.Settings["DEFAULT_BRANCH"] }
func (p *Project) WorkRoot() string      { return p.Settings["WORK_ROOT"] }

// loadProject is a project that's cloned on this machine, with its settings.
func (c *Crew) loadProject(name string) (*Project, error) {
	p, err := c.ledgerProject(name)
	if err != nil {
		return nil, err
	}
	if p.Repo() == "" || !isDir(p.Repo()) {
		return nil, fail("no clone of %s on this machine (crew host up makes one, or run 'crew project add' in your clone of it)", name)
	}
	return p, nil
}

// ledgerProject is a project with its settings, cloned on this machine or not:
// for commands that need only its ledger files.
func (c *Crew) ledgerProject(name string) (*Project, error) {
	s, err := c.projectSettings(name)
	if err != nil {
		return nil, err
	}
	return &Project{Name: name, Dir: c.path("projects", name), Home: c.Home, Settings: s}, nil
}

// projectSettings is a project's settings: the shared ones, then its
// project.env, then its local.env. Unlike loadProject, it doesn't need the
// project cloned here.
func (c *Crew) projectSettings(name string) (Settings, error) {
	s, dir := c.Settings.clone(), c.path("projects", name)
	env := filepath.Join(dir, "project.env")
	if !isFile(env) {
		return nil, fail("unknown project '%s' (run 'crew project add' in the repo)", name)
	}
	if err := readEnvFile(env, s); err != nil {
		return nil, err
	}
	if local := filepath.Join(dir, "local.env"); isFile(local) {
		if err := readEnvFile(local, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// allProjects is the CREW_PROJECT of a session that works on every project.
const allProjects = "all"

// defaultProject is the project a command works on when it isn't told which,
// or "" for every project. In a session crew starts, the lead's included, it's
// CREW_PROJECT, a project or "all". In your own shell, it's the project
// whose repo you're in. The session's choice wins over the current directory,
// which in a session is only wherever its shell last went.
func (c *Crew) defaultProject() (string, error) {
	switch p := os.Getenv("CREW_PROJECT"); p {
	case allProjects:
		return "", nil
	case "":
		p, _ = c.projectFromDir(".")
		return p, nil
	default:
		if !isDir(c.path("projects", p)) {
			return "", fail("CREW_PROJECT is '%s', which isn't a project registered with crew", p)
		}
		return p, nil
	}
}

func (c *Crew) projectFromDir(dir string) (string, bool) {
	root, ok := repoRoot(dir)
	if !ok {
		return "", false
	}
	s := slugFor(root)
	if !isDir(c.path("projects", s)) {
		return "", false
	}
	return s, true
}

// scope is the project a command works on, or "" for every project: --project
// when it's given (--project all is every project, like --all), every project
// with --all, else defaultProject.
func (c *Crew) scope(project string, all bool) (string, error) {
	switch {
	case all || project == allProjects:
		return "", nil
	case project != "":
		if !isDir(c.path("projects", project)) {
			return "", fail("unknown project '%s'", project)
		}
		return project, nil
	}
	return c.defaultProject()
}

// oneProject is scope for a command that works on one project: it fails rather
// than take every project.
func (c *Crew) oneProject(project string) (string, error) {
	p, err := c.scope(project, false)
	if err == nil && p == "" {
		err = fail("which project? Pass --project P (crew status --all lists them), or run this in a repo registered with crew")
	}
	return p, err
}

// projects is the projects in a scope: the one it names, or every registered
// project for "".
func (c *Crew) projects(scope string) []string {
	if scope != "" {
		return []string{scope}
	}
	var ps []string
	dirs, _ := filepath.Glob(c.path("projects", "*", "project.env"))
	for _, d := range dirs {
		ps = append(ps, filepath.Base(filepath.Dir(d)))
	}
	return ps
}

// repoRoot is the main checkout of the repo dir is in, also from a worktree.
func repoRoot(dir string) (string, bool) {
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", false
	}
	return filepath.Dir(common), true
}

// slugFor names a repo's project: <owner>-<repo> from its origin, else its
// directory's name.
func slugFor(root string) string {
	url, err := git(root, "remote", "get-url", "origin")
	if err != nil || url == "" {
		return slugify(filepath.Base(root))
	}
	s := strings.TrimSuffix(strings.TrimSuffix(url, ".git"), "/")
	s = strings.ReplaceAll(s, ":", "/")
	rest, repo := splitLast(s)
	_, owner := splitLast(rest)
	return slugify(owner + "-" + repo)
}

// splitLast splits s at its last slash: "a/b/c" is "a/b" and "c".
func splitLast(s string) (string, string) {
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return s, s
	}
	return s[:i], s[i+1:]
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// intSetting reads a setting that has to be a whole number.
func intSetting(s Settings, key string) (int, error) {
	n, err := strconv.Atoi(s[key])
	if err != nil {
		return 0, fail("%s must be a whole number, not '%s'", key, s[key])
	}
	return n, nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// git runs git in dir and returns its output without trailing newlines.
func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// gitOK runs git in dir, quietly, and reports whether it succeeded.
func gitOK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

// gitRun runs git in dir with its output going to crew's own.
func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// say and note are what crew install and the host commands report progress with.
func say(format string, a ...any)  { fmt.Printf("\033[1m"+format+"\033[0m\n", a...) }
func note(format string, a ...any) { fmt.Printf("  "+format+"\n", a...) }
