package main

// crew's settings files (config/defaults.env, config/crew.env, a project's
// project.env and local.env) are shell-style, but crew parses them rather than
// running them. A line is blank, a # comment, or KEY=value, where the value is
// one shell word: plain characters, 'single quoted' (taken as written) and
// "double quoted" (where $VAR and ${VAR} are expanded) parts, followed by an
// optional # comment. $VAR and ${VAR} expand unquoted too, and a leading ~ is
// $HOME. Anything else, a command substitution say, is an error that names the
// file and line.

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Settings maps each setting's name to its value.
type Settings map[string]string

func (s Settings) clone() Settings {
	c := make(Settings, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

// readEnvFile adds the settings in path to s. A value may use the settings s
// has so far, and the environment.
func readEnvFile(path string, s Settings) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		key, value, err := parseEnvLine(sc.Text(), s)
		if err != nil {
			return fmt.Errorf("%s:%d: %v", path, n, err)
		}
		if key != "" {
			s[key] = value
		}
	}
	return sc.Err()
}

// parseEnvLine returns the setting a line makes, or no key for a blank line or
// a comment.
func parseEnvLine(line string, s Settings) (key, value string, err error) {
	line = strings.TrimLeft(line, " \t")
	if line == "" || line[0] == '#' {
		return "", "", nil
	}
	eq := strings.IndexByte(line, '=')
	if eq < 1 || !isName(line[:eq]) {
		return "", "", fmt.Errorf("expected KEY=value, got %q", line)
	}
	key = line[:eq]
	p := envParser{rest: line[eq+1:], vars: s}
	value, err = p.word()
	if err != nil {
		return "", "", fmt.Errorf("%s: %v", key, err)
	}
	rest := strings.TrimLeft(p.rest, " \t")
	if rest != "" && rest[0] != '#' {
		return "", "", fmt.Errorf("%s: unexpected %q after the value; quote a value that has spaces", key, rest)
	}
	return key, value, nil
}

func isName(s string) bool {
	for i, c := range s {
		letter := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

type envParser struct {
	rest string
	vars Settings
}

// word reads one shell word, up to unquoted whitespace or the end of the line.
func (p *envParser) word() (string, error) {
	var b strings.Builder
	if strings.HasPrefix(p.rest, "#") { // KEY=#… sets nothing: the rest is a comment
		return "", nil
	}
	if strings.HasPrefix(p.rest, "~") && (len(p.rest) == 1 || p.rest[1] == '/' || p.rest[1] == ' ' || p.rest[1] == '\t') {
		home, err := p.lookup("HOME")
		if err != nil {
			return "", err
		}
		b.WriteString(home)
		p.rest = p.rest[1:]
	}
	for p.rest != "" {
		c := p.rest[0]
		switch {
		case c == ' ' || c == '\t':
			return b.String(), nil
		case c == '\'':
			end := strings.IndexByte(p.rest[1:], '\'')
			if end < 0 {
				return "", fmt.Errorf("no closing ' (a value can't span lines)")
			}
			b.WriteString(p.rest[1 : end+1])
			p.rest = p.rest[end+2:]
		case c == '"':
			p.rest = p.rest[1:]
			if err := p.doubleQuoted(&b); err != nil {
				return "", err
			}
		case c == '$':
			if err := p.variable(&b); err != nil {
				return "", err
			}
		case c == '\\':
			if len(p.rest) < 2 {
				return "", fmt.Errorf("a value can't span lines")
			}
			b.WriteByte(p.rest[1])
			p.rest = p.rest[2:]
		case strings.IndexByte("`;&|<>()", c) >= 0:
			return "", fmt.Errorf("%q isn't supported in a settings file (only quotes, $VAR and ${VAR})", c)
		default:
			b.WriteByte(c)
			p.rest = p.rest[1:]
		}
	}
	return b.String(), nil
}

func (p *envParser) doubleQuoted(b *strings.Builder) error {
	for p.rest != "" {
		c := p.rest[0]
		switch c {
		case '"':
			p.rest = p.rest[1:]
			return nil
		case '$':
			if err := p.variable(b); err != nil {
				return err
			}
		case '`':
			return fmt.Errorf("'`' isn't supported in a settings file (only quotes, $VAR and ${VAR})")
		case '\\':
			if len(p.rest) >= 2 && strings.IndexByte("$`\"\\", p.rest[1]) >= 0 {
				b.WriteByte(p.rest[1])
				p.rest = p.rest[2:]
			} else {
				b.WriteByte(c)
				p.rest = p.rest[1:]
			}
		default:
			b.WriteByte(c)
			p.rest = p.rest[1:]
		}
	}
	return fmt.Errorf(`no closing " (a value can't span lines)`)
}

// variable expands $NAME or ${NAME} at the start of p.rest.
func (p *envParser) variable(b *strings.Builder) error {
	r := p.rest[1:]
	var name string
	if strings.HasPrefix(r, "{") {
		end := strings.IndexByte(r, '}')
		if end < 0 || !isName(r[1:end]) {
			return fmt.Errorf("only ${NAME} is supported in a settings file, not %q", p.rest)
		}
		name, p.rest = r[1:end], r[end+1:]
	} else {
		n := 0
		for n < len(r) && isName(r[:n+1]) {
			n++
		}
		if n == 0 {
			return fmt.Errorf("only $NAME and ${NAME} are supported in a settings file, not %q", p.rest)
		}
		name, p.rest = r[:n], r[n:]
	}
	v, err := p.lookup(name)
	b.WriteString(v)
	return err
}

func (p *envParser) lookup(name string) (string, error) {
	if v, ok := p.vars[name]; ok {
		return v, nil
	}
	if v, ok := os.LookupEnv(name); ok {
		return v, nil
	}
	return "", fmt.Errorf("$%s isn't set", name)
}
