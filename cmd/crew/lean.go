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
// skills, the repo's own agents, skills and servers, and the project's servers
// from its mcp.json. A project keeps more of yours with KEEP_LOADED. Built-in
// tools and agent types a role doesn't use are denied in its agent file and its
// settings file, which don't depend on what you've installed.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// bundledSkills are the skills Claude Code ships (as of 2.1.328), each with the
// roles that use it. Every other crew session has it off. A skill Claude Code
// adds later stays on until it's listed here.
var bundledSkills = map[string][]string{
	"simplify":        {"implementer"},
	"security-review": {"implementer"},
	// Only leads publish artifacts.
	"artifact-capabilities":    {"lead"},
	"artifact-design":          {"lead"},
	"artifact-diagramming":     {"lead"},
	"dataviz":                  {"lead"},
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
// Any value settings already has wins.
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
	skills := newObject()
	for skill, roles := range bundledSkills {
		if !slices.Contains(roles, role) {
			yours.skills = append(yours.skills, skill)
		}
	}
	slices.Sort(yours.skills)
	for _, skill := range yours.skills {
		if !kept(skill) {
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
	lean.set("skillOverrides", skills)
	if len(servers) > 0 {
		lean.set("deniedMcpServers", servers)
	}
	if !kept(connectorsKeep) {
		lean.set("disableClaudeAiConnectors", true)
	}
	if role != "lead" {
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
// your own sessions start from. A file that's missing or broken has nothing in
// it: Claude Code reports a broken one itself.
func readYourSetup() yourSetup {
	home, _ := os.UserHomeDir()
	var yours yourSetup
	if settings, _ := readSettings(filepath.Join(home, ".claude", "settings.json")); settings != nil {
		if enabled, ok := settings.vals["enabledPlugins"].(*object); ok {
			for _, id := range enabled.keys {
				if enabled.vals[id] == true {
					yours.plugins = append(yours.plugins, id)
				}
			}
		}
	}
	// Personal skills are folders in ~/.claude/skills; synced ones sit two folders
	// down in its synced/.
	skills := filepath.Join(home, ".claude", "skills")
	personal, _ := filepath.Glob(filepath.Join(skills, "*", "SKILL.md"))
	synced, _ := filepath.Glob(filepath.Join(skills, "synced", "*", "*", "SKILL.md"))
	for _, f := range append(personal, synced...) {
		yours.skills = append(yours.skills, filepath.Base(filepath.Dir(f)))
	}
	if config, _ := readSettings(filepath.Join(home, ".claude.json")); config != nil {
		if servers, ok := config.vals["mcpServers"].(*object); ok {
			yours.servers = slices.Sorted(slices.Values(servers.keys))
		}
	}
	return yours
}

// keepLoaded is what a worker or goal lead for p keeps of yours: what the
// project's KEEP_LOADED names, and any server of yours that shares a name with
// one in the repo's .mcp.json, since Claude Code turns servers off by name and
// would take the repo's with it.
func (p *Project) keepLoaded() []string {
	keep := strings.Fields(p.Settings["KEEP_LOADED"])
	if repo, err := readMCPFile(filepath.Join(p.Repo(), ".mcp.json")); err == nil {
		keep = append(keep, repo.keys...)
	}
	return keep
}

// leadKeeps is what a lead for proj keeps of yours: the project's KEEP_LOADED,
// or each project's, for a lead for every project.
func (c *Crew) leadKeeps(proj string) ([]string, error) {
	projects := []string{proj}
	if proj == "" {
		projects = c.projects("")
	}
	var keep []string
	for _, p := range projects {
		s, err := c.projectSettings(p)
		if err != nil {
			return nil, err
		}
		keep = append(keep, strings.Fields(s["KEEP_LOADED"])...)
	}
	return keep, nil
}
