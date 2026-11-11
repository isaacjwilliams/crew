package main

// The lead, started in the home's lead/, whose settings make the session the lead
// and give it the ledger and the project checkouts.

import (
	"os"
	"os/exec"
	"syscall"
)

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
