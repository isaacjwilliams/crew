package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// process is what stopProcessesIn needs to know about one. processes() lists
// them: proc_linux.go from /proc, proc_darwin.go from ps and lsof, both with the
// parsers below.
type process struct {
	ppid     int
	cwd      string // its working directory, or "" when it can't be read
	terminal bool   // it has a controlling terminal
	zombie   bool   // it has exited, and waits for its parent to reap it
}

// stopProcessesIn ends the background processes still running in a directory,
// such as a dev server a worker started in a background shell: stopping the
// worker leaves those running. It sends TERM, then KILL to any still running
// after 10 seconds.
func stopProcessesIn(dir string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return
	}
	procs, err := processes()
	if err != nil {
		warn("couldn't list the processes still running in %s, so they're left running: %v", dir, err)
		return
	}
	pids := processesToStop(procs, os.Getpid(), real)
	if len(pids) == 0 {
		return
	}
	fmt.Printf("stopping %d process(es) still running in %s\n", len(pids), dir)
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	var left []int
	for range 20 {
		if left = stillRunning(pids); len(left) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	warn("killing %d process(es) in %s that ignored TERM", len(left), dir)
	for _, pid := range left {
		syscall.Kill(pid, syscall.SIGKILL)
	}
}

// processesToStop is which of procs stopProcessesIn ends: those working in dir
// or below it. A process with a terminal is the user's, such as their shell in a
// worktree, so it's left alone, and so are crew (self) and the processes that
// started it.
func processesToStop(procs map[int]process, self int, dir string) []int {
	mine := map[int]bool{}
	for pid := self; pid > 0 && !mine[pid]; pid = procs[pid].ppid {
		mine[pid] = true
	}
	var pids []int
	for pid, p := range procs {
		if !mine[pid] && !p.terminal && !p.zombie && p.cwd != "" && under(p.cwd, dir) {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// parseProcStat reads a Linux /proc/<pid>/stat. The 2nd field, the command
// name, is in parentheses and may hold spaces, so the fields are counted from
// its end: the state (Z for a zombie), the parent, the process group, the
// session, and the terminal (0 for none).
func parseProcStat(stat string) (process, bool) {
	i := strings.LastIndex(stat, ") ")
	if i < 0 {
		return process{}, false
	}
	f := strings.Fields(stat[i+2:])
	if len(f) < 5 {
		return process{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return process{}, false
	}
	return process{ppid: ppid, terminal: f[4] != "0", zombie: f[0] == "Z"}, true
}

// parsePs reads `ps -A -o pid=,ppid=,tty=,stat=` on macOS: one process a line,
// its terminal ?? when it has none, and its state starting with Z when it's a
// zombie.
func parsePs(out string) map[int]process {
	procs := map[int]process{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs[pid] = process{ppid: ppid, terminal: f[2] != "??", zombie: strings.HasPrefix(f[3], "Z")}
	}
	return procs
}

// addLsofCwds reads `lsof -a -d cwd -F pn` into procs: a line p<pid> starts a
// process, and n<path> names its working directory. lsof writes other fields
// too (f, the descriptor, always), which are skipped. A process ps didn't list
// has started since, and is skipped too.
func addLsofCwds(procs map[int]process, out string) {
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if p, ok := procs[pid]; ok {
				p.cwd = line[1:]
				procs[pid] = p
			}
		}
	}
}
