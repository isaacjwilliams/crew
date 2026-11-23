package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Another machine ran the crew from each commit that named it in the host file
// until the file next changed, or until now. Stretches when this machine ran
// it, or none did, aren't tenures elsewhere.
func TestTenuresElsewhereAreTheHostFilesOtherHolders(t *testing.T) {
	home := t.TempDir()
	runIn(t, home, "git", "init", "-q")
	claim := func(holder, at string) {
		if err := os.WriteFile(filepath.Join(home, "host"), []byte(holder+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runIn(t, home, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "host: "+holder, "--date", at, "--", "host")
	}
	runIn(t, home, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(home, "host"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, home, "git", "add", "host")
	claim("laptop", "2026-11-01T09:00:00Z")
	claim("desk", "2026-11-05T09:00:00Z")
	claim("", "2026-11-10T09:00:00Z")
	claim("laptop", "2026-11-12T09:00:00Z")

	began := time.Now()
	got, err := (&Crew{Home: home, Host: "desk"}).tenuresElsewhere()
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time {
		at, _ := time.Parse(time.RFC3339, s)
		return at
	}
	if len(got) != 2 {
		t.Fatalf("tenures elsewhere %v; want the laptop's two", got)
	}
	if g := got[0]; g.holder != "laptop" || !g.from.Equal(day("2026-11-01T09:00:00Z")) || !g.to.Equal(day("2026-11-05T09:00:00Z")) {
		t.Errorf("first tenure %v; want the laptop's, from 2026-11-01 09:00 until the desk took over on 2026-11-05 09:00", g)
	}
	if g := got[1]; g.holder != "laptop" || !g.from.Equal(day("2026-11-12T09:00:00Z")) || g.to.Before(began) {
		t.Errorf("second tenure %v; want the laptop's, from 2026-11-12 09:00 until now", g)
	}
}
