package main

import (
	"errors"
	"os/exec"
)

// The tools are named by their full paths: a launchd job's PATH may lack /usr/sbin.

// processes lists this machine's processes with ps, and their working
// directories with lsof, for stopProcessesIn.
func processes() (map[int]process, error) {
	procs, err := ps()
	if err != nil {
		return nil, err
	}
	// lsof exits 1 when it couldn't look at every process, another user's say, and
	// still lists the rest.
	out, err := exec.Command("/usr/sbin/lsof", "-nP", "-a", "-d", "cwd", "-F", "pn").Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, err
	}
	addLsofCwds(procs, string(out))
	return procs, nil
}

// stillRunning is which of pids haven't exited: ps still lists them, and not as
// zombies. When ps fails, they all count as running.
func stillRunning(pids []int) []int {
	procs, err := ps()
	if err != nil {
		return pids
	}
	var left []int
	for _, pid := range pids {
		if p, ok := procs[pid]; ok && !p.zombie {
			left = append(left, pid)
		}
	}
	return left
}

// ps lists the processes, without their working directories.
func ps() (map[int]process, error) {
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=,ppid=,tty=,stat=").Output()
	if err != nil {
		return nil, err
	}
	return parsePs(string(out)), nil
}
