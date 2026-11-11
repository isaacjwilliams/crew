package main

import (
	"slices"
	"strings"
	"testing"
)

func TestTakeProjectKeepsTheOtherArgumentsInOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		args, rest []string
		project    string
	}{
		"no --project":            {[]string{"-c", "hello"}, []string{"-c", "hello"}, ""},
		"no arguments":            {nil, nil, ""},
		"--project first":         {[]string{"--project", "shop", "-c"}, []string{"-c"}, "shop"},
		"--project in the middle": {[]string{"-c", "--project", "shop", "hello"}, []string{"-c", "hello"}, "shop"},
		"--project last":          {[]string{"hello", "--project", "all"}, []string{"hello"}, "all"},
		"only the first is taken": {[]string{"--project", "a", "--project", "b"}, []string{"--project", "b"}, "a"},
	} {
		project, rest, err := takeProject(tc.args)
		if err != nil || project != tc.project || !slices.Equal(rest, tc.rest) {
			t.Errorf("%s: got %q %q %v, want %q %q", name, project, rest, err, tc.project, tc.rest)
		}
	}
}

func TestTakeProjectRefusesAProjectlessFlag(t *testing.T) {
	_, _, err := takeProject([]string{"-c", "--project"})
	if err == nil || !strings.Contains(err.Error(), "--project needs a project") {
		t.Errorf("got %v", err)
	}
}
