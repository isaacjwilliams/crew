package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A Codex that starts and then goes quiet is stopped once the stall limit
// passes, and crew codex exits 124. The rest of crew codex is tested in
// tests/crew-e2e.sh, with tests/stubs/codex.
func TestCodexStopsAStalledCodex(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho '{\"type\":\"thread.started\",\"thread_id\":\"0199a3b2-1c2d-7e3f-8a9b-0c1d2e3f4a5b\"}'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CREW_PROJECT", allProjects)
	prompt := filepath.Join(dir, "prompt.md")
	os.WriteFile(prompt, []byte("Review this.\n"), 0o644)
	c := &Crew{Home: dir, Settings: Settings{"CODEX_MODEL": "gpt-test", "CODEX_MODEL_EFFORT": "high"}}

	defer func(limit time.Duration) { codexStallLimit = limit }(codexStallLimit)
	codexStallLimit = 500 * time.Millisecond
	began := time.Now()
	err := cmdCodex(c, []string{dir, prompt, filepath.Join(dir, "out", "answer-0")})
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("took %s to stop a Codex that went quiet; want about the stall limit", took)
	}
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != exitCodexStalled {
		t.Errorf("crew codex returned %v; want exit %d for a stall", err, exitCodexStalled)
	}
}

func TestWithoutMCPServersKeepsTheRest(t *testing.T) {
	config := `model_provider = "proxy"
mcp_servers.inline = { command = "inline" }
projects = {
  "/a" = { trust_level = "trusted" },
}
projects_note = "kept"

[model_providers.proxy]
base_url = "http://proxy.invalid/v1"
projects = "kept: only the root table's keys are dropped"

[mcp_servers.sentry]
command = "sentry"

[[mcp_servers_extra]]
name = "kept"

[mcp_servers.sentry.env]
TOKEN = "x"

[projects."/a"]
trust_level = "trusted"

[tui]
theme = "dark"
`
	want := `model_provider = "proxy"
projects_note = "kept"

[model_providers.proxy]
base_url = "http://proxy.invalid/v1"
projects = "kept: only the root table's keys are dropped"

[[mcp_servers_extra]]
name = "kept"

[tui]
theme = "dark"
`
	if got := string(withoutMCPServers(config)); got != want {
		t.Errorf("withoutMCPServers gave:\n%s\nwant:\n%s", got, want)
	}
}
