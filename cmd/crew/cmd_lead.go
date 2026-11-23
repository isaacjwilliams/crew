package main

// Starting the lead: crew lead, which bin/lead runs, and execLead, which crew
// lead and crew serve share. A lead runs in the home's lead/, whose settings make
// the session the lead and give it the ledger and the project checkouts.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"unicode/utf8"
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
		return c.leadHook(os.Stdin)
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
	name, sessionProject := "lead", allProjects
	if proj != "" {
		name, sessionProject = "lead-"+proj, proj
	}
	flags, err := c.leadFlags(proj)
	if err != nil {
		return err
	}
	cmd := c.claude(slices.Concat([]string{"--name", name}, flags, claudeArgs)...)
	cmd.Env = append(cmd.Env, "CREW_PROJECT="+sessionProject, "CREW_LEAD_FINGERPRINT="+c.leadFingerprint(flags))
	return c.execLead(cmd)
}

// leadFlags is the flags a lead for proj ("" for every project) starts with,
// besides its name: its permission mode, and --settings that turn off the other
// projects' servers (leadMCPSettings) and what a lead doesn't use of yours
// (leanSettings). A lead keeps what its projects' KEEP_LOADED name.
func (c *Crew) leadFlags(proj string) ([]string, error) {
	settings, lead := c.Settings, newObject()
	var keep []string
	for _, p := range c.projects(proj) {
		s, err := c.projectSettings(p)
		if err != nil {
			return nil, err
		}
		keep = append(keep, strings.Fields(s["KEEP_LOADED"])...)
		// A lead for one project takes its permission mode from that project.
		if p == proj {
			settings = s
		}
	}
	if proj != "" {
		var err error
		if lead, err = c.leadMCPSettings(proj); err != nil {
			return nil, err
		}
	}
	lead = leanSettings(lead, "lead", keep)
	return []string{"--permission-mode", settings["LEAD_PERMISSION_MODE"], "--settings", encodeJSON(lead, false)}, nil
}

// leadFingerprint is the fingerprint of what a lead started with flags loads:
// its flags, its settings file and its agent files (see fingerprint).
func (c *Crew) leadFingerprint(flags []string) string {
	return c.fingerprint(flags, append([]string{c.path("lead/.claude/settings.json")}, c.agentFiles("lead")...)...)
}

// crew lead --hook, the lead's SessionStart and UserPromptSubmit hook: Claude
// Code adds what it prints to the prompt. With each prompt, the session hears
// the news since its previous one (leadNews). A lead runs on the instructions
// and settings it started with, so once they've changed, crew lead's session
// is also told to have the user start a new lead. The phone lead (crew serve)
// hears the news too; a goal lead runs with the lead's settings, and hears
// nothing (see inWorkerOrGoalLead).
func (c *Crew) leadHook(in io.Reader) error {
	if inWorkerOrGoalLead() {
		return nil
	}
	var input struct {
		SessionID string `json:"session_id"`
		Event     string `json:"hook_event_name"`
	}
	// Without a session ID in the input, leadNews has no record to keep and says nothing.
	json.NewDecoder(in).Decode(&input)
	// The project crew lead or crew serve gave the session in CREW_PROJECT.
	proj, err := c.scope("", false)
	if err != nil {
		return err
	}
	// A session that's starting has just loaded what's current: nothing has changed.
	starting := input.Event == "SessionStart"
	var out string
	if started := os.Getenv("CREW_LEAD_FINGERPRINT"); started != "" && !starting {
		flags, err := c.leadFlags(proj)
		if err != nil {
			return err
		}
		if started != c.leadFingerprint(flags) {
			out = fmt.Sprintf("crew's lead instructions or settings changed since this session started (crew is now %s). Tell the user once, briefly, that they should quit and start a new lead to get them.\n", c.crewVersion())
		}
	}
	out += c.leadNews(input.SessionID, proj, starting, hookOutputCap-utf8.RuneCountInString(out))
	fmt.Print(out)
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
