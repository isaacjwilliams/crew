package main

// crew contract checks that the repo it's run in keeps its Contract: every
// obligation in contract/<component>/README.md has a test tagged for it, and
// every tag names an obligation that exists. A line that looks meant as an
// obligation or a tag but isn't in its exact form is a problem too, so a typo
// can't hide an obligation nothing tests. It reads the working tree, needs no
// crew home, and writes nothing. SETUP.md's "The Contract" states the convention.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

var (
	// A list item in a component's README that starts in bold, ** or __, is meant
	// as an obligation, and obligationLine is the one form it may take.
	obligationLike = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(?:\*\*|__)`)
	obligationLine = regexp.MustCompile(`^- \*\*([A-Z]+[0-9]+)\*\* +\S`)
	// A comment line that starts with "Contract:", in any case, is meant as a tag,
	// and tagLine is the one form it may take.
	tagLike       = regexp.MustCompile(`(?i)^\s*(?://+|#+)\s*contract\s*:`)
	tagLine       = regexp.MustCompile(`^\s*(?://|#)\s*Contract:\s*([a-z][a-z0-9-]*/[A-Z]+[0-9]+)\s*$`)
	componentName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// obligation is one promise in a component's Contract, with the files that hold
// a tag for it.
type obligation struct {
	id    string // guard/G7
	where string // contract/guard/README.md:12
	tests []string
}

// contractCheck is what crew contract found in a repo: its components, their
// obligations in the order the READMEs give them, and every way the repo fails
// to keep them.
type contractCheck struct {
	components  map[string]bool
	obligations []*obligation
	byID        map[string]*obligation
	problems    []string
}

func cmdContract(args []string) error {
	if len(args) > 0 {
		return usage("crew contract")
	}
	root, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return fail("crew contract checks a git repo's working tree, and this isn't in one")
	}
	check, err := checkContract(root)
	if err != nil {
		return err
	}
	check.print()
	if n := len(check.problems); n > 0 {
		return fail("the Contract isn't kept: %s", plural(n, "problem"))
	}
	return nil
}

// checkContract reads the Contract in the repo at root, then the tags in its
// files: those git tracks and the new ones it doesn't ignore.
func checkContract(root string) (*contractCheck, error) {
	check := &contractCheck{components: map[string]bool{}, byID: map[string]*obligation{}}
	files, err := repoFiles(root)
	if err != nil {
		return nil, err
	}
	if err := check.readComponents(root, files); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := check.readTags(root, f); err != nil {
			return nil, err
		}
	}
	for _, o := range check.obligations {
		if len(o.tests) == 0 {
			check.problem("%s: %s has no test tagged for it", o.where, o.id)
		}
	}
	return check, nil
}

