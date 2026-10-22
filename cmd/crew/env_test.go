package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvLine(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("FROM_ENV", "env")
	vars := Settings{"EARLIER": "earlier"}
	for line, want := range map[string]string{
		`KEY=value`:                     "value",
		`KEY="two words"`:               "two words",
		`KEY='$NOT expanded'`:           "$NOT expanded",
		`KEY="$HOME/agent-work"`:        "/home/me/agent-work",
		`KEY=${HOME}/x`:                 "/home/me/x",
		`KEY="${EARLIER}-$FROM_ENV"`:    "earlier-env",
		`KEY="yes"      # yes | no — a`: "yes",
		`KEY=plain # comment`:           "plain",
		`KEY=a#b`:                       "a#b",
		`KEY=`:                          "",
		`KEY=""`:                        "",
		`KEY=#nothing`:                  "",
		`KEY="say \"hi\" \$5"`:          `say "hi" $5`,
		`KEY="a"'b'c`:                   "abc",
		`KEY=~/agent-work`:              "/home/me/agent-work",
		`  KEY=indented`:                "indented",
	} {
		key, got, err := parseEnvLine(line, vars)
		if err != nil || key != "KEY" || got != want {
			t.Errorf("%s: got %q=%q, %v; want %q", line, key, got, err, want)
		}
	}
	for _, line := range []string{"", "   ", "# a comment", "  # indented comment"} {
		if key, _, err := parseEnvLine(line, vars); key != "" || err != nil {
			t.Errorf("%q isn't blank: %q, %v", line, key, err)
		}
	}
	for _, line := range []string{
		`KEY=$(date)`, "KEY=`date`", `KEY="$(date)"`, `KEY=two words`, `KEY="unterminated`,
		`KEY='unterminated`, `KEY=${HOME:-x}`, `KEY=$UNSET_FOR_TEST`, `export KEY=x`, `KEY = x`,
		`1KEY=x`, `echo hi`, `KEY=a;b`, `KEY=a|b`,
	} {
		if _, _, err := parseEnvLine(line, vars); err == nil {
			t.Errorf("%s: no error", line)
		}
	}
}

func TestReadEnvFileNamesTheLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crew.env")
	os.WriteFile(p, []byte("# settings\nA=1\nB=$(oops)\n"), 0o644)
	err := readEnvFile(p, Settings{})
	if err == nil || !strings.HasPrefix(err.Error(), p+":3: B: ") {
		t.Errorf("error = %v", err)
	}
}

// config/defaults.env reads as bash's `source` reads it.
func TestDefaultsEnv(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	s := Settings{}
	if err := readEnvFile("../../config/defaults.env", s); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"MAX_WORKERS": "3", "MAX_IN_FLIGHT": "5", "BRANCH_PREFIX": "agent/", "WORK_ROOT": "/home/me/agent-work",
		"WORKER_PERMISSION_MODE": "auto", "WORKER_AUTOCOMPACT_WINDOW": "400k", "LEAD_PERMISSION_MODE": "auto",
		"PRE_PR_REVIEW": "yes", "PR_MODE": "draft", "REPLY_TO_THREADS": "yes", "MAX_REVIEW_ROUNDS": "4",
		"WORKTREE_COPY": "", "WORKTREE_TOOL": "git", "GARDENER_LOOKBACK": "14 days ago", "GARDENER_MAX_TURNS": "150",
	} {
		if v, ok := s[k]; !ok || v != want {
			t.Errorf("%s = %q (set: %v), want %q", k, v, ok, want)
		}
	}
	for _, f := range []string{"../../templates/crew.env"} {
		if err := readEnvFile(f, Settings{}); err != nil {
			t.Error(err)
		}
	}
}
