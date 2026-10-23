package main

// crew install puts this machine's files in place: the agents in ~/.claude/agents/,
// the fish files in ~/.config/fish/, and the crew's services, installed but not
// enabled (services_linux.go and services_darwin.go). Re-running it is safe. A
// file is replaced only when it differs, after a backup, and by rename, so a
// running crew-guard hook never reads half a file.
// A file an earlier install shipped and this one doesn't is backed up and
// removed. The manifest of what was shipped, and the backups, live in
// ~/.local/share/agent-crew/.

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The tools crew runs: what crew doctor checks, here and on the PATH of the
// enabled services.
var runtimeTools = []string{"git", "gh", "claude", "curl"}

const minClaude = "2.1.281"

// installer is one run of crew install.
type installer struct {
	*Crew
	home    string // the user's home directory
	state   string // ~/.local/share/agent-crew
	backups string // this run's backup directory
	old     []string
	shipped map[string]bool
	newRows []string
	pruned  map[string]bool
}

func cmdInstall(c *Crew, args []string) error {
	yes, fish, services, askAgentsMD := false, true, true, true
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--no-fish":
			fish = false
		case "--no-services":
			services = false
		case "--skip-agents-md-setting":
			askAgentsMD = false
		default:
			return usage("crew install [--yes] [--no-fish] [--no-services] [--skip-agents-md-setting]")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	in := &installer{Crew: c, home: home, state: filepath.Join(home, ".local/share/agent-crew"),
		shipped: map[string]bool{}, pruned: map[string]bool{}}
	in.backups = filepath.Join(in.state, "backups", time.Now().Format("20060102-150405"))
	if b, err := os.ReadFile(filepath.Join(in.state, "manifest")); err == nil {
		in.old = splitLines(string(b))
	}
	// The fish files name ~/crew, the usual home. Only a home elsewhere needs its
	// path written into them.
	custom := c.Home != filepath.Join(home, "crew")

	say("Checking dependencies")
	var missing []string
	for _, t := range append(runtimeTools, "fish") {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		note("missing: %s", strings.Join(missing, " "))
		note("%s", installTools)
		note("Claude Code: curl -fsSL https://claude.ai/install.sh | bash   (or 'claude update')")
	}
	if v, ok := claudeVersion(); ok {
		if !versionAtLeast(v, minClaude) {
			note("Claude Code %s is older than %s; run 'claude update' (AGENTS.md loading and agent view need it)", v, minClaude)
		} else {
			note("Claude Code %s", v)
		}
	}

	say("Installing agent profiles → ~/.claude/agents/")
	agents, _ := filepath.Glob(c.path("agents", "*.md"))
	for _, f := range agents {
		if err := in.install("agents", f, filepath.Join(home, ".claude/agents", filepath.Base(f))); err != nil {
			return err
		}
		note("%s", strings.TrimSuffix(filepath.Base(f), ".md"))
	}
	in.prune("agents", filepath.Join(home, ".claude/agents"), nil)

	// Hooks find crew-guard through CREW_HOME, and so do sessions that run crew.
	say("Crew home: %s", c.Home)
	in.mergeSettings(func(o *object) { o.child("env").set("CREW_HOME", c.Home) })
	os.MkdirAll(c.Settings["WORK_ROOT"], 0o755)
	in.writeLeadLocalSettings()

	if fish {
		say("Installing fish integration → ~/.config/fish/")
		var files []string
		filepath.WalkDir(c.path("fish"), func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			rel, _ := filepath.Rel(c.path("fish"), f)
			var content []byte
			if rel == "conf.d/agent-crew.fish" && custom {
				content = renderLines(f, func(l string) string {
					if strings.HasPrefix(l, "set -q CREW_HOME; or set -gx CREW_HOME ") {
						return "set -q CREW_HOME; or set -gx CREW_HOME " + c.Home
					}
					return l
				})
			}
			if err := in.installContent("fish", f, content, filepath.Join(home, ".config/fish", rel)); err != nil {
				return err
			}
		}
		in.prune("fish", filepath.Join(home, ".config/fish"), nil)
		note("open a new fish shell (or: source ~/.config/fish/conf.d/agent-crew.fish)")
	}

	if services {
		if err := in.installServices(); err != nil {
			return err
		}
		var off []string
		for _, s := range crewServices {
			if !serviceEnabled(s) {
				off = append(off, s)
			}
		}
		if len(off) > 0 {
			note("off on this machine: %s. crew host up turns them on, to run the crew here", strings.Join(off, " "))
		}
	}

	if err := in.writeManifest(); err != nil {
		return err
	}

	// Asked once: not when the setting is on already, or when the user declined before.
	declined := filepath.Join(in.state, "agents-md-declined")
	if askAgentsMD && !exists(declined) && !agentsMDOn(home) {
		say("AGENTS.md loading")
		note("Claude Code reads AGENTS.md automatically, but only when a repo has no CLAUDE.md.")
		if yes || confirm("In repos that have both, load CLAUDE.md and AGENTS.md together?") {
			in.mergeSettings(func(o *object) {
				o.child("pluginConfigs").child("agents-md@builtin").child("options").set("instructionFiles", "claude-md-and-agents-md")
			})
		} else {
			os.MkdirAll(in.state, 0o755)
			os.WriteFile(declined, nil, 0o644)
			note("skipped, and won't ask again; see SETUP.md if you change your mind")
		}
	}
	say("Done (crew %s)", version)
	return nil
}

