package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// authntestPath is the package that builds the context a gateway would have
// left on a request. It exists for tests: production code that imported it
// could make an identity no gateway stamped.
const authntestPath = "github.com/vislake/speed/pkg/authn/authntest"

// importersOf lists the files under root that import the package named and are
// not tests, and counts the files it read. It parses every such file for its
// imports whatever its build constraints say: go list reads only the files that
// build for the platform and tags of the run, and a file for another operating
// system, under a tag nobody sets here, or in a package whose every file is
// excluded is production code all the same.
//
// A directory named testdata is skipped, as the go command skips it, and so is
// one that holds a module of its own.
func importersOf(t *testing.T, root, target string) (files []string, scanned int) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		scanned++
		for _, spec := range f.Imports {
			if strings.Trim(spec.Path.Value, "\"`") == target {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				files = append(files, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the source files under %s: %v", root, err)
	}
	return files, scanned
}

// writeScratchModule lays out a module in a temporary directory with the
// files named, each given as a slash-separated path and its content. The
// module's own go.mod is written, and a file may bring its own to make a
// directory a module of its own.
func writeScratchModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module example.com/scratch\n\ngo 1.24\n"
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

// TestTheReadingFindsAnImporterUnderAnyBuildConstraint is the case for the
// reading of imports not depending on the platform the test runs on: a file for
// another operating system, or under a tag nobody sets here, is production code
// all the same, and the reading has to name it. The plain file is the positive
// control: the reading finds an importer that no constraint hides.
func TestTheReadingFindsAnImporterUnderAnyBuildConstraint(t *testing.T) {
	t.Parallel()
	const source = "package scratch\n\nimport _ \"" + authntestPath + "\"\n"
	cases := map[string]string{
		"evade.go":                    source,
		"evade_aix.go":                source,
		"evade_windows_amd64.go":      source,
		"evade_aix_tagged.go":         "//go:build aix && nobodysetsthis\n\n" + source,
		"evade_negated.go":            "//go:build !linux && !darwin\n\n" + source,
		"evade_tagged.go":             "//go:build nobodysetsthis\n\n" + source,
		"evade_ignored.go":            "//go:build ignore\n\n" + source,
		"evade_unsatisfiable.go":      "//go:build linux && !linux\n\n" + source,
		"evade_grouped.go":            "package scratch\n\nimport (\n\t\"fmt\"\n\t_ \"" + authntestPath + "\"\n)\n\nvar _ = fmt.Sprint\n",
		"evade_aliased.go":            "package scratch\n\nimport at \"" + authntestPath + "\"\n\nvar _ = at.Context\n",
		"evade_raw.go":                "package scratch\n\nimport _ `" + authntestPath + "`\n",
		"sub/evade_only_tagged.go":    "//go:build nobodysetsthis\n\n" + strings.Replace(source, "scratch", "sub", 1),
		"deep/er/evade_only_other.go": "//go:build aix\n\n" + strings.Replace(source, "scratch", "er", 1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeScratchModule(t, map[string]string{
				"clean.go": "package scratch\n",
				name:       content,
			})
			files, scanned := importersOf(t, dir, authntestPath)
			if scanned < 2 {
				t.Fatalf("the reading scanned %d files, want both of the module's", scanned)
			}
			if want := []string{filepath.FromSlash(name)}; !slices.Equal(files, want) {
				t.Errorf("the reading found the importers %v in a module whose file %s imports %s, want %v",
					files, name, authntestPath, want)
			}
		})
	}
}

// TestTheReadingLeavesOutWhatTheRuleDoesNotCover pins the other side of the
// reading: a file that is not production code of this module is not named. A
// test file is the rule's own exception, whatever platform or tag it carries; a
// testdata directory is not part of the build; and a directory with a go.mod of
// its own is another module, held to its own rule.
func TestTheReadingLeavesOutWhatTheRuleDoesNotCover(t *testing.T) {
	t.Parallel()
	const source = "package scratch\n\nimport _ \"" + authntestPath + "\"\n"
	cases := map[string]map[string]string{
		"a test file":                    {"clean_test.go": source},
		"a test file for a platform":     {"clean_windows_test.go": "//go:build windows\n\n" + source},
		"a test file in a subpackage":    {"sub/sub_test.go": source},
		"a file under testdata":          {"testdata/fixture.go": source},
		"a file under a nested module":   {"nested/go.mod": "module example.com/nested\n\ngo 1.24\n", "nested/nested.go": source},
		"a file that is not a go file":   {"notes.txt": source},
		"a file that does not import it": {"other.go": "package scratch\n\nimport _ \"fmt\"\n"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			extra["clean.go"] = "package scratch\n"
			dir := writeScratchModule(t, extra)
			files, scanned := importersOf(t, dir, authntestPath)
			if scanned == 0 {
				t.Fatal("the reading scanned no file")
			}
			if len(files) != 0 {
				t.Errorf("the reading found %v, want none", files)
			}
		})
	}
}

// TestNoCodeOfThisHostThatIsNotATestImportsAuthntest holds the rule for this
// module, which the rule in pkg/authn cannot reach: this host is a module of its
// own in the workspace, outside what go list ./... lists there, so its own
// non-test files are held to the rule here.
//
// The positive control is what makes the case more than a directory that was
// never read: the files have to include one of this host's that imports the
// authentication module.
func TestNoCodeOfThisHostThatIsNotATestImportsAuthntest(t *testing.T) {
	t.Parallel()
	files, scanned := importersOf(t, ".", "github.com/vislake/speed/pkg/authn")
	if scanned == 0 {
		t.Fatal("the reading scanned no file of this module")
	}
	if len(files) == 0 {
		t.Fatal("no file of this host is read as importing the authentication package, " +
			"so the reading does not hold imports")
	}

	if got, _ := importersOf(t, ".", authntestPath); len(got) != 0 {
		t.Errorf("%v import %s outside a test file: an identity built there would be one no gateway stamped",
			got, authntestPath)
	}
}
