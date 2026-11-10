package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpHome is a crew home with a lead/ folder and the named projects, each with
// a project.env and, for a non-empty one, the given mcp.json.
func mcpHome(t *testing.T, mcpJSON map[string]string) *Crew {
	t.Helper()
	c := &Crew{Home: t.TempDir(), Settings: Settings{}}
	os.MkdirAll(c.path("lead"), 0o755)
	for proj, j := range mcpJSON {
		os.MkdirAll(c.path("projects", proj), 0o755)
		os.WriteFile(c.path("projects", proj, "project.env"), nil, 0o644)
		if j != "" {
			os.WriteFile(c.path("projects", proj, "mcp.json"), []byte(j), 0o644)
		}
	}
	return c
}

func readJSONFile(t *testing.T, f string) string {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatalf("%s: %v", f, err)
	}
	return encodeJSON(v, false)
}

func TestProjectMCPServersNamesAndExpands(t *testing.T) {
	c := mcpHome(t, map[string]string{"shop": `{"mcpServers": {
		"trello": {"type": "http", "url": "https://mcp.trello.com/v1"},
		"tracker": {"command": "npx", "args": ["tracker", "--key=${TRACKER_KEY}"], "env": {"TOKEN": "${TOKEN}", "LEFT": "$TOKEN and ${"}}
	}}`})
	got, err := c.projectMCPServers("shop", Settings{"TRACKER_KEY": "k1", "TOKEN": "t2"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"shop_trello":{"type":"http","url":"https://mcp.trello.com/v1"},` +
		`"shop_tracker":{"command":"npx","args":["tracker","--key=k1"],"env":{"TOKEN":"t2","LEFT":"$TOKEN and ${"}}}`
	if s := encodeJSON(got, false); s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
}

func TestProjectMCPServersRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ mcpJSON, want string }{
		"an unknown setting":       {`{"mcpServers": {"t": {"env": {"K": "${SECRET_KEY}"}}}}`, "server 't': ${SECRET_KEY} isn't set"},
		"a ${…} that's not a name": {`{"mcpServers": {"t": {"url": "${A:-b}"}}}`, "${A:-b} isn't a setting"},
		"a server name with a dot": {`{"mcpServers": {"tre.llo": {}}}`, "the server name 'tre.llo'"},
		"an empty server name":     {`{"mcpServers": {"": {}}}`, "the server name ''"},
		"no mcpServers":            {`{"servers": {}}`, `must be a JSON object with an "mcpServers" object`},
		"mcpServers not an object": {`{"mcpServers": []}`, `must be a JSON object with an "mcpServers" object`},
		"invalid JSON":             {`{"mcpServers": {`, "isn't valid JSON"},
	} {
		c := mcpHome(t, map[string]string{"shop": tc.mcpJSON})
		f := c.path("projects", "shop", "mcp.json")
		_, err := c.projectMCPServers("shop", Settings{"SECRET": "hunter2"})
		if err == nil || !strings.HasPrefix(err.Error(), f) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one naming %s and saying %q", name, err, f, tc.want)
		}
	}
}

func TestExpandSettingsNeverReadsTheEnvironment(t *testing.T) {
	t.Setenv("FROM_SHELL", "x")
	if _, err := expandSettings("${FROM_SHELL}", Settings{}); err == nil {
		t.Error("a variable set only in the environment was expanded")
	}
}

func TestSyncMCPWritesPrivateFilesAndTheLeadUnion(t *testing.T) {
	c := mcpHome(t, map[string]string{
		"acme-app":       `{"mcpServers": {"trello": {"type": "http", "url": "https://a"}}}`,
		"acme-app-admin": `{"mcpServers": {"trello": {"type": "http", "url": "https://b"}, "sentry": {"type": "http", "url": "https://s"}}}`,
		"quiet":          "",
	})
	if !c.syncMCP() {
		t.Fatal("sync reported a problem")
	}
	if got := readJSONFile(t, c.path(leadMCPFile)); got != `{"mcpServers":{"acme-app_trello":{"type":"http","url":"https://a"},`+
		`"acme-app-admin_trello":{"type":"http","url":"https://b"},"acme-app-admin_sentry":{"type":"http","url":"https://s"}}}` {
		t.Errorf("lead/.mcp.json = %s", got)
	}
	if got := readJSONFile(t, c.path("projects", "quiet", "mcp.local.json")); got != `{"mcpServers":{}}` {
		t.Errorf("a project with no mcp.json got %s", got)
	}
	for _, f := range []string{c.path(leadMCPFile), c.path("projects", "acme-app", "mcp.local.json")} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v; want 0600", f, fi.Mode().Perm(), err)
		}
	}
	if left, _ := filepath.Glob(c.path("projects", "*", "mcp.local.json.*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestSyncMCPKeepsGoingPastABrokenProject(t *testing.T) {
	c := mcpHome(t, map[string]string{
		"broken": `{"mcpServers": {"t": {"url": "${NOPE}"}}}`,
		"fine":   `{"mcpServers": {"t": {"url": "https://f"}}}`,
	})
	if c.syncMCP() {
		t.Error("sync reported no problem for an unset setting")
	}
	if got := readJSONFile(t, c.path(leadMCPFile)); got != `{"mcpServers":{"fine_t":{"url":"https://f"}}}` {
		t.Errorf("lead/.mcp.json = %s", got)
	}
}

func TestAProjectThatDropsItsMCPJSONGetsAnEmptyFile(t *testing.T) {
	c := mcpHome(t, map[string]string{"shop": `{"mcpServers": {"t": {"url": "https://t"}}}`})
	c.syncMCP()
	os.Remove(c.path("projects", "shop", "mcp.json"))
	if !c.syncMCP() {
		t.Fatal("sync reported a problem")
	}
	for _, f := range []string{c.path("projects", "shop", "mcp.local.json"), c.path(leadMCPFile)} {
		if got := readJSONFile(t, f); got != `{"mcpServers":{}}` {
			t.Errorf("%s = %s", f, got)
		}
	}
}

func TestLeadMCPSettings(t *testing.T) {
	c := mcpHome(t, map[string]string{
		"acme-app":       `{"mcpServers": {"trello": {"url": "https://a"}}}`,
		"acme-app-admin": `{"mcpServers": {"trello": {"url": "https://b"}, "sentry": {"url": "https://s"}}}`,
		"quiet":          "",
	})
	lead := func(proj string) string {
		t.Helper()
		s, err := c.leadMCPSettings(proj)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := lead("acme-app"); got != `{"disabledMcpjsonServers":[]}` {
		t.Errorf("with no lead/.mcp.json: %s", got)
	}
	c.syncMCP()
	// Names that share a prefix still belong to their own project.
	for proj, want := range map[string]string{
		"acme-app":       `{"disabledMcpjsonServers":["acme-app-admin_trello","acme-app-admin_sentry"]}`,
		"acme-app-admin": `{"disabledMcpjsonServers":["acme-app_trello"]}`,
		"quiet":          `{"disabledMcpjsonServers":["acme-app_trello","acme-app-admin_trello","acme-app-admin_sentry"]}`,
	} {
		if got := lead(proj); got != want {
			t.Errorf("%s: got %s, want %s", proj, got, want)
		}
	}
	// A server still in the lead's file after its project dropped it stays off.
	os.Remove(c.path("projects", "acme-app-admin", "mcp.json"))
	c.writeProjectMCP("acme-app-admin", Settings{})
	if got := lead("acme-app-admin"); got != `{"disabledMcpjsonServers":["acme-app_trello","acme-app-admin_trello","acme-app-admin_sentry"]}` {
		t.Errorf("with a stale lead file: %s", got)
	}
}
