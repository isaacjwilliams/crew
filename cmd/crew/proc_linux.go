package main

import (
	"fmt"
	"os"
	"strconv"
)

// processes lists this machine's processes from /proc, for stopProcessesIn.
func processes() (map[int]process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	procs := map[int]process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if p, ok := parseProcStat(string(stat)); ok {
			p.cwd, _ = os.Readlink("/proc/" + e.Name() + "/cwd")
			procs[pid] = p
		}
	}
	return procs, nil
}

// stillRunning is which of pids haven't exited. One that has exited has no cwd,
// even before its parent reaps it.
func stillRunning(pids []int) []int {
	var left []int
	for _, pid := range pids {
		if _, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			left = append(left, pid)
		}
	}
	return left
}
