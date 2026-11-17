package main

import "testing"

func TestSlugFor(t *testing.T) {
	for _, c := range []struct{ origin, want string }{
		{"git@github.com:Ninja-Master-Software/pr_evidence.git", "ninja-master-software-pr-evidence"},
		{"https://github.com/isaacjwilliams/crew.git", "isaacjwilliams-crew"},
		{"https://github.com/isaacjwilliams/crew/", "isaacjwilliams-crew"},
		{"ssh://git@github.com/owner/name", "owner-name"},
		{"/srv/git/owner/name.git", "owner-name"},
	} {
		repo := t.TempDir()
		runIn(t, repo, "git", "init", "-q")
		runIn(t, repo, "git", "remote", "add", "origin", c.origin)
		if got := slugFor(repo); got != c.want {
			t.Errorf("slugFor with origin %s is %q, want %q", c.origin, got, c.want)
		}
	}
}