// readComponents reads the obligations of each component: each directory in
// contract/. A file directly in contract/, such as a README for them all,
// belongs to none.
func (check *contractCheck) readComponents(root string, files []string) error {
	var dirs []string
	for _, f := range files {
		if rel, ok := strings.CutPrefix(f, "contract/"); ok {
			if dir, _, ok := strings.Cut(rel, "/"); ok {
				dirs = append(dirs, dir)
			}
		}
	}
	for _, dir := range slices.Compact(dirs) { // files is sorted, so each directory's files are together
		switch {
		case !componentName.MatchString(dir):
			check.problem("contract/%s: a component's name is lowercase letters, digits and dashes", dir)
		case !slices.Contains(files, "contract/"+dir+"/README.md"):
			check.problem("contract/%s: a component needs a README.md that lists its obligations", dir)
		default:
			check.components[dir] = true
			if err := check.readObligations(root, dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// readObligations adds the obligations in component's README to check, and a
// problem for each line meant as one that isn't in its form.
func (check *contractCheck) readObligations(root, component string) error {
	readme := "contract/" + component + "/README.md"
	lines, err := readTextLines(root, readme)
	if err != nil {
		return err
	}
	for i, line := range lines {
		if !obligationLike.MatchString(line) {
			continue
		}
		where := fmt.Sprintf("%s:%d", readme, i+1)
		m := obligationLine.FindStringSubmatch(line)
		if m == nil {
			check.problem("%s: an obligation is a line \"- **G7** <what it promises>\": a dash, its ID in bold (capital letters, then digits), a sentence", where)
			continue
		}
		id := component + "/" + m[1]
		if first := check.byID[id]; first != nil {
			check.problem("%s: %s is listed twice, here and at %s", where, id, first.where)
			continue
		}
		o := &obligation{id: id, where: where}
		check.byID[id] = o
		check.obligations = append(check.obligations, o)
	}
	return nil
}

// readTags adds file f to the tests of each obligation it holds a tag for, and a
// problem for each line meant as a tag that isn't in its form or names nothing.
func (check *contractCheck) readTags(root, f string) error {
	if ext := strings.ToLower(path.Ext(f)); ext == ".md" || ext == ".markdown" {
		return nil // a "# Contract: …" line there is a heading
	}
	lines, err := readTextLines(root, f)
	if err != nil {
		return err
	}
	for i, line := range lines {
		if !tagLike.MatchString(line) {
			continue
		}
		where := fmt.Sprintf("%s:%d", f, i+1)
		m := tagLine.FindStringSubmatch(line)
		if m == nil {
			check.problem("%s: a tag is one comment naming one obligation, as in \"// Contract: guard/G7\" or \"# Contract: guard/G7\"", where)
			continue
		}
		id := m[1]
		component, _, _ := strings.Cut(id, "/")
		o := check.byID[id]
		switch {
		case !check.components[component]:
			check.problem("%s: the tag names %s, but there's no component %s (no contract/%s/README.md)", where, id, component, component)
		case o == nil:
			check.problem("%s: the tag names %s, which contract/%s/README.md doesn't list", where, id, component)
		case !slices.Contains(o.tests, f):
			o.tests = append(o.tests, f)
		}
	}
	return nil
}

func (check *contractCheck) problem(format string, a ...any) {
	check.problems = append(check.problems, fmt.Sprintf(format, a...))
}

// print lists each obligation with the files tagged for it, then the problems.
func (check *contractCheck) print() {
	if len(check.components) == 0 && len(check.problems) == 0 {
		fmt.Println("No Contract here: no contract/<component>/README.md.")
		return
	}
	width := 0
	for _, o := range check.obligations {
		width = max(width, len(o.id))
	}
	covered := 0
	for _, o := range check.obligations {
		tests := strings.Join(o.tests, ", ")
		if tests == "" {
			tests = "no test"
		} else {
			covered++
		}
		fmt.Printf("%-*s  %s\n", width, o.id, tests)
	}
	fmt.Printf("%d of %d obligations have a test.\n", covered, len(check.obligations))
	for _, p := range check.problems {
		fmt.Println(p)
	}
}

// repoFiles lists the files in the working tree at root that git tracks, and the
// new ones it doesn't ignore, sorted, as paths from root. A path git lists that
// isn't on disk is skipped; any other error reading a file fails the check.
func repoFiles(root string) ([]string, error) {
	out, err := git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate")
	if err != nil {
		return nil, fail("can't list the files in %s: %v", root, err)
	}
	files := strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
	files = slices.DeleteFunc(files, func(f string) bool {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f)))
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	})
	slices.Sort(files) // git lists the tracked files, then the new ones after them
	return files, nil
}

// readTextLines is the lines of the text file at name, from root. A file that
// isn't a regular file (a submodule, say), or holds a NUL byte, as a binary file
// does, has none.
func readTextLines(root, name string) ([]string, error) {
	p := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(p)
	if err != nil {
		return nil, fail("can't read %s: %v", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fail("can't read %s: %v", name, err)
	}
	if slices.Contains(b, 0) {
		return nil, nil
	}
	return splitLines(string(b)), nil
}
