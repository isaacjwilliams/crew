package main

import "testing"

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
