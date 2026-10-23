package main

import (
	"reflect"
	"testing"
)

// The ps and lsof output here is written from their macOS man pages, not taken
// from a Mac: swap in real output when one is at hand.
const samplePs = `    1     0 ??       Ss
  401     1 ??       S
  402   401 ??       S
  500   402 ??       S
  600     1 ??       Ss
  601   600 ttys001  S+
  700   401 ??       Z
  800     1 ??       S
  900   600 ??       R
`

const sampleLsof = `p1
fcwd
n/
p401
fcwd
n/Users/me/agent-work/acme-app/007
p402
fcwd
n/Users/me/agent-work/acme-app/007/web
p500
fcwd
n/Users/me/agent-work/acme-app/007
p601
fcwd
n/Users/me/agent-work/acme-app/007
p800
fcwd
n/Users/me/agent-work/acme-app/0070
p900
fcwd
n/Users/me/crew
p999
fcwd
n/Users/me/agent-work/acme-app/007
`

func TestParsePsAndLsof(t *testing.T) {
	procs := parsePs(samplePs)
	addLsofCwds(procs, sampleLsof)
	want := map[int]process{
		1:   {ppid: 0, cwd: "/"},
		401: {ppid: 1, cwd: "/Users/me/agent-work/acme-app/007"},
		402: {ppid: 401, cwd: "/Users/me/agent-work/acme-app/007/web"},
		500: {ppid: 402, cwd: "/Users/me/agent-work/acme-app/007"},
		600: {ppid: 1},
		601: {ppid: 600, cwd: "/Users/me/agent-work/acme-app/007", terminal: true},
		700: {ppid: 401, zombie: true},
		800: {ppid: 1, cwd: "/Users/me/agent-work/acme-app/0070"},
		900: {ppid: 600, cwd: "/Users/me/crew"},
	}
	if !reflect.DeepEqual(procs, want) {
		t.Errorf("got  %+v\nwant %+v", procs, want)
	}
}

func TestProcessesToStop(t *testing.T) {
	procs := parsePs(samplePs)
	addLsofCwds(procs, sampleLsof)
	dir := "/Users/me/agent-work/acme-app/007"
	// From outside the worktree: everything in it without a terminal. Not 601 (a
	// terminal), 700 (a zombie), 800 (a directory that only starts the same), or
	// 999 (not listed by ps).
	if got, want := processesToStop(procs, 900, dir), []int{401, 402, 500}; !reflect.DeepEqual(got, want) {
		t.Errorf("from outside: %v, want %v", got, want)
	}
	// crew itself, 500, and the processes that started it, are left alone.
	if got, want := processesToStop(procs, 500, dir), []int(nil); !reflect.DeepEqual(got, want) {
		t.Errorf("from inside: %v, want %v", got, want)
	}
}

func TestParseProcStat(t *testing.T) {
	for stat, want := range map[string]process{
		"4242 (node) S 4200 4242 4242 0 -1 4194560 1":            {ppid: 4200},
		"4243 (my ) dev server) S 1 4243 4243 34817 4243 0":      {ppid: 1, terminal: true},
		"4244 (sh) Z 4242 4242 4242 0 -1 4227084 0 0 0 0 0 0 0 ": {ppid: 4242, zombie: true},
	} {
		if got, ok := parseProcStat(stat); !ok || got != want {
			t.Errorf("parseProcStat(%q) = %+v, %v; want %+v", stat, got, ok, want)
		}
	}
	if _, ok := parseProcStat("4245 (cut"); ok {
		t.Error("a stat without the fields after the name parsed")
	}
}
