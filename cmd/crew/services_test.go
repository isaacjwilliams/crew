package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// readUnit reads a systemd unit's Key=value lines, and its Environment= ones as
// Environment.NAME.
func readUnit(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unit := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.HasPrefix(l, "#") {
			continue
		}
		if k == "Environment" {
			k, v, _ = strings.Cut(v, "=")
			k = "Environment." + k
		}
		unit[k] = v
	}
	return unit
}

// Each launchd job is its systemd unit's twin: the same command, the unit's PATH
// within its own, the same Nice, and the children left running where the unit
// leaves them. It also starts off, names the home, and logs to the home's logs/.
func TestPlistsAgreeWithUnits(t *testing.T) {
	plists, _ := filepath.Glob("../../launchd/*.plist")
	units, _ := filepath.Glob("../../systemd/*.service")
	if len(plists) == 0 || len(plists) != len(units) {
		t.Fatalf("%d plists for %d services", len(plists), len(units))
	}
	for _, f := range plists {
		label := strings.TrimSuffix(filepath.Base(f), ".plist")
		b, _ := os.ReadFile(f)
		job, err := readPlist(b)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		unit := readUnit(t, "../../systemd/"+label+".service")
		env, _ := job["EnvironmentVariables"].(map[string]any)
		var command []any
		for _, a := range strings.Fields(unit["ExecStart"]) {
			command = append(command, a)
		}
		log := "%h/crew/logs/" + label + ".log"
		var nice any // the unit's Nice, if it has one
		if n, err := strconv.Atoi(unit["Nice"]); err == nil {
			nice = n
		}
		for what, ok := range map[string]bool{
			"Label is the file's name": job["Label"] == label,
			"Disabled":                 job["Disabled"] == true,
			"the unit's command":       reflect.DeepEqual(job["ProgramArguments"], command),
			"CREW_HOME":                env["CREW_HOME"] == "%h/crew",
			"the unit's Nice":          job["Nice"] == nice,
			"AbandonProcessGroup as KillMode=process": (job["AbandonProcessGroup"] == true) == (unit["KillMode"] == "process"),
			"logs in the home's logs/":                job["StandardOutPath"] == log && job["StandardErrorPath"] == log,
		} {
			if !ok {
				t.Errorf("%s: not %s", f, what)
			}
		}
		path, _ := env["PATH"].(string)
		for _, dir := range strings.Split(unit["Environment.PATH"], ":") {
			if !strings.Contains(":"+path+":", ":"+dir+":") {
				t.Errorf("%s: PATH lacks %s, which the unit has", f, dir)
			}
		}
	}
}

// launchd expands nothing, so every %h goes, and a home with a character XML
// escapes still makes a plist.
func TestRenderPlist(t *testing.T) {
	plists, _ := filepath.Glob("../../launchd/*.plist")
	for _, f := range plists {
		out := renderPlist(f, "/Users/a&b/crew home", "/Users/a&b")
		if strings.Contains(string(out), "%h") {
			t.Errorf("%s: %%h left after rendering", f)
		}
		job, err := readPlist(out)
		if err != nil {
			t.Errorf("%s rendered: %v", f, err)
			continue
		}
		env, _ := job["EnvironmentVariables"].(map[string]any)
		args, _ := job["ProgramArguments"].([]any)
		if len(args) == 0 || args[0] != "/Users/a&b/crew home/bin/crew" || env["CREW_HOME"] != "/Users/a&b/crew home" ||
			!strings.HasPrefix(env["PATH"].(string), "/Users/a&b/crew home/bin:/Users/a&b/.local/bin:") {
			t.Errorf("%s rendered: %v, %v", f, args, env)
		}
	}
}

func TestRenderUnit(t *testing.T) {
	out := string(renderUnit("../../systemd/crew-tick.service", "/srv/crew"))
	for _, want := range []string{"\nExecStart=/srv/crew/bin/crew tick\n", "\nEnvironment=CREW_HOME=/srv/crew\n", "Environment=PATH=/srv/crew/bin:%h/.local/bin:"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered unit lacks %q:\n%s", want, out)
		}
	}
}

func TestReadPlist(t *testing.T) {
	got, err := readPlist([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<!-- a comment -->
<dict>
	<key>Label</key><string>a &amp; b</string>
	<key>On</key><true/>
	<key>Off</key><false/>
	<key>Every</key><integer> 180 </integer>
	<key>Args</key><array><string>x</string><dict/></array>
</dict>
</plist>`))
	want := map[string]any{"Label": "a & b", "On": true, "Off": false, "Every": 180, "Args": []any{"x", map[string]any{}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("readPlist = %v, %v; want %v", got, err, want)
	}
	for _, bad := range []string{
		`<plist><dict><key>A</key><string>x</string></dict>`,             // unclosed
		`<plist><dict><key>A</key></dict></plist>`,                       // a key without a value
		`<plist><dict><string>x</string></dict></plist>`,                 // a value without a key
		`<plist><dict><key>A</key><date>2026</date></dict></plist>`,      // a value crew doesn't read
		`<plist><array/></plist>`,                                        // not a dict
		`<plist><dict/><dict/></plist>`,                                  // two values
		`<plist><!-- a -- b --><dict/></plist>`,                          // -- in a comment
		`<plist><dict><key>N</key><integer>ten</integer></dict></plist>`, // not a number
		`<dict/>`, // no <plist>
	} {
		if _, err := readPlist([]byte(bad)); err == nil {
			t.Errorf("readPlist(%s) succeeded", bad)
		}
	}
}