// claudeVersion is what `claude --version` says, if claude is installed.
func claudeVersion() (string, bool) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", false
	}
	out, _ := exec.Command("claude", "--version").Output()
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", true
	}
	return f[0], true
}

// versionAtLeast compares dotted version numbers: 2.1.290 is at least 2.1.281.
func versionAtLeast(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			return x > y
		}
	}
	return true
}

// install ships src to dst, and records it in the manifest.
func (in *installer) install(component, src, dst string) error {
	return in.installContent(component, src, nil, dst)
}

// installContent ships content (or src's own, when it's nil) to dst, with src's
// mode: replacing dst by rename, after backing it up, when it differs.
func (in *installer) installContent(component, src string, content []byte, dst string) error {
	if content == nil {
		var err error
		if content, err = os.ReadFile(src); err != nil {
			return err
		}
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(src); err == nil && fi.Mode()&0o111 != 0 {
		mode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in.newRows = append(in.newRows, component+"\t"+dst)
	in.shipped[dst] = true
	if cur, err := os.ReadFile(dst); err == nil && isFile(dst) {
		if bytes.Equal(cur, content) {
			return os.Chmod(dst, mode)
		}
		in.backup(dst)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".")
	if err != nil {
		return err
	}
	tmp.Write(content)
	tmp.Close()
	os.Chmod(tmp.Name(), mode)
	return os.Rename(tmp.Name(), dst)
}

// backup copies a file into this run's backup directory, at its path under the
// user's home.
func (in *installer) backup(path string) {
	rel := strings.TrimPrefix(path, in.home+"/")
	dst := filepath.Join(in.backups, rel)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if exec.Command("cp", "-p", path, dst).Run() != nil {
		warn("couldn't back up %s", path)
	}
	note("backed up ~/%s → %s/", rel, in.backups)
}

// prune removes the files the last install of a component shipped and this one
// didn't, running beforeRemove (when there is one) on each first. Run it after the
// component's installs. It only touches files under root, the component's
// directory. A component this run skips (--no-fish, --no-services) is never
// pruned, so it loses nothing.
func (in *installer) prune(component, root string, beforeRemove func(path string)) {
	in.pruned[component] = true
	for _, row := range in.old {
		comp, path, _ := strings.Cut(row, "\t")
		if comp != component || !strings.HasPrefix(path, root+"/") || !isFile(path) || in.shipped[path] {
			continue
		}
		if beforeRemove != nil {
			beforeRemove(path)
		}
		in.backup(path)
		os.Remove(path)
		note("removed %s (no longer shipped)", tilde(in.home, path))
	}
}

func tilde(home, path string) string {
	if rest, ok := strings.CutPrefix(path, home); ok {
		return "~" + rest
	}
	return path
}

// writeManifest records a "component<TAB>path" line for every file shipped: by
// this run, or by the last run of a component this one skipped.
func (in *installer) writeManifest() error {
	var b strings.Builder
	for _, row := range in.old {
		comp, _, _ := strings.Cut(row, "\t")
		if !in.pruned[comp] {
			b.WriteString(row + "\n")
		}
	}
	for _, row := range in.newRows {
		b.WriteString(row + "\n")
	}
	if err := os.MkdirAll(in.state, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(in.state, "manifest.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(in.state, "manifest"))
}

// renderLines is a file with each line changed by edit: what's installed when the
// home isn't ~/crew, so an unchanged re-run compares the same content and backs
// nothing up.
func renderLines(path string, edit func(string) string) []byte {
	b, _ := os.ReadFile(path)
	var out strings.Builder
	for _, l := range splitLines(string(b)) {
		out.WriteString(edit(l) + "\n")
	}
	return []byte(out.String())
}

// mergeSettings changes ~/.claude/settings.json, after a backup, keeping its
// keys' order. A file crew can't read is left alone.
func (in *installer) mergeSettings(change func(*object)) {
	f := filepath.Join(in.home, ".claude/settings.json")
	os.MkdirAll(filepath.Dir(f), 0o755)
	if !exists(f) {
		os.WriteFile(f, []byte("{}\n"), 0o644)
	}
	cur, err := os.ReadFile(f)
	var v any
	if err == nil {
		v, err = parseJSON(cur)
	}
	o, ok := v.(*object)
	if err != nil || !ok {
		note("couldn't parse ~/.claude/settings.json; leaving it alone")
		return
	}
	change(o)
	if out := encodeJSON(o, true); out != string(cur) {
		in.backup(f)
		os.WriteFile(f, []byte(out), 0o644)
		note("updated ~/.claude/settings.json")
	}
}

// agentsMDOn: Claude Code loads AGENTS.md alongside CLAUDE.md.
func agentsMDOn(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, ".claude/settings.json"))
	if err != nil {
		return false
	}
	v, err := parseJSON(b)
	if err != nil {
		return false
	}
	for _, k := range []string{"pluginConfigs", "agents-md@builtin", "options", "instructionFiles"} {
		o, ok := v.(*object)
		if !ok {
			return false
		}
		if v, ok = o.get(k); !ok {
			return false
		}
	}
	return equalJSON(v, "claude-md-and-agents-md")
}

// writeLeadLocalSettings writes the lead's settings for this machine, in the home
// but gitignored: it reads the ledger and the project checkouts as extra
// directories, and leaves out the home's CLAUDE.md, which is for developing crew.
// It's rewritten only when it changes.
func (in *installer) writeLeadLocalSettings() {
	f := in.path("lead/.claude/settings.local.json")
	o := newObject()
	o.child("permissions").set("additionalDirectories", []any{in.Home, in.Settings["WORK_ROOT"]})
	o.set("claudeMdExcludes", []any{in.Home + "/CLAUDE.md"})
	out := encodeJSON(o, true)
	if cur, err := os.ReadFile(f); err == nil && string(cur) == out {
		return
	}
	os.MkdirAll(filepath.Dir(f), 0o755)
	tmp := f + ".tmp"
	os.WriteFile(tmp, []byte(out), 0o644)
	os.Rename(tmp, f)
	note("wrote %s", tilde(in.home, f))
}

// confirm asks a yes-or-no question, yes unless the user answers no. Without a
// terminal it reads the answer from stdin, and no answer is yes.
func confirm(question string) bool {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintf(os.Stderr, "  %s [Y/n] ", question)
	}
	a, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a = strings.TrimSpace(a)
	return a == "" || regexp.MustCompile(`^[Yy]`).MatchString(a)
}
