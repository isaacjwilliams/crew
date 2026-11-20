package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A session's since keeps the time zone it was written in. Once the clock's
// zone moves back (a daylight saving change, say), since can fall on a later
// day than the file holding the lines written after it.
func TestNewsSinceReadsTheDayBeforeSinceToo(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	os.MkdirAll(c.path("news"), 0o755)
	line := "2026-10-24T23:45:00Z\tp\t001: status → draft\n"
	if err := os.WriteFile(filepath.Join(c.path("news"), "2026-10-24.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	since, _ := time.Parse(time.RFC3339Nano, "2026-10-25T00:30:00+01:00") // 23:30 UTC on the 24th
	items := c.newsSince(since)
	if len(items) != 1 || items[0].text != "001: status → draft" {
		t.Fatalf("newsSince(%s) = %v, want the line written at 23:45 UTC", since, items)
	}
}

// A line is timed once its writer holds the news lock, so a lead's session that
// read the news meanwhile, and moved its since past then, still hears it.
func TestRecordNewsTimesALineOnceItHoldsTheLock(t *testing.T) {
	c := &Crew{Home: t.TempDir()}
	lock, err := c.lockNews()
	if err != nil {
		t.Fatal(err)
	}
	written := make(chan bool)
	go func() {
		c.recordNews("p", "001: status → pr-open")
		close(written)
	}()
	time.Sleep(200 * time.Millisecond) // the writer is waiting for the lock
	since := time.Now()
	lock.Close()
	<-written
	if items := c.newsSince(since); len(items) != 1 {
		t.Fatalf("newsSince(the lock's release) = %v, want the line written after it", items)
	}
}
