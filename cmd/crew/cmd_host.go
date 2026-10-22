package main

// Which machine runs the crew (crew host), and the person's own crew (crew setup).

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func cmdHost(c *Crew, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		if holder := c.holder(); holder != "" {
			fmt.Printf("crew runs on %s (since %s)\n", holder, c.leaseSince())
		} else {
			fmt.Println("no machine runs the crew")
		}
		fmt.Println("this machine: " + c.Host)
		return nil
	case "up":
		take := false
		for _, a := range args {
			if a != "--take" {
				return usage("crew host up [--take]")
			}
			take = true
		}
		return c.hostUp(take)
	case "down":
		force := false
		for _, a := range args {
			if a != "--force" {
				return usage("crew host down [--force]")
			}
			force = true
		}
		return c.hostDown(force)
	}
	return usage("crew host [up [--take] | down [--force]]")
}

// hostUp runs the crew on this machine: it installs, claims the lease, clones the
// projects this machine has no clone of, and enables the units.
func (c *Crew) hostUp(take bool) error {
	if !gitOK(c.Home, "remote", "get-url", "origin") {
		return fail("the home has no origin to claim the lease through")
	}
	if err := c.runSelf(os.Stdout, "install"); err != nil {
		return err
	}
	say("Claiming the lease for %s", c.Host)
	if err := c.claimLease(take); err != nil {
		return err
	}
	note("crew runs on %s", c.Host)
	c.cloneMissingProjects()
	c.setUnits("enable")
	note("enabled %s", strings.Join(crewUnits, " "))
	c.runSelf(os.Stdout, "doctor")
	return nil
}

// runSelf runs another crew command in a process of its own, with its output to w.
func (c *Crew) runSelf(w io.Writer, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, w, os.Stderr
	return quietly(cmd.Run())
}

// claimLease claims the lease for this machine by pushing the host file. A
// rejected push means another machine moved first: this claim is dropped and
// origin looked at again. Without take, a lease another machine holds is refused.
func (c *Crew) claimLease(take bool) error {
	if err := c.takeLock(); err != nil {
		return err
	}
	defer c.unlock()
	claimed := false
	for range 5 {
		if gitRun(c.Home, "pull", "-q", "--ff-only") != nil {
			return fail("the home has ledger commits origin hasn't and origin has commits it hasn't; settle that first (git -C %s status)", c.Home)
		}
		if holder := c.holder(); holder != "" && holder != c.Host && !take {
			return fail("crew runs on %s (since %s); crew host up --take moves it here", holder, c.leaseSince())
		}
		if err := os.WriteFile(c.path("host"), []byte(c.Host+"\n"), 0o644); err != nil {
			return err
		}
		if c.commitHost("host: " + c.Host) {
			claimed = true
			break
		}
		gitOK(c.Home, "reset", "-q", "--hard", "@{u}")
	}
	if !claimed {
		return fail("couldn't push the lease to origin")
	}
	os.Remove(c.leaseLost())
	return nil
}

// commitHost commits the host file and pushes it in the foreground: a lease counts
// only once origin has it. A rejected push is expected when another machine moved
// first, so git's message is left out; the caller says what failed. Like
// pushLedger, it doesn't push with -u.
func (c *Crew) commitHost(msg string) bool {
	gitOK(c.Home, "add", "--", "host")
	if !gitOK(c.Home, "diff", "--cached", "--quiet", "--", "host") {
		gitOK(c.Home, "commit", "-qm", msg, "--", "host")
	}
	return gitOK(c.Home, "push", "-q", "origin", "HEAD")
}

