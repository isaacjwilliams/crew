package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// stopProcessesIn ends the background processes still running in a directory,
// such as a dev server a worker started in a background shell: stopping the
// worker leaves those running. It sends TERM, then KILL to any still running
// after 10 seconds. A process with a terminal is the user's, so it's left alone,
// and so are crew and the processes that started it. It reads /proc.
func stopProcessesIn(dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	mine := map[string]bool{}
	for pid := fmt.Sprint(os.Getpid()); pid != "" && pid != "0"; pid = parentPID(pid) {
		mine[pid] = true
	}
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid := e.Name()
		if strings.Trim(pid, "0123456789") != "" || mine[pid] {
			continue
		}
		cwd, err := os.Readlink("/proc/" + pid + "/cwd")
		if err != nil || (cwd != real && !strings.HasPrefix(cwd, real+"/")) {
			continue
		}
		if noTerminal(pid) {
			var n int
			fmt.Sscan(pid, &n)
			pids = append(pids, n)
		}
	}
	if len(pids) == 0 {
		return nil
	}
	fmt.Printf("stopping %d process(es) still running in %s\n", len(pids), dir)
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	var left []int
	for range 20 {
		left = nil
		// A process that has exited has no cwd, even before its parent reaps it.
		for _, pid := range pids {
			if _, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
				left = append(left, pid)
			}
		}
		if len(left) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	warn("killing %d process(es) in %s that ignored TERM", len(left), dir)
	for _, pid := range left {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

func parentPID(pid string) string {
	b, _ := os.ReadFile("/proc/" + pid + "/status")
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PPid:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// noTerminal: the process has no controlling terminal. That's the 7th field of
// its stat; the 2nd, the command name, may hold spaces, so it's counted from the
// end of that.
func noTerminal(pid string) bool {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return false
	}
	i := strings.LastIndex(string(b), ") ")
	if i < 0 {
		return false
	}
	f := strings.Fields(string(b)[i+2:])
	return len(f) > 4 && f[4] == "0"
}
