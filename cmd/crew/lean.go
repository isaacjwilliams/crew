package main

// What crew's sessions load of your own Claude Code setup. Without a word from
// crew, a lead, goal lead or worker would load everything your own sessions do:
// the plugins your settings enable, your personal skills and those synced from
// claude.ai, the skills Claude Code ships, your user-scope MCP servers and your
// claude.ai connectors. Most of it is no use to the role, and all of it takes
// room in every request the session makes. leanSettings turns off what a role
// doesn't use, in the session's own --settings: nothing under ~/.claude changes,
// so your own sessions load what they always did.
//
// What a session gets from crew and its project is never turned off: the home's
// skills, the repo's own agents, skills, plugins and servers, and the project's
// servers from its mcp.json. Claude Code turns things off by name, so crew keeps
// anything of yours that shares a name with one of those (theirs). A project
// keeps more of yours with KEEP_LOADED. Built-in
// tools and agent types a role doesn't use are denied in its agent file and its
// settings file, which don't depend on what you've installed.

import (
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// bundledSkills are the skills Claude Code ships (as of 2.1.328), each with the
// roles that use it; nil means no role does. Every other crew session has it
// off, and a role keeps a skill of yours by the name of one it uses. A skill
// Claude Code adds later stays on until it's listed here.
var bundledSkills = map[string][]string{
	// The implementer's exhale and its security review.
	"simplify":        {"implementer"},
	"security-review": {"implementer"},
	// Only leads publish artifacts, so only they keep the Artifact tool too
	// (enableArtifact in leanSettings).
	"artifact-capabilities": {"lead"},
	"artifact-design":       {"lead"},
	"artifact-diagramming":  {"lead"},
	"dataviz":               {"lead"},
	// No role uses these.
	"claude-api":               nil,
	"code-review":              nil,
	"fewer-permission-prompts": nil,
	"init":                     nil,
	"keybindings-help":         nil,
	"loop":                     nil,
	"plugin-authoring":         nil,
	"run":                      nil,
	"schedule":                 nil,
	"update-config":            nil,
	"workflow-authoring":       nil,
}

// connectorsKeep is the KEEP_LOADED name that keeps the claude.ai connectors.
// crew can only turn them off all together: it has no list of them.
const connectorsKeep = "claude.ai"

// leanSettings is settings, the --settings of a session of role, with what
// turns off everything of yours the role doesn't use and keep doesn't name.
// It only adds (addOnly): a value settings already has wins, and a list or map
// settings already has gains these entries.
func leanSettings(settings *object, role string, keep []string) *object {
	kept := func(name string) bool { return slices.Contains(keep, name) }
	yours := readYourSetup()
	lean := newObject()

	plugins := newObject()
	for _, id := range yours.plugins {
		name, _, _ := strings.Cut(id, "@")
		if !kept(id) && !kept(name) {
			plugins.set(id, false)
		}
	}
	off := slices.Concat(yours.skills, slices.Collect(maps.Keys(bundledSkills)))
	slices.Sort(off)
	skills := newObject()
	for _, skill := range slices.Compact(off) {
		if !kept(skill) && !slices.Contains(bundledSkills[skill], role) {
			skills.set(skill, "off")
		}
	}
	servers := []any{}
	for _, name := range yours.servers {
		if !kept(name) {
			server := newObject()
			server.set("serverName", name)
			servers = append(servers, server)
		}
	}
	if len(plugins.keys) > 0 {
		lean.set("enabledPlugins", plugins)
	}
	if len(skills.keys) > 0 {
		lean.set("skillOverrides", skills)
	}
	if len(servers) > 0 {
		lean.set("deniedMcpServers", servers)
	}
	if !kept(connectorsKeep) {
		lean.set("disableClaudeAiConnectors", true)
	}
	if role != "lead" { // see bundledSkills' artifact skills
		lean.set("enableArtifact", false)
	}
	return addOnly(settings, lean).(*object)
}

// yourSetup is what of your own Claude Code setup crew can turn off by name.
type yourSetup struct {
	plugins []string // the plugins ~/.claude/settings.json enables, by id
	skills  []string // your personal skills and those synced from claude.ai
	servers []string // the MCP servers ~/.claude.json gives every project
}

// readYourSetup reads your setup from ~/.claude and ~/.claude.json, the files
// your own sessions start from (both in CLAUDE_CONFIG_DIR, when it's set). A
// file that's missing or broken has nothing in it: Claude Code reports a broken
// one itself.
func readYourSetup() yourSetup {
	dir := claudeConfigDir()
	var yours yourSetup
	yours.plugins = enabledPlugins(filepath.Join(dir, "settings.json"))
	slices.Sort(yours.plugins)
	// Personal skills are folders in ~/.claude/skills; synced ones sit two folders
	// down in its synced/.
	skills := filepath.Join(dir, "skills")
	yours.skills = append(skillsIn(skills), skillsIn(filepath.Join(skills, "synced", "*"))...)
	if config, _ := readSettings(claudeGlobalConfig()); config != nil {
		if servers, ok := config.vals["mcpServers"].(*object); ok {
			yours.servers = slices.Sorted(slices.Values(servers.keys))
		}
	}
	return yours
}

// keepLoaded is what a worker or goal lead for p keeps of yours: what the
// project's KEEP_LOADED names, and what shares a name with what the session gets
// from crew, the project and the repo. The project's servers come from its
// mcp.json, since the launch writes them to mcp.local.json only after this.
func (c *Crew) keepLoaded(p *Project) []string {
	keep := strings.Fields(p.Settings["KEEP_LOADED"])
	var servers []string
	if mcp, err := c.projectMCPServers(p.Name, p.Settings); err == nil {
		servers = mcp.keys
	}
	return append(keep, c.theirs(servers, p.Repo())...)
}

// theirs is the names of what a session gets from crew and from repo, the repo
// it works in ("" for a lead, which has none): crew's skills, the servers crew
// gives it, and the repo's skills, the plugins its settings enable, and its
// .mcp.json and local-scope servers. Claude Code turns a plugin, skill or server
// off by its name, so turning off one of yours by one of these names would turn
// theirs off too.
func (c *Crew) theirs(servers []string, repo string) []string {
	names := slices.Concat(skillsOf(c.path(".claude")), servers)
	if repo != "" {
		names = append(names, skillsOf(filepath.Join(repo, ".claude"))...)
		for _, f := range []string{"settings.json", "settings.local.json"} {
			names = append(names, enabledPlugins(filepath.Join(repo, ".claude", f))...)
		}
		names = append(names, serversIn(filepath.Join(repo, ".mcp.json"))...)
		if config, _ := readSettings(claudeGlobalConfig()); config != nil {
			names = append(names, config.child("projects").child(repo).child("mcpServers").keys...)
		}
	}
	return names
}

// skillsOf is the names of the skills in a .claude folder: those in its skills/,
// and its commands/, whose files Claude Code loads as skills too.
func skillsOf(dir string) []string {
	return append(skillsIn(filepath.Join(dir, "skills")), commandsIn(filepath.Join(dir, "commands"))...)
}

// commandsIn is the names of the command files in dir and its folders, as
// Claude Code names them: a/b.md is the skill a:b.
func commandsIn(dir string) []string {
	var names []string
	filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(f) == ".md" {
			rel, _ := filepath.Rel(dir, strings.TrimSuffix(f, ".md"))
			names = append(names, strings.ReplaceAll(filepath.ToSlash(rel), "/", ":"))
		}
		return nil
	})
	return names
}

// skillsIn is the names of the skills in dir: its folders with a SKILL.md.
func skillsIn(dir string) []string {
	var names []string
	found, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
	for _, f := range found {
		names = append(names, filepath.Base(filepath.Dir(f)))
	}
	return names
}

// serversIn is the names of the servers in the MCP file f, or none when f is
// broken.
func serversIn(f string) []string {
	if mcp, err := readMCPFile(f); err == nil {
		return mcp.keys
	}
	return nil
}

// enabledPlugins is the ids of the plugins the settings file f turns on.
func enabledPlugins(f string) []string {
	var ids []string
	if settings, _ := readSettings(f); settings != nil {
		if enabled, ok := settings.vals["enabledPlugins"].(*object); ok {
			for _, id := range enabled.keys {
				if enabled.vals[id] == true {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}
