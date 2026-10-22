package main

// crew's locks are files in the home, taken with flock(2), the same lock
// util-linux flock takes:
//
//	.crew.lock       every change to the ledger (lock)
//	.crew-tick.lock  one crew tick at a time
//	.crew-push.lock  one ledger push at a time
//
// A lock is held by its open file. Go opens files close-on-exec, so the sessions
// and processes crew starts never hold one.

import (
	"errors"
	"os"
	"syscall"
	"time"
)

var errLocked = errors.New("locked")

// flock opens path and takes an exclusive lock on it, trying for up to wait (or
// once, when wait is 0). It returns errLocked when another process holds it.
func flock(path string, wait time.Duration) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, errLocked
			}
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// takeLock takes the ledger lock until unlock or the end of the process. Every
// change to the ledger takes it through lock, which also checks that this machine
// runs the crew; takeLock alone is for the commands that move the lease.
func (c *Crew) takeLock() error {
	if c.ledgerLock != nil {
		return nil
	}
	f, err := flock(c.path(".crew.lock"), 30*time.Second)
	if err != nil {
		return fail("ledger is locked by another crew command")
	}
	c.ledgerLock = f
	return nil
}

func (c *Crew) lock() error {
	if err := c.takeLock(); err != nil {
		return err
	}
	return c.requireHost()
}

func (c *Crew) unlock() {
	if c.ledgerLock != nil {
		c.ledgerLock.Close()
		c.ledgerLock = nil
	}
}
