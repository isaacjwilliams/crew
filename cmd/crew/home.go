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
	Host     string   // this machine's name: CREW_HOST, else its short hostname

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
		h, _ := os.Hostname()
		c.Host, _, _ = strings.Cut(h, ".")
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

func (c *Crew) path(parts ...string) string {
	return filepath.Join(append([]string{c.Home}, parts...)...)
}

// Project is a repo the crew works on, with its settings: the shared ones, then
// its project.env (committed, the same on every machine: REMOTE, DEFAULT_BRANCH,
// overrides), then its local.env (this machine's REPO, never committed).
type Project struct {
	Name     string
	Dir      string
	Settings Settings
}

func (p *Project) Repo() string          { return p.Settings["REPO"] }
func (p *Project) DefaultBranch() string { return p.Settings["DEFAULT_BRANCH"] }
func (p *Project) WorkRoot() string      { return p.Settings["WORK_ROOT"] }

func (c *Crew) loadProject(name string) (*Project, error) {
	p := &Project{Name: name, Dir: c.path("projects", name), Settings: c.Settings.clone()}
	env := filepath.Join(p.Dir, "project.env")
	if !isFile(env) {
		return nil, fail("unknown project '%s' (run 'crew project add' in the repo)", name)
	}
	if err := readEnvFile(env, p.Settings); err != nil {
		return nil, err
	}
	if local := filepath.Join(p.Dir, "local.env"); isFile(local) {
		if err := readEnvFile(local, p.Settings); err != nil {
			return nil, err
		}
	}
	if p.Repo() == "" || !isDir(p.Repo()) {
		return nil, fail("no clone of %s on this machine (crew host up makes one, or run 'crew project add' in your clone of it)", name)
	}
	return p, nil
}

// projectFromCwd is the registered project whose repo the current directory is in.
func (c *Crew) projectFromCwd() (string, bool) { return c.projectFromDir(".") }

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

func (c *Crew) currentProject() (string, error) {
	if p, ok := c.projectFromCwd(); ok {
		return p, nil
	}
	return "", fail("this directory isn't in a repo registered with crew (run 'crew project add' there)")
}

// projectsInScope is the cwd's project, or every registered project when run
// outside one, or when all is set.
func (c *Crew) projectsInScope(all bool) []string {
	if !all {
		if p, ok := c.projectFromCwd(); ok {
			return []string{p}
		}
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
