package main

// What both systems' services share, so it's tested on Linux: how crew install
// renders a unit or a plist for a home, and reading a plist.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// renderUnit is a systemd unit as installed for a crew home other than ~/crew:
// its %h/crew paths name the home, and its commented CREW_HOME line is set.
// systemd expands the rest of %h itself.
func renderUnit(path, crewHome string) []byte {
	return renderLines(path, func(l string) string {
		if strings.HasPrefix(l, "# Environment=CREW_HOME=") {
			return "Environment=CREW_HOME=" + crewHome
		}
		return strings.ReplaceAll(l, "%h/crew", crewHome)
	})
}

// renderPlist is a launchd job as installed. launchd expands nothing, so its
// %h/crew paths name the crew home, and every other %h the user's home. The
// replacer tries %h/crew first, as it's listed first.
func renderPlist(path, crewHome, userHome string) []byte {
	b, _ := os.ReadFile(path)
	return []byte(strings.NewReplacer("%h/crew", xmlEscape(crewHome), "%h", xmlEscape(userHome)).Replace(string(b)))
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// readPlist reads an XML property list whose top value is a dict. A dict comes
// back as a map[string]any, an array as []any, a string as a string, an integer
// as an int, and true and false as bools. It fails on XML that isn't well
// formed, and on anything else.
func readPlist(b []byte) (map[string]any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	start, err := nextElement(d)
	if err != nil {
		return nil, err
	}
	if start.Name.Local != "plist" {
		return nil, fmt.Errorf("<%s> where <plist> should be", start.Name.Local)
	}
	v, err := plistValue(d)
	if err != nil {
		return nil, err
	}
	dict, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the plist holds a %T, not a dict", v)
	}
	if _, err := nextElement(d); err != errEnd {
		return nil, fmt.Errorf("more than one value in <plist>")
	}
	// The rest must be well formed too.
	for {
		if _, err := d.Token(); err == io.EOF {
			return dict, nil
		} else if err != nil {
			return nil, err
		}
	}
}

// errEnd is nextElement's answer at the end of the enclosing element.
var errEnd = errors.New("end of element")

// nextElement is the next start element, past white space, comments and
// declarations; errEnd at an end element.
func nextElement(d *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.EndElement:
			return xml.StartElement{}, errEnd
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return xml.StartElement{}, fmt.Errorf("text %q outside an element", bytes.TrimSpace(t))
			}
		}
	}
}

// plistValue reads the next value of a dict or an array, or errEnd at its end.
func plistValue(d *xml.Decoder) (any, error) {
	start, err := nextElement(d)
	if err != nil {
		return nil, err
	}
	switch start.Name.Local {
	case "dict":
		dict := map[string]any{}
		for {
			key, err := nextElement(d)
			if err == errEnd {
				return dict, nil
			}
			if err != nil {
				return nil, err
			}
			if key.Name.Local != "key" {
				return nil, fmt.Errorf("<%s> where a dict's <key> should be", key.Name.Local)
			}
			var k string
			if err := d.DecodeElement(&k, &key); err != nil {
				return nil, err
			}
			v, err := plistValue(d)
			if err == errEnd {
				return nil, fmt.Errorf("the dict's key %s has no value", k)
			}
			if err != nil {
				return nil, err
			}
			dict[k] = v
		}
	case "array":
		list := []any{}
		for {
			v, err := plistValue(d)
			if err == errEnd {
				return list, nil
			}
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case "string", "integer":
		var s string
		if err := d.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		if start.Name.Local == "string" {
			return s, nil
		}
		return strconv.Atoi(strings.TrimSpace(s))
	case "true", "false":
		return start.Name.Local == "true", d.Skip()
	}
	return nil, fmt.Errorf("<%s> isn't a plist value crew reads", start.Name.Local)
}