// cloneMissingProjects clones each registered project this machine has no clone
// of, into $WORK_ROOT/<p>/repo, and records it as this machine's.
func (c *Crew) cloneMissingProjects() {
	envs, _ := filepath.Glob(c.path("projects", "*", "project.env"))
	for _, env := range envs {
		pd := filepath.Dir(env)
		if isFile(filepath.Join(pd, "local.env")) {
			continue
		}
		p := filepath.Base(pd)
		s := c.Settings.clone()
		readEnvFile(env, s)
		remote := s["REMOTE"]
		if remote == "" {
			warn("%s has no REMOTE in project.env; run 'crew project add' in a clone of it", p)
			continue
		}
		clone := filepath.Join(c.Settings["WORK_ROOT"], p, "repo")
		if !isDir(filepath.Join(clone, ".git")) {
			os.MkdirAll(filepath.Dir(clone), 0o755)
			if gitRun(".", "clone", "-q", remote, clone) != nil {
				warn("couldn't clone %s for %s", remote, p)
				continue
			}
		}
		if c.runSelf(io.Discard, "project", "add", clone) != nil {
			warn("couldn't register %s for %s", clone, p)
		}
		note("%s: this machine's clone is %s", p, clone)
	}
}

// setUnits enables or disables the crew's systemd units, now.
func (c *Crew) setUnits(action string) {
	if exec.Command("systemctl", append([]string{"--user", action, "--now"}, crewUnits...)...).Run() != nil {
		warn("couldn't %s %s (systemctl --user)", action, strings.Join(crewUnits, " "))
	}
}

// stopCrewJobs stops every background session crew started on this machine.
func (c *Crew) stopCrewJobs() {
	for _, a := range c.agents() {
		if strings.HasPrefix(a.Name, "crew-") && a.open() && !c.stopJob(a.ID) {
			warn("background session %s didn't stop", a.ID)
		}
	}
}

// hostDown hands over: it stops the crew here and releases the lease, unless a
// worktree has work origin doesn't have.
func (c *Crew) hostDown(force bool) error {
	if err := c.lock(); err != nil {
		return err
	}
	// Work that exists only in this machine's worktrees would be lost to the next host.
	var unpushed []string
	for _, t := range c.tasks("") {
		wt := t.Worktree()
		if wt == "" || !isDir(wt) {
			continue
		}
		if !gitOK(wt, "rev-parse", "--verify", "--quiet", "@{u}") {
			unpushed = append(unpushed, t.ID()+": its branch was never pushed")
		} else if n, err := git(wt, "rev-list", "--count", "@{u}..@"); err == nil && n != "0" {
			unpushed = append(unpushed, fmt.Sprintf("%s: %s commit(s) not pushed", t.ID(), n))
		}
	}
	if len(unpushed) > 0 && !force {
		for _, u := range unpushed {
			fmt.Fprintln(os.Stderr, "  "+u)
		}
		return fail("these worktrees have work origin doesn't; have their workers push, or use --force")
	}
	c.setUnits("disable")
	c.stopCrewJobs()
	// The push may have to take the ledger lock to rebase.
	c.unlock()
	if !c.pushNow(os.Stderr) {
		warn("couldn't push every ledger commit before handing over")
	}
	if err := c.takeLock(); err != nil {
		return err
	}
	if err := os.WriteFile(c.path("host"), nil, 0o644); err != nil {
		return err
	}
	if !c.commitHost("host: none (" + c.Host + " stepped down)") {
		return fail("couldn't push the handover to origin; crew still runs here as far as origin knows")
	}
	fmt.Println("crew runs on no machine now; crew host up starts it on the next one")
	return nil
}

// exitNotHost is what crew serve exits with on a machine that doesn't run the
// crew, which crew-lead.service takes as "don't restart" (RestartPreventExitStatus).
const exitNotHost = 3

