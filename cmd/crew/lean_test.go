package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeYourSetup gives a fake HOME a setup like the user's own: two plugins on and
// one off, a personal skill, a skill synced from claude.ai and a user-scope MCP
// server.
func fakeYourSetup(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	files := map[string]string{
		".claude/settings.json": `{"enabledPlugins": {"stripe@claude-plugins-official": true,
			"frontend-design@claude-plugins-official": true, "ruby-lsp@claude-plugins-official": false}}`,
		".claude/skills/codex-review/SKILL.md":       "---\nname: codex-review\n---\n",
		".claude/skills/synced/bucket/docx/SKILL.md": "---\nname: docx\n---\n",
		".claude.json": `{"mcpServers": {"chrome-devtools": {"command": "npx"}}}`,
	}
	for name, body := range files {
		f := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// lean is what leanSettings turns off for role, as the names it lists under each key.
type lean struct {
	plugins, skills, servers  []string
	connectorsOff, noArtifact bool
}

func leanOf(t *testing.T, settings *object) lean {
	t.Helper()
	var l lean
	if plugins := settings.child("enabledPlugins"); plugins != nil {
		for _, id := range plugins.keys {
			if plugins.vals[id] == false {
				l.plugins = append(l.plugins, id)
			}
		}
	}
	if skills := settings.child("skillOverrides"); skills != nil {
		for _, name := range skills.keys {
			if skills.vals[name] == "off" {
				l.skills = append(l.skills, name)
			}
		}
	}
	servers, _ := settings.vals["deniedMcpServers"].([]any)
	for _, s := range servers {
		l.servers = append(l.servers, s.(*object).vals["serverName"].(string))
	}
	l.connectorsOff = settings.vals["disableClaudeAiConnectors"] == true
	l.noArtifact = settings.vals["enableArtifact"] == false
	return l
}

func TestLeanSettingsTurnOffWhatAWorkerDoesntUseOfYours(t *testing.T) {
	fakeYourSetup(t)
	got := leanOf(t, leanSettings(newObject(), "implementer", nil))
	if want := []string{"stripe@claude-plugins-official", "frontend-design@claude-plugins-official"}; !slices.Equal(got.plugins, want) {
		t.Errorf("plugins off = %v, want %v: only the ones your settings enable", got.plugins, want)
	}
	for _, skill := range []string{"codex-review", "docx", "claude-api", "artifact-design", "loop"} {
		if !slices.Contains(got.skills, skill) {
			t.Errorf("skill %s is on, want off: skills off = %v", skill, got.skills)
		}
	}
	if !slices.IsSorted(got.skills) {
		t.Errorf("skills off = %v, want them sorted, so the fingerprint doesn't change from launch to launch", got.skills)
	}
	if !slices.Equal(got.servers, []string{"chrome-devtools"}) || !got.connectorsOff || !got.noArtifact {
		t.Errorf("lean = %+v, want chrome-devtools denied, the connectors and Artifact off", got)
	}
}

// Two projects like the user's: rolemodel-ninja-master keeps Stripe and
// chrome-devtools, isaacjwilliams-crew keeps nothing.
func TestAProjectKeepsWhatKeepLoadedNames(t *testing.T) {
	fakeYourSetup(t)
	ninja := &Project{Name: "ninja", Settings: Settings{"KEEP_LOADED": "stripe@claude-plugins-official chrome-devtools", "REPO": t.TempDir()}}
	crew := &Project{Name: "crew", Settings: Settings{"REPO": t.TempDir()}}

	got := leanOf(t, leanSettings(newObject(), "implementer", ninja.keepLoaded()))
	if !slices.Equal(got.plugins, []string{"frontend-design@claude-plugins-official"}) || got.servers != nil {
		t.Errorf("ninja's workers turn off %v and deny %v, want only frontend-design off", got.plugins, got.servers)
	}
	got = leanOf(t, leanSettings(newObject(), "implementer", crew.keepLoaded()))
	if !slices.Contains(got.plugins, "stripe@claude-plugins-official") || !slices.Equal(got.servers, []string{"chrome-devtools"}) {
		t.Errorf("crew's workers keep %+v, want neither Stripe nor chrome-devtools", got)
	}

	// A plugin by its name alone, a skill, and the connectors.
	got = leanOf(t, leanSettings(newObject(), "implementer", []string{"frontend-design", "docx", connectorsKeep}))
	if slices.Contains(got.plugins, "frontend-design@claude-plugins-official") || slices.Contains(got.skills, "docx") || got.connectorsOff {
		t.Errorf("lean = %+v, want frontend-design, docx and the connectors kept", got)
	}
}

func TestAWorkerKeepsAServerOfYoursThatItsRepoAlsoDeclares(t *testing.T) {
	fakeYourSetup(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte(`{"mcpServers": {"chrome-devtools": {"command": "npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Project{Name: "p", Settings: Settings{"REPO": repo}}
	if got := leanOf(t, leanSettings(newObject(), "implementer", p.keepLoaded())); got.servers != nil {
		t.Errorf("denied %v: a server is turned off by name, so the repo's would go too", got.servers)
	}
}

func TestOnlyLeadsKeepArtifact(t *testing.T) {
	fakeYourSetup(t)
	artifactSkills := []string{"artifact-capabilities", "artifact-design", "artifact-diagramming", "dataviz"}
	for _, role := range []string{"lead", "goal-lead", "implementer"} {
		got := leanOf(t, leanSettings(newObject(), role, nil))
		for _, skill := range artifactSkills {
			if slices.Contains(got.skills, skill) != (role != "lead") {
				t.Errorf("%s: skill %s off = %v", role, skill, slices.Contains(got.skills, skill))
			}
		}
		if got.noArtifact != (role != "lead") {
			t.Errorf("%s: Artifact off = %v", role, got.noArtifact)
		}
	}
}

func TestLeanSettingsLeaveAValueTheSettingsAlreadyHave(t *testing.T) {
	fakeYourSetup(t)
	settings, err := parseJSON([]byte(`{"enabledPlugins": {"stripe@claude-plugins-official": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := leanOf(t, leanSettings(settings.(*object), "implementer", nil))
	if !slices.Equal(got.plugins, []string{"frontend-design@claude-plugins-official"}) {
		t.Errorf("plugins off = %v, want Stripe left on, as the settings have it", got.plugins)
	}
}

// What each role's instructions use. The launch must turn none of it off.
var roleUses = map[string]struct{ tools, agents, skills []string }{
	"lead": {
		tools:  []string{"Bash", "Read", "Edit", "Write", "Grep", "Glob", "Skill", "Agent", "WebFetch", "WebSearch", "ToolSearch", "Artifact"},
		agents: []string{"brief-reviewer", "general-purpose", "Explore", "claude-code-guide"},
		skills: []string{"artifact-capabilities", "artifact-design", "artifact-diagramming", "dataviz"},
	},
	"goal-lead": {
		tools:  []string{"Bash", "Read", "Edit", "Write", "Grep", "Glob", "Skill", "Agent", "WebFetch", "WebSearch", "ToolSearch"},
		agents: []string{"brief-reviewer", "general-purpose", "Explore", "claude-code-guide"},
	},
	"implementer": {
		tools:  []string{"Bash", "Read", "Edit", "Write", "Grep", "Glob", "Skill", "Agent", "WebFetch", "WebSearch", "ToolSearch", "TaskCreate", "TaskUpdate"},
		agents: []string{"casual-observer", "reviewer", "general-purpose", "Explore", "claude-code-guide"},
		skills: []string{"pr-review", "simplify", "security-review"},
	},
}

func TestEachRoleKeepsWhatItsInstructionsUse(t *testing.T) {
	fakeYourSetup(t)
	settingsFile := map[string]string{"lead": "lead/.claude/settings.json", "goal-lead": "lead/.claude/settings.json", "implementer": "config/worker-settings.json"}
	for role, uses := range roleUses {
		agent, err := os.ReadFile(filepath.Join("../../agents", role+".md"))
		if err != nil {
			t.Fatal(err)
		}
		var disallowed []string
		for line := range strings.Lines(string(agent)) {
			if list, ok := strings.CutPrefix(line, "disallowedTools:"); ok {
				for tool := range strings.SplitSeq(list, ",") {
					disallowed = append(disallowed, strings.TrimSpace(tool))
				}
			}
		}
		settings, err := readSettings(filepath.Join("../..", settingsFile[role]))
		if err != nil {
			t.Fatal(err)
		}
		var denied []string
		for _, rule := range settings.child("permissions").vals["deny"].([]any) {
			denied = append(denied, rule.(string))
		}
		off := leanOf(t, leanSettings(settings, role, nil))

		for _, tool := range uses.tools {
			if slices.Contains(disallowed, tool) || slices.Contains(denied, tool) || tool == "Artifact" && off.noArtifact {
				t.Errorf("%s uses %s, but its launch turns it off", role, tool)
			}
		}
		for _, agent := range uses.agents {
			if slices.Contains(denied, "Agent("+agent+")") || slices.Contains(disallowed, "Agent("+agent+")") {
				t.Errorf("%s uses the %s agent, but its launch denies it", role, agent)
			}
		}
		for _, skill := range uses.skills {
			if slices.Contains(off.skills, skill) || slices.Contains(denied, "Skill("+skill+")") {
				t.Errorf("%s uses the %s skill, but its launch turns it off", role, skill)
			}
		}
	}
}

// Every role's launch only reads your setup: nothing under ~/.claude, or in
// ~/.claude.json, is written, moved or removed, and no .trash appears.
func TestLaunchingASessionLeavesYourSetupAsItWas(t *testing.T) {
	home := fakeYourSetup(t)
	snapshot := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			info, _ := d.Info()
			files[path] = string(b) + info.ModTime().String()
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()

	c := &Crew{Home: t.TempDir(), Settings: Settings{"LEAD_PERMISSION_MODE": "auto"}}
	for _, f := range []string{"config/worker-settings.json", "lead/.claude/settings.json"} {
		if err := os.MkdirAll(filepath.Dir(c.path(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path(f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &Project{Name: "p", Dir: t.TempDir(), Settings: Settings{"WORKER_AUTOCOMPACT_WINDOW": "400k", "REPO": t.TempDir()}}
	if _, err := c.workerSettings(p); err != nil {
		t.Fatal(err)
	}
	if _, err := c.goalLeadSettings(p); err != nil {
		t.Fatal(err)
	}
	if _, err := c.leadFlags("", c.Settings); err != nil {
		t.Fatal(err)
	}

	after := snapshot()
	for path := range after {
		if strings.Contains(path, ".trash") {
			t.Errorf("%s appeared", path)
		}
	}
	if len(after) != len(before) {
		t.Errorf("your setup has %d files after the launches, %d before", len(after), len(before))
	}
	for path, was := range before {
		if after[path] != was {
			t.Errorf("%s changed", path)
		}
	}
}
