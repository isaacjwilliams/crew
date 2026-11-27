package main

import (
	"slices"
	"testing"
)

func TestParseText(t *testing.T) {
	var why string
	flags := func() map[string]*string { why = ""; return map[string]*string{"--why": &why} }
	text, err := parseText([]string{"two", "--why", "a reason", "words"}, flags(), "use")
	if err != nil || text != "two words" || why != "a reason" {
		t.Errorf("got %q, why %q, err %v; want the words joined and the flag set", text, why, err)
	}
	for _, args := range [][]string{
		{"title", "--project x --source y"}, // flags in one argument, as an unsplit shell variable passes them
		{"title", "--unknown", "value"},
		{"title", "--why"},
	} {
		if _, err := parseText(args, flags(), "use"); err == nil {
			t.Errorf("parseText(%q) succeeded; want a refusal", args)
		}
	}
}

// A switch takes no value: what follows it is one of the other arguments.
func TestParseArgs(t *testing.T) {
	var fork string
	var split, flipped bool
	args, err := parseArgs([]string{"001", "--split", "must fix", "--fork", "abc", "a title"},
		map[string]*string{"--fork": &fork}, map[string]*bool{"--split": &split, "--flipped": &flipped}, "use")
	if err != nil || !slices.Equal(args, []string{"001", "must fix", "a title"}) || fork != "abc" || !split || flipped {
		t.Errorf("got %q, fork %q, split %v, flipped %v, err %v; want the other arguments in order and the flags set", args, fork, split, flipped, err)
	}
	// A word that starts with "--" but isn't one of the flags is a mistake.
	if _, err := parseArgs([]string{"--unknown"}, nil, map[string]*bool{"--split": &split}, "use"); err == nil {
		t.Error("parseArgs took an unknown flag")
	}
}
