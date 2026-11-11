package main

// MCP servers for one project, and crew mcp sync. A project declares its servers
// in projects/<p>/mcp.json, shaped like a repo's .mcp.json, and refers to a
// secret as ${NAME}: a setting from its settings files, which for a secret is its
// local.env. crew writes two git-ignored files from it, which nobody edits by
// hand:
//
//   - projects/<p>/mcp.local.json: the project's servers, each named
//     <p>_<server>, with every ${NAME} replaced by its value. The project's
//     workers and goal leads start with it as --mcp-config.
//   - lead/.mcp.json: every project's servers. Leads run in lead/, so Claude Code
//     loads it as their folder's servers. A lead for one project starts with the
//     settings leadMCPSettings makes, which turn the others off.
//
// Neither is ever deleted. With nothing to hold, it has no servers, so a session
// that started with it still finds a valid file.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// leadMCPFile is the servers every lead loads, in the home.
const leadMCPFile = "lead/.mcp.json"

// serverName is what a server in mcp.json may be called. A project's name is
// [a-z0-9-] (slugify), so the <project>_<server> names crew writes can't clash
// between projects.
var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// settingRef is a ${…} in a string in mcp.json, with what's between the braces.
var settingRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// crew mcp sync
func cmdMCP(c *Crew, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		return usage("crew mcp sync")
	}
	if !c.syncMCP() {
		return exitQuietly(1)
	}
	return nil
}

// syncMCP rebuilds every project's mcp.local.json, then lead/.mcp.json from them.
// It's best-effort: it reports each problem as it goes and carries on, so one
// project's broken mcp.json doesn't keep the others' servers from the leads. That
// project keeps the mcp.local.json it had. It returns whether there were no
// problems, which only crew mcp sync needs: crew install, crew lead and crew
// serve go on either way.
func (c *Crew) syncMCP() bool {
	ok := true
	for _, proj := range c.projects("") {
		s, err := c.projectSettings(proj)
		if err == nil {
			_, err = c.writeProjectMCP(proj, s)
		}
		if err != nil {
			report(err)
			ok = false
		}
	}
	if err := c.writeLeadMCP(); err != nil {
		report(err)
		ok = false
	}
	return ok
}

// refreshMCPConfig rebuilds a project's mcp.local.json, and returns the flags that
// give a fresh session its servers: --mcp-config and that file. Its launchers
// call it before a resume too, which takes no flags, so the file a session
// started with is current when it resumes. Every session gets the file, empty
// while the project has no mcp.json, so servers added later reach it too.
//
// The launchers put these flags before their others: --mcp-config takes more than
// one value, and launchSession puts the prompt right after the last flag.
func (c *Crew) refreshMCPConfig(p *Project) ([]string, error) {
	f, err := c.writeProjectMCP(p.Name, p.Settings)
	if err != nil {
		return nil, err
	}
	return []string{"--mcp-config", f}, nil
}

// projectMCPServers is the servers a project's mcp.json declares, as crew writes
// them: each named <project>_<server>, with every ${NAME} in its strings replaced
// by the project's setting of that name. A project with no mcp.json has none. It
// only reads, so a check can call it.
func (c *Crew) projectMCPServers(proj string, s Settings) (*object, error) {
	f := c.path("projects", proj, "mcp.json")
	servers, err := readMCPFile(f)
	if err != nil {
		return nil, err
	}
	out := newObject()
	for _, name := range servers.keys {
		if !serverName.MatchString(name) {
			return nil, fail("%s: the server name '%s' can only have letters, digits, _ and -", f, name)
		}
		v, err := expandSettings(servers.vals[name], s)
		if err != nil {
			return nil, fail("%s: server '%s': %v", f, name, err)
		}
		out.set(proj+"_"+name, v)
	}
	return out, nil
}

