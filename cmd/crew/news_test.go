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