// crew serve is what crew-lead.service runs: the lead, as a Remote Control server
// in the home's lead/, so the person opens it from Claude Mobile.
func cmdServe(c *Crew, args []string) error {
	if len(args) > 0 {
		return usage("crew serve")
	}
	if err := c.requireHost(); err != nil {
		report(err)
		return exitQuietly(exitNotHost)
	}
	if err := os.Chdir(c.path("lead")); err != nil {
		return fail("%s is missing", c.path("lead"))
	}
	// Without --continue: it fails when no earlier server ran here, and serves only
	// one session. A plain start brings back the last server's sessions, if it
	// stopped in the last few hours. same-dir keeps sessions in lead/, not in
	// worktrees of the home. Run like any session crew starts, so this home's crew
	// is the one its sessions find.
	cmd := c.claude("remote-control", "--name", "crew", "--spawn", "same-dir", "--permission-mode", c.Settings["LEAD_PERMISSION_MODE"])
	path, err := exec.LookPath("claude")
	if err != nil {
		return fail("'claude' is required but not installed")
	}
	return syscall.Exec(path, cmd.Args, cmd.Env)
}

// crew setup [--name NAME] turns a fresh clone of the public crew repo into a
// person's own crew: a private GitHub repo becomes origin, the public one
// upstream, which can never be pushed to, so a ledger can't reach it. Then it
// writes their settings file and runs the crew on this machine. Once per person;
// another machine clones the private repo.
func cmdSetup(c *Crew, args []string) error {
	name := "crew"
	for len(args) > 0 {
		if args[0] != "--name" || len(args) < 2 {
			return usage("crew setup [--name NAME]")
		}
		name, args = args[1], args[2:]
	}
	if err := need("gh"); err != nil {
		return err
	}
	if exists(c.path("config/crew.env")) {
		return fail("%s is set up already (config/crew.env exists)", c.Home)
	}
	public, err := git(c.Home, "remote", "get-url", "origin")
	if err != nil {
		return fail("%s has no origin; clone the public crew repo and run crew setup in it", c.Home)
	}
	login, err := c.ghLogin()
	if err != nil {
		return fail("gh isn't signed in; run 'gh auth login' first")
	}
	say("Making %s/%s, your private crew repo", login, name)
	gitOK(c.Home, "remote", "rename", "origin", "upstream")
	// Not a URL, so any push to upstream fails.
	gitOK(c.Home, "remote", "set-url", "--push", "upstream", "DISABLED")
	create := exec.Command("gh", "repo", "create", login+"/"+name, "--private", "--source", ".", "--remote", "origin")
	create.Dir, create.Stderr = c.Home, os.Stderr
	if create.Run() != nil {
		gitOK(c.Home, "remote", "rename", "upstream", "origin")
		gitOK(c.Home, "config", "--unset", "remote.origin.pushurl")
		return fail("couldn't create %s/%s; if you have a repo by that name, pick another with crew setup --name NAME", login, name)
	}
	origin, _ := git(c.Home, "remote", "get-url", "origin")
	note("origin: %s; upstream: %s (never pushed to)", origin, public)
	tmpl, err := os.ReadFile(c.path("templates/crew.env"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.path("config/crew.env"), tmpl, 0o644); err != nil {
		return err
	}
	gitOK(c.Home, "add", "--", "config/crew.env")
	gitOK(c.Home, "commit", "-qm", "crew: set up for "+login, "--", "config/crew.env")
	if gitRun(c.Home, "push", "-q", "-u", "origin", "HEAD") != nil {
		return fail("couldn't push to origin. Push with 'git -C %s push -u origin HEAD', then run crew host up", c.Home)
	}
	if err := c.hostUp(false); err != nil {
		return err
	}
	say("Your crew is set up. Next:")
	fmt.Printf(`  1. Trust two folders once (accept, then /exit):   cd %s; and claude
                                                    cd %s/lead; and claude
  2. Set BRANCH_PREFIX in %s/config/crew.env, then commit and push it.
  3. In each repo you want the crew to work on:   crew project add
  4. Talk to the lead: lead, or the crew session in the Claude app.
  See SETUP.md for the full walkthrough.
`, c.Settings["WORK_ROOT"], c.Home, c.Home)
	return nil
}
