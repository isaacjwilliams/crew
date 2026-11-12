package main

// Starting the lead: crew lead, which bin/lead runs, and execLead, which crew
// lead and crew serve share. A lead runs in the home's lead/, whose settings make
// the session the lead and give it the ledger and the project checkouts.

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
)

// crew lead [--project P] [claude options] ["<request>"]: talk to the lead. Every
// run starts a new session; the ledger, not the conversation, carries state
// between them. `lead -c` continues the most recent lead conversation, and
// `lead "<request>"` starts on the request after the briefing.
//
// A lead works on one project when --project names it, or when it's started in
// that project's repo; crew's commands then default to that project. Otherwise
// it works on every project. CREW_PROJECT carries the choice to the session, and
// CREW_LEAD_FINGERPRINT what it loads, for crew lead --hook.
func cmdLead(c *Crew, args []string) error {
	if len(args) > 0 && args[0] == "--hook" {
		return c.leadHook()
	}
	project, claudeArgs, err := takeProject(args)
	if err != nil {
		return err
	}
	proj, err := c.scope(project, false)
	if err != nil {
		return err
	}
	// Every lead loads lead/.mcp.json, so bring it up to date first: a project
	// lead's --settings switch off the other projects' servers as it lists them.
	// A project's problem is reported and doesn't stop the lead.
	c.syncMCP()
	name, sessionProject, settings, mcp := "lead", allProjects, c.Settings, []string{}
	if proj != "" {
		name, sessionProject = "lead-"+proj, proj
		if settings, err = c.projectSettings(proj); err != nil {
			return err
		}
		off, err := c.leadMCPSettings(proj)
		if err != nil {
			return err
		}
		mcp = []string{"--settings", off}
	}
	flags := append([]string{"--name", name, "--permission-mode", settings["LEAD_PERMISSION_MODE"]}, mcp...)
	cmd := c.claude(append(flags, claudeArgs...)...)
	cmd.Env = append(cmd.Env, "CREW_PROJECT="+sessionProject, "CREW_LEAD_FINGERPRINT="+c.leadFingerprint(settings))
	return c.execLead(cmd)
}

// leadFingerprint is the fingerprint of what a lead started with settings loads:
// its permission mode, its settings file and its agent files (see fingerprint).
func (c *Crew) leadFingerprint(settings Settings) string {
	return c.fingerprint([]string{"--permission-mode", settings["LEAD_PERMISSION_MODE"]},
		append([]string{c.path("lead/.claude/settings.json")}, c.agentFiles("lead")...)...)
}

// crew lead --hook, the lead's UserPromptSubmit hook: Claude Code adds what it
// prints to the prompt. A lead runs on the instructions and settings it started
// with, so once they've changed, it tells the session to have the user start a
// new lead. Sessions crew lead didn't start, such as goal leads and the phone
// lead, have no CREW_LEAD_FINGERPRINT, or an empty one, and hear nothing.
func (c *Crew) leadHook() error {
	started := os.Getenv("CREW_LEAD_FINGERPRINT")
	if started == "" {
		return nil
	}
	// The settings of the project crew lead gave the session in CREW_PROJECT.
	proj, err := c.scope("", false)
	if err != nil {
		return err
	}
	settings := c.Settings
	if proj != "" {
		if settings, err = c.projectSettings(proj); err != nil {
			return err
		}
	}
	if started != c.leadFingerprint(settings) {
		fmt.Printf("crew's lead instructions or settings changed since this session started (crew is now %s). Tell the user once, briefly, that they should quit and start a new lead to get them.\n", c.crewVersion())
	}
	return nil
}

// takeProject takes the first --project P out of crew lead's arguments. The rest
// go to claude unchanged and in order. project is "" when there's no --project.
func takeProject(args []string) (project string, rest []string, err error) {
	i := slices.Index(args, "--project")
	switch {
	case i < 0:
		return "", args, nil
	case i == len(args)-1:
		return "", nil, fail("--project needs a project (crew status --all lists them)")
	}
	return args[i+1], slices.Concat(args[:i], args[i+2:]), nil
}

// execLead replaces crew with cmd, a lead's claude command, run in the home's
// lead/. Its caller syncs the MCP servers first, since the session loads
// lead/.mcp.json.
func (c *Crew) execLead(cmd *exec.Cmd) error {
	if err := os.Chdir(c.path("lead")); err != nil {
		return fail("%s is missing", c.path("lead"))
	}
	return execClaude(cmd)
}