// expandSettings is v with each ${NAME} in its strings replaced by the setting
// NAME. It looks only in the settings, not the environment, so the timer and
// your shell give a session the same servers, as long as the settings files hold
// the values themselves: a $VAR in one is still read from the environment. An
// error names the setting, never a value.
func expandSettings(v any, s Settings) (any, error) {
	switch v := v.(type) {
	case *object:
		out := newObject()
		for _, k := range v.keys {
			x, err := expandSettings(v.vals[k], s)
			if err != nil {
				return nil, err
			}
			out.set(k, x)
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i := range v {
			x, err := expandSettings(v[i], s)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case string:
		return expandString(v, s)
	}
	return v, nil
}

// expandString is str with each ${NAME} replaced by the setting NAME.
func expandString(str string, s Settings) (string, error) {
	var b strings.Builder
	done := 0
	for _, m := range settingRef.FindAllStringSubmatchIndex(str, -1) {
		ref, name := str[m[0]:m[1]], str[m[2]:m[3]]
		value, set := s[name]
		if !isName(name) {
			return "", fmt.Errorf("%s isn't a setting: only ${NAME} is supported", ref)
		} else if !set {
			return "", fmt.Errorf("${%s} isn't set; put it in the project's local.env", name)
		}
		b.WriteString(str[done:m[0]] + value)
		done = m[1]
	}
	b.WriteString(str[done:])
	return b.String(), nil
}

// writeProjectMCP writes a project's mcp.local.json from its mcp.json, and
// returns its path. On an error, it leaves the file as it was.
func (c *Crew) writeProjectMCP(proj string, s Settings) (string, error) {
	servers, err := c.projectMCPServers(proj, s)
	if err != nil {
		return "", err
	}
	f := c.path("projects", proj, "mcp.local.json")
	return f, writeMCPFile(f, servers)
}

// writeLeadMCP writes lead/.mcp.json: the servers of every project's
// mcp.local.json, as they are.
func (c *Crew) writeLeadMCP() error {
	all := newObject()
	for _, proj := range c.projects("") {
		servers, err := readMCPFile(c.path("projects", proj, "mcp.local.json"))
		if err != nil {
			return err
		}
		for _, name := range servers.keys {
			all.set(name, servers.vals[name])
		}
	}
	return writeMCPFile(c.path(leadMCPFile), all)
}

// leadMCPSettings is the settings JSON a lead for one project starts with: every
// server in lead/.mcp.json that isn't in the project's own mcp.local.json,
// switched off. It goes by the lead's file as it is on disk, the one the session
// loads, so a server a sync hasn't removed yet is switched off too.
func (c *Crew) leadMCPSettings(proj string) (string, error) {
	lead, err := readMCPFile(c.path(leadMCPFile))
	if err != nil {
		return "", err
	}
	own, err := readMCPFile(c.path("projects", proj, "mcp.local.json"))
	if err != nil {
		return "", err
	}
	off := []any{}
	for _, name := range lead.keys {
		if _, mine := own.get(name); !mine {
			off = append(off, name)
		}
	}
	o := newObject()
	o.set("disabledMcpjsonServers", off)
	return encodeJSON(o, false), nil
}

// readMCPFile is the servers in a file shaped like .mcp.json: a JSON object whose
// mcpServers is an object. A file that doesn't exist has none.
func readMCPFile(f string) (*object, error) {
	b, err := os.ReadFile(f)
	if errors.Is(err, fs.ErrNotExist) {
		return newObject(), nil
	} else if err != nil {
		return nil, err
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fail("%s isn't valid JSON: %v", f, err)
	}
	if top, ok := v.(*object); ok {
		if servers, ok := top.vals["mcpServers"].(*object); ok {
			return servers, nil
		}
	}
	return nil, fail(`%s must be a JSON object with an "mcpServers" object`, f)
}

// writeMCPFile writes servers to f, shaped like .mcp.json and readable only by
// you, since its values can be secrets. It replaces f in one step, so a session
// starting meanwhile reads the old file or the new one, never part of one. Its
// temporary file is f.<random>, which .gitignore covers too.
func writeMCPFile(f string, servers *object) error {
	top := newObject()
	top.set("mcpServers", servers)
	tmp, err := os.CreateTemp(filepath.Dir(f), filepath.Base(f)+".*") // mode 0600
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(encodeJSON(top, true))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), f)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
