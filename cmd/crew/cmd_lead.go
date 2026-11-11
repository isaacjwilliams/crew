package main

// The lead, started in the home's lead/, whose settings make the session the lead
// and give it the ledger and the project checkouts: crew lead, which bin/lead
// runs, and crew serve.

import (
	"os"
	"os/exec"
	"slices"
	"syscall"
)

// crew lead [--project P] [claude options] ["<request>"]: talk to the lead. Every
// run starts a new session; the ledger, not the conversation, carries state
// between them. `lead -c` continues the most recent lead conversation, and
// `lead "<request>"` starts on the request after the briefing.
//
// A lead works on one project when --project names it, or when it's started in
// that project's repo; crew's commands then default to that project. Otherwise
// it works on every project. CREW_PROJECT carries the choice to the session.
func cmdLead(c *Crew, args []string) error {
	project, claudeArgs, err := takeProject(args)
	if err != nil {
		return err
	}
	proj, err := c.scope(project, false)
	if err != nil {
		return err
	}
	name, sessionProject, settings := "lead", allProjects, c.Settings
	if proj != "" {
		name, sessionProject = "lead-"+proj, proj
		if settings, err = c.projectSettings(proj); err != nil {
			return err
		}
	}
	// Every lead loads lead/.mcp.json, so bring it up to date. A project's problem
	// is reported and doesn't stop the lead.
	c.syncMCP()
	flags := []string{"--name", name, "--permission-mode", settings["LEAD_PERMISSION_MODE"]}
	if proj != "" {
		// After the sync: these settings switch off the other projects' servers
		// as lead/.mcp.json lists them now.
		off, err := c.leadMCPSettings(proj)
		if err != nil {
			return err
		}
		flags = append(flags, "--settings", off)
	}
	cmd := c.claude(append(flags, claudeArgs...)...)
	cmd.Env = append(cmd.Env, "CREW_PROJECT="+sessionProject)
	return c.execLead(cmd)
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
	path, err := exec.LookPath("claude")
	if err != nil {
		return fail("'claude' is required but not installed")
	}
	return syscall.Exec(path, cmd.Args, cmd.Env)
}
