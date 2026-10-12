package authn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	rootPath         = moduleImportPath + "/authn"
	authntestPath    = rootPath + "/authntest"
	mounttestPath    = internalPrefix + "mount/mounttest"
	internalPrefix   = rootPath + "/internal/"
	modelPath        = internalPrefix + "model"
	identityPath     = internalPrefix + "identity"
	tenantPath       = moduleImportPath + "/tenant"
	guardPath        = rootPath + "/guard"
	speedHTTPPath    = moduleImportPath + "/http"
	ginPath          = "github.com/gin-gonic/gin"
)

// identityWriters are the packages that may import the identity package in a
// file that is not a test: the root package, which re-exports its read side;
// the gateway, which writes the identity of the request chain; the login
// engine, which derives a context for the one audit record of a login and
// hands it to nothing else; and authntest, whose context builder is for tests.
// The package itself is not an importer of itself.
var identityWriters = []string{
	rootPath,
	rootPath + "/gate",
	rootPath + "/login",
	authntestPath,
}

// valueTypePackages are the four packages the root package may import besides
// the standard library, with what each of them may import in turn: model the
// standard library alone, the other three the standard library and model.
var valueTypePackages = map[string][]string{
	internalPrefix + "model":    nil,
	internalPrefix + "identity": {modelPath},
	internalPrefix + "verdict":  {modelPath},
	internalPrefix + "step":     {modelPath},
}

// listed is what the checks below read about one package of the module.
type listed struct {
	ImportPath string
	Standard   bool
	Dir        string
	// The file lists are go list's: the files that build here, the cgo files,
	// the files a build constraint leaves out, and the files that do not parse
	// as Go source. Test files are in none of them that the checks read.
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	InvalidGoFiles []string
	// Imports are the imports of every non-test file of the package, whether or
	// not it builds under this GOOS, GOARCH and set of build tags: go list's own
	// Imports are only those of the files that do, and a file for another
	// platform or under a custom tag is production code all the same. goList
	// fills it from the source after decoding, and go list's own list is
	// merged into it. The test imports are a separate list in go list's output,
	// and are not asked for.
	Imports []string
}

// sourceFiles are the non-test files of a package that go list reports under
// any of its file lists.
func (p listed) sourceFiles() []string {
	var out []string
	for _, list := range [][]string{p.GoFiles, p.CgoFiles, p.IgnoredGoFiles, p.InvalidGoFiles} {
		for _, name := range list {
			if !strings.HasSuffix(name, "_test.go") && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// readSourceImports adds the imports of every non-test file of the package to
// Imports.
func (p *listed) readSourceImports() error {
	for _, name := range p.sourceFiles() {
		imps, err := readImports(filepath.Join(p.Dir, name))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		for _, imp := range imps {
			if !slices.Contains(p.Imports, imp) {
				p.Imports = append(p.Imports, imp)
			}
		}
	}
	return nil
}

// readImports parses the import clauses of one file and returns the paths.
func readImports(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, spec := range f.Imports {
		// An import path in the AST is a quoted literal that the parser has
		// already checked is well formed.
		out = append(out, strings.Trim(spec.Path.Value, "\"`"))
	}
	return out, nil
}

// isStandard reports whether an import path is the standard library's, which is
// the paths whose first element has no dot.
func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// moduleRoot finds the directory of the pkg module from where this source file
// is, so that the answer does not depend on the directory the tests run from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	root, err := moduleRootFrom(file, cwd)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// moduleRootFrom finds the module that holds source, the path the compiler
// recorded for this file. Built with -trimpath that path is module-qualified
// and names no directory, and then the module is the one the test runs in: go
// test runs a package's tests from its source directory.
func moduleRootFrom(source, cwd string) (string, error) {
	dir := filepath.Dir(source)
	if info, err := os.Stat(dir); err != nil || !filepath.IsAbs(source) || !info.IsDir() {
		dir = cwd
	}
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD in %s: %w", dir, err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		return "", fmt.Errorf("%s is not inside a module", dir)
	}
	return filepath.Dir(mod), nil
}

// goList runs go list -e -json over patterns in dir and decodes the packages.
// -e keeps a package that does not compile in the listing, with its imports as
// the parser read them: the dependency rules are about what a package imports,
// and a package half way through a change still imports something.
//
// Every non-test file the package holds is then parsed for its imports, the
// ones a build constraint leaves out included, so the rules see what a build on
// another platform or with another tag would compile. Standard library packages
// are not parsed: nothing is asked of what they import.
func goList(t *testing.T, dir string, env []string, args ...string) []listed {
	t.Helper()
	const fields = "ImportPath,Standard,Dir,Imports,GoFiles,CgoFiles,IgnoredGoFiles,InvalidGoFiles"
	cmd := exec.Command("go", append([]string{"list", "-e", "-json=" + fields}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v in %s: %v\n%s", args, dir, err, stderr.String())
	}
	var pkgs []listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return pkgs
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		if !p.Standard {
			if err := p.readSourceImports(); err != nil {
				t.Fatalf("reading the imports of %s: %v", p.ImportPath, err)
			}
		}
		pkgs = append(pkgs, p)
	}
}

// listModule lists every package of the module at dir: go list ./..., and the
// directories it leaves out. A directory whose files a build constraint all
// excludes is matched by no pattern, so it is not in go list's output at all,
// and a package of nothing but files for another platform or under a custom tag
// is production code that nothing else would read. Those directories are found
// by walking the tree the way go list does: no directory that starts with a dot
// or an underscore, none named testdata or vendor, and none that is a module of
// its own.
func listModule(t *testing.T, dir string, env []string) []listed {
	t.Helper()
	pkgs := goList(t, dir, env, "./...")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != dir {
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		importPath := moduleImportPath
		if rel != "." {
			importPath += "/" + filepath.ToSlash(rel)
		}
		if _, ok := find(pkgs, importPath); ok {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		p := listed{ImportPath: importPath, Dir: path}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				p.IgnoredGoFiles = append(p.IgnoredGoFiles, e.Name())
			}
		}
		if len(p.IgnoredGoFiles) == 0 {
			return nil
		}
		if err := p.readSourceImports(); err != nil {
			return err
		}
		pkgs = append(pkgs, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s for packages go list does not list: %v", dir, err)
	}
	return pkgs
}

func find(pkgs []listed, path string) (listed, bool) {
	for _, p := range pkgs {
		if p.ImportPath == path {
			return p, true
		}
	}
	return listed{}, false
}

// rootImportViolations is the first rule: the root package's non-test imports
// are the standard library and the four value-type packages. The standard
// library is taken whole, net/http with it, because the cookie clearer's
// signature is written in its types; the rule is about what of this module and of
// third parties the root package may pull in. Every package in
// the second list is imported by tenant/resolve, audit/journal and ratelimit
// through this one, so an import added here enters all of their closures.
func rootImportViolations(pkgs []listed) []string {
	root, ok := find(pkgs, rootPath)
	if !ok {
		return []string{"the listing does not hold the root package"}
	}
	var out []string
	for _, imp := range root.Imports {
		if isStandard(imp) {
			continue
		}
		if _, allowed := valueTypePackages[imp]; allowed {
			continue
		}
		out = append(out, rootPath+" imports "+imp+": the root package may import the standard library and the four value-type packages only")
	}
	return out
}

// layerViolations is the second rule: the value-type packages depend on the
// standard library, and on model where the table above says so, and on nothing
// else.
func layerViolations(pkgs []listed) []string {
	var out []string
	for path, allow := range valueTypePackages {
		p, ok := find(pkgs, path)
		if !ok {
			out = append(out, "the listing does not hold "+path)
			continue
		}
		for _, imp := range p.Imports {
			if isStandard(imp) || slices.Contains(allow, imp) {
				continue
			}
			out = append(out, path+" imports "+imp+": it may import the standard library and "+strings.Join(allow, ", ")+" only")
		}
	}
	slices.Sort(out)
	return out
}

// authntestViolations is the third rule: no package imports authntest or
// mounttest in a file that is not a test, except those two themselves, and
// mounttest, which builds the identities of its requests with authntest, is the
// one package besides authntest that may import it. The compiler cannot stop
// production code importing either, and an identity built there would be one no
// gateway stamped, a route registered there one no gateway guards.
func authntestViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if p.ImportPath != authntestPath && p.ImportPath != mounttestPath && slices.Contains(p.Imports, authntestPath) {
			out = append(out, p.ImportPath+" imports authntest outside a test file")
		}
		if p.ImportPath != mounttestPath && slices.Contains(p.Imports, mounttestPath) {
			out = append(out, p.ImportPath+" imports mounttest outside a test file")
		}
	}
	slices.Sort(out)
	return out
}

// identityWriterViolations is the fourth rule: no package outside identityWriters
// imports the identity package in a file that is not a test. The identity
// package holds the one way to put an identity into a context, and Go admits
// every package under pkg/authn to it, so the compiler stops what sits outside
// the tree and this rule stops what sits inside it: a backend or a login method
// that imported it could stamp an identity the gateway never decided on.
func identityWriterViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if p.ImportPath == identityPath || slices.Contains(identityWriters, p.ImportPath) {
			continue
		}
		if slices.Contains(p.Imports, identityPath) {
			out = append(out, p.ImportPath+" imports "+identityPath+" outside a test file: only "+strings.Join(identityWriters, ", ")+" may")
		}
	}
	slices.Sort(out)
	return out
}

// unusedWriterViolations is the other direction of the fourth rule: a package on
// identityWriters that is in the listing and does not import the identity package
// in a non-test file is a name on the list with no code behind it, which the
// list should not carry, because a closed list that names a package that does
// not write is a list that hides what the writers are. A writer that is not in
// the listing at all is not reported: the login engine is on the list before it
// exists, and the entry is held to the code from the day the package appears.
func unusedWriterViolations(pkgs []listed) []string {
	var out []string
	for _, w := range identityWriters {
		if p, ok := find(pkgs, w); ok && !slices.Contains(p.Imports, identityPath) {
			out = append(out, w+" is on the list of identity writers and does not import "+identityPath+" outside a test file")
		}
	}
	slices.Sort(out)
	return out
}

// isTenant reports whether an import path is the tenant module or one of its
// subpackages.
func isTenant(path string) bool {
	return path == tenantPath || strings.HasPrefix(path, tenantPath+"/")
}

// inAuthnTree reports whether an import path is the root package or sits under it.
func inAuthnTree(path string) bool {
	return path == rootPath || strings.HasPrefix(path, rootPath+"/")
}

// tenantViolations is the fifth rule: no package of the authn tree, the root
// package and every package below it, reaches the tenant module. Tenancy depends
// on authentication and the admission layer that joins the two lives in the
// tenant directory, so the reverse edge would make the identity system know the
// directory of the tenants it is meant to serve. A package is judged on every
// non-test file it holds, the ones a build constraint leaves out included, and
// on what the packages outside the tree that it imports import in turn: an edge
// through another package of the module is the same edge. A package of the tree
// is judged on its own files and is not walked into from its importers, so one
// import of the tenant module is reported once, where it is written.
func tenantViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if !inAuthnTree(p.ImportPath) {
			continue
		}
		for _, imp := range p.Imports {
			if isTenant(imp) {
				out = append(out, p.ImportPath+" imports "+imp+": no package of the authn tree may import the tenant module")
				continue
			}
			if isStandard(imp) || inAuthnTree(imp) {
				continue
			}
			for _, reached := range reachable(pkgs, imp) {
				if isTenant(reached) {
					out = append(out, p.ImportPath+" reaches "+reached+" through "+imp+": no package of the authn tree may depend on the tenant module")
					break
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// guardImportViolations is the sixth rule: the guard package's non-test imports
// are the standard library, the root package, the HTTP entry point and gin, and
// nothing else. The middleware decides a request from the identity the gateway
// stamped, so no factor module, no store and no other package of the capability
// may enter its closure; Go admits every package under pkg/authn to it, so this
// rule is what keeps a leaf factor out. A host that mounts a guarded route then
// carries the factor only if its own code imports it.
func guardImportViolations(pkgs []listed) []string {
	p, ok := find(pkgs, guardPath)
	if !ok {
		return []string{"the listing does not hold " + guardPath}
	}
	allowed := []string{rootPath, speedHTTPPath, ginPath}
	var out []string
	for _, imp := range p.Imports {
		if isStandard(imp) || slices.Contains(allowed, imp) {
			continue
		}
		out = append(out, guardPath+" imports "+imp+": the guard package may import the standard library, the root package, the HTTP entry point and gin only")
	}
	slices.Sort(out)
	return out
}

// reachable is the set of non-standard packages reachable from start through
// the imports of every non-test file, start included, sorted. A package outside
// the listing is in the set and is not walked into.
func reachable(pkgs []listed, start string) []string {
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if seen[path] {
			continue
		}
		seen[path] = true
		if p, ok := find(pkgs, path); ok {
			for _, imp := range p.Imports {
				if !isStandard(imp) {
					queue = append(queue, imp)
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

// closure is the set of non-standard packages reachable from the root package
// through imports, over the listing of the whole module and the imports of
// every non-test file. It is walked here rather than read from go list -deps,
// which follows only the files that build under one platform and one set of
// tags. A package outside the listing is a third-party one: it is in the set
// and is not walked into, and any package of the listing is, whether or not the
// rules allow it.
func closure(pkgs []listed) []string { return reachable(pkgs, rootPath) }

// closureViolations is the transitive form of the first rule, over the closure
// of the root package: nothing outside the standard library and the four
// value-type packages is in it, so no third-party library and no gin reaches a
// package that only reads an identity.
func closureViolations(pkgs []listed) []string {
	var out []string
	for _, path := range closure(pkgs) {
		if path == rootPath {
			continue
		}
		if _, allowed := valueTypePackages[path]; allowed {
			continue
		}
		out = append(out, "the closure of the root package holds "+path)
	}
	return out
}

// TestTheRootPackageImportsOnlyWhatItMay is the executable form of the
// packaging decision's dependency statement, over the tree as it stands.
//
// Every package of the module is listed, not this one alone: the third rule has
// to see all of them, and the positive controls below make sure the listing
// holds what the rules are about — a listing that came back empty would let
// every rule pass.
func TestTheRootPackageImportsOnlyWhatItMay(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listModule(t, root, nil)

	for _, want := range append([]string{rootPath, authntestPath}, mapKeys(valueTypePackages)...) {
		if _, ok := find(pkgs, want); !ok {
			t.Fatalf("the listing does not hold %s, so what the rules read is not this module", want)
		}
	}
	if p, _ := find(pkgs, rootPath); len(p.Imports) == 0 {
		t.Fatal("the root package lists no import, so the listing is not reading its files")
	}
	if p, _ := find(pkgs, authntestPath); !slices.Contains(p.Imports, rootPath) {
		t.Fatal("authntest is not listed as importing the root package, so the listing does not hold imports")
	}
	if p, ok := find(pkgs, mounttestPath); !ok || !slices.Contains(p.Imports, authntestPath) {
		t.Fatal("mounttest is not listed as importing authntest, so the rule about who may import either has nothing to read")
	}
	for _, writer := range []string{rootPath, rootPath + "/gate", authntestPath} {
		if p, ok := find(pkgs, writer); !ok || !slices.Contains(p.Imports, identityPath) {
			t.Fatalf("%s is not listed as importing the identity package, so the writer rule has nothing to read", writer)
		}
	}
	if p, ok := find(pkgs, guardPath); !ok || len(p.Imports) == 0 {
		t.Fatal("the listing does not hold the guard package and its imports, so the guard rule has nothing to read")
	}

	for _, v := range rootImportViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range layerViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range authntestViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range identityWriterViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range unusedWriterViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range tenantViolations(pkgs) {
		t.Error(v)
	}
	for _, v := range guardImportViolations(pkgs) {
		t.Error(v)
	}
}

// TestTheRootPackageClosureHoldsNoThirdPartyPackage reads the closure
// transitively: the direct-import rule says what the root writes, this says
// what it ends up pulling in.
func TestTheRootPackageClosureHoldsNoThirdPartyPackage(t *testing.T) {
	pkgs := listModule(t, moduleRoot(t), nil)

	got := closure(pkgs)
	if !slices.Contains(got, modelPath) {
		t.Fatalf("the closure %q does not hold model, so it is not this package's closure", got)
	}
	if !slices.Contains(got, rootPath) {
		t.Fatalf("the closure %q does not hold the root package, so the walk did not start", got)
	}
	for _, v := range closureViolations(pkgs) {
		t.Error(v)
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestTheModuleRootIsFoundFromAnyDirectory holds that the checks do not depend
// on the directory they run from: a test binary started elsewhere still lists
// this module.
//
// A test binary built with -trimpath does not know where its source is, and
// finds the module from the working directory alone, which is what the next
// test holds; there is nothing to find it from here.
func TestTheModuleRootIsFoundFromAnyDirectory(t *testing.T) {
	if _, file, _, ok := runtime.Caller(0); !ok || !filepath.IsAbs(file) {
		t.Skip("built with -trimpath: the source file's directory is not recorded")
	}
	t.Chdir(t.TempDir())
	root := moduleRoot(t)
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod under %s: %v", root, err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(mod), []byte("module "+moduleImportPath)) {
		t.Errorf("the root found from another directory is %s, whose go.mod does not declare %s", root, moduleImportPath)
	}
}

// TestTheModuleRootIsFoundFromTrimmedSourcePaths holds that the checks run
// under -trimpath, which records the source file as a module-qualified path
// that is no directory: the module is then the one in the working directory.
func TestTheModuleRootIsFoundFromTrimmedSourcePaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := moduleRootFrom(rootPath+"/deps_test.go", cwd)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod under %s: %v", root, err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(mod), []byte("module "+moduleImportPath)) {
		t.Errorf("the root found for a trimmed path is %s, whose go.mod does not declare %s", root, moduleImportPath)
	}
}

// writeTree writes files, named by slash-separated path, under a fresh
// directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// violatingTree writes a module shaped like pkg, in which each rule is broken
// once, and returns its directory. The rules above are only worth anything if
// they fail on such a tree: a check that passes on the real tree and on this
// one holds nothing.
//
// Each file is a package whose non-test code breaks one rule:
//
//   - the root imports a third-party library and, through model, a package that
//     is not the standard library or one of the four;
//   - model imports identity, which the layering forbids;
//   - step imports a third-party library;
//   - a production package imports authntest, and another does so from a test
//     file alone, which is allowed; one imports mounttest, another does so from
//     a test file alone, and mounttest itself imports authntest, which is
//     allowed;
//   - a backend imports the identity package, as model does too, while the
//     gateway and the login engine do so legitimately and a session package
//     does so from a test file alone;
//   - packages of the tree import the tenant module: a subpackage directly, one
//     through a file under a custom tag, the root package from a platform
//     file, and one that only reaches it through a package of the module, while
//     the tenant module imports the root package, which is the direction the
//     design fixes and is no violation;
//   - files that the host's own GOOS and no build tag compile break the same
//     rules where go list does not read them: a root file for another
//     platform, an identity file under a custom tag, a package reached from the
//     root whose own tagged file imports a third-party library, and two
//     packages that import authntest from a platform file, a tagged file, or
//     tagged files alone.
func violatingTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"go.mod": "module " + moduleImportPath + "\n\ngo 1.26\n",
		"authn/authn.go": `package authn

import (
	_ "example.com/lib"
	_ "` + internalPrefix + `model"
	_ "` + moduleImportPath + `/other"
)
`,
		"authn/authn_windows.go":              "package authn\n\nimport _ \"example.com/windowsonly\"\n",
		"authn/internal/model/model.go":       "package model\n\nimport _ \"" + internalPrefix + "identity\"\n",
		"authn/internal/identity/identity.go": "package identity\n\nimport _ \"" + modelPath + "\"\n",
		"authn/internal/identity/custom.go":   "//go:build prodfixtures\n\npackage identity\n\nimport _ \"example.com/tagged\"\n",
		"authn/internal/verdict/verdict.go":   "package verdict\n\nimport _ \"" + modelPath + "\"\n",
		"authn/internal/step/step.go":         "package step\n\nimport _ \"example.com/lib\"\n",
		"authn/authntest/authntest.go":        "package authntest\n\nimport _ \"" + rootPath + "\"\n",
		"authn/internal/mount/mounttest/m.go": "package mounttest\n\nimport _ \"" + authntestPath + "\"\n",
		"usesmounttest/usesmounttest.go":      "package usesmounttest\n\nimport _ \"" + mounttestPath + "\"\n",
		"mounttestonly/mounttestonly.go":      "package mounttestonly\n",
		"mounttestonly/mounttestonly_test.go": "package mounttestonly\n\nimport _ \"" + mounttestPath + "\"\n",
		"authn/static/inject.go":              "package static\n\nimport _ \"" + identityPath + "\"\n",
		"authn/gate/gate.go":                  "package gate\n\nimport _ \"" + identityPath + "\"\n",
		"authn/login/login.go":                "package login\n\nimport _ \"" + identityPath + "\"\n",
		"authn/session/session.go":            "package session\n",
		"authn/account/account.go":            "package account\n\nimport _ \"" + tenantPath + "\"\n",
		"authn/sso/sso.go":                    "package sso\n",
		"authn/sso/sso_custom.go":             "//go:build prodfixtures\n\npackage sso\n\nimport _ \"" + tenantPath + "/resolve\"\n",
		"authn/token/token.go":                "package token\n\nimport _ \"" + moduleImportPath + "/audit\"\n",
		"audit/audit.go":                      "package audit\n\nimport _ \"" + tenantPath + "\"\n",
		"authn/authn_plan9.go":                "package authn\n\nimport _ \"" + tenantPath + "\"\n",
		"tenant/tenant.go":                    "package tenant\n\nimport _ \"" + rootPath + "\"\n",
		"tenant/resolve/resolve.go":           "package resolve\n",
		"authn/session/session_test.go":       "package session\n\nimport _ \"" + identityPath + "\"\n",
		"production/production.go":            "package production\n\nimport _ \"" + authntestPath + "\"\n",
		"platform/platform.go":                "package platform\n",
		"platform/platform_windows.go":        "package platform\n\nimport _ \"" + authntestPath + "\"\n",
		"tagged/tagged.go":                    "package tagged\n",
		"tagged/custom.go":                    "//go:build prodfixtures\n\npackage tagged\n\nimport _ \"" + authntestPath + "\"\n",
		"onlytagged/onlytagged.go":            "//go:build prodfixtures\n\npackage onlytagged\n\nimport _ \"" + authntestPath + "\"\n",
		"testonly/testonly.go":                "package testonly\n",
		"testonly/testonly_test.go":           "package testonly\n\nimport _ \"" + authntestPath + "\"\n",
		"testonly/testonly_windows_test.go":   "package testonly\n\nimport _ \"" + authntestPath + "\"\n",
		"other/other.go":                      "package other\n",
		"other/other_custom.go":               "//go:build prodfixtures\n\npackage other\n\nimport _ \"example.com/deep\"\n",
	})
}

// isolated keeps go list of a scratch module off the workspace and the network.
var isolated = []string{"GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off"}

// TestTheDependencyRulesFailOnAViolatingTree runs each rule over a tree that
// breaks it and asks for the failure, so the rules cannot pass vacuously. The
// tree is listed with the real go list: the file-by-file split between
// production and test imports is go list's, and that is the very property the
// authntest rule depends on.
func TestTheDependencyRulesFailOnAViolatingTree(t *testing.T) {
	tree := violatingTree(t)
	pkgs := listModule(t, tree, isolated)

	t.Run("the root imports something it may not", func(t *testing.T) {
		got := rootImportViolations(pkgs)
		wantFragments := []string{"example.com/lib", moduleImportPath + "/other", "example.com/windowsonly"}
		for _, frag := range wantFragments {
			if !anyContains(got, frag) {
				t.Errorf("no violation names %q among %q: the rule lets the root import it", frag, got)
			}
		}
		if anyContains(got, "internal/model") {
			t.Errorf("the rule reports model, which the root may import: %q", got)
		}
	})
	t.Run("a value-type package imports too much", func(t *testing.T) {
		got := layerViolations(pkgs)
		if !anyContains(got, "internal/model imports "+internalPrefix+"identity") {
			t.Errorf("no violation says model imports identity among %q", got)
		}
		if !anyContains(got, "internal/step imports example.com/lib") {
			t.Errorf("no violation says step imports a third-party library among %q", got)
		}
		if !anyContains(got, "internal/identity imports example.com/tagged") {
			t.Errorf("no violation says identity imports a library from a file under a custom build tag among %q", got)
		}
		if anyContains(got, "internal/verdict imports") {
			t.Errorf("the rule reports a package that imports only model: %q", got)
		}
	})
	t.Run("production code imports authntest", func(t *testing.T) {
		got := authntestViolations(pkgs)
		var names []string
		for _, v := range got {
			name, _, _ := strings.Cut(v, " ")
			names = append(names, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		want := []string{"onlytagged", "platform", "production", "tagged", "usesmounttest"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: a test file's import is allowed, authntest's own import of the root "+
				"is not an import of itself, and a file another platform or a custom build tag leaves out of the build "+
				"is still a file that imports it", names, want)
		}
	})
	t.Run("a package outside the writers imports the identity package", func(t *testing.T) {
		var names []string
		for _, v := range identityWriterViolations(pkgs) {
			name, _, _ := strings.Cut(v, " ")
			names = append(names, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		want := []string{"authn/internal/model", "authn/static"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: the gateway, the login engine, authntest and the root package may "+
				"import the identity package, a test file's import is allowed, and a package outside them is not", names, want)
		}
	})
	t.Run("a package on the list of writers does not write", func(t *testing.T) {
		var names []string
		for _, v := range unusedWriterViolations(pkgs) {
			name, _, _ := strings.Cut(v, " ")
			names = append(names, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		want := []string{"authn", "authn/authntest"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: the root package and authntest are on the list here and import "+
				"no identity package, while the gateway and the login engine do", names, want)
		}
	})
	t.Run("a package of the authn tree reaches the tenant module", func(t *testing.T) {
		var names []string
		for _, v := range tenantViolations(pkgs) {
			name, _, _ := strings.Cut(v, " ")
			names = append(names, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		want := []string{"authn", "authn/account", "authn/sso", "authn/token"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: a direct import, one from a file a custom build tag leaves out, "+
				"the root package's own file for another platform, and an import of a package that imports the "+
				"tenant module are all edges to it, while the tenant module importing the authn tree is the "+
				"direction the design fixes", names, want)
		}
		if got := tenantViolations(pkgs); !anyContains(got, "authn/token reaches "+tenantPath+" through "+moduleImportPath+"/audit") {
			t.Errorf("the edge through audit is not reported as one, got %q", got)
		}
	})
	t.Run("a closure with a third-party package", func(t *testing.T) {
		got := closureViolations(pkgs)
		for _, frag := range []string{"example.com/lib", moduleImportPath + "/other", "example.com/windowsonly", "example.com/deep"} {
			if !anyContains(got, frag) {
				t.Errorf("no violation names %q among %q: the closure rule lets it in", frag, got)
			}
		}
		if anyContains(got, "internal/model") || anyContains(got, "internal/identity") {
			t.Errorf("the closure rule reports a value-type package, which is allowed: %q", got)
		}
	})
}

// TestTheDependencyRulesPassOnACleanTree is the control for the case above:
// the same shape with no violation reports none, so the failures above are the
// rules finding something and not their being unable to pass.
func TestTheDependencyRulesPassOnACleanTree(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod":                              "module " + moduleImportPath + "\n\ngo 1.26\n",
		"authn/authn.go":                      "package authn\n\nimport (\n\t_ \"fmt\"\n\t_ \"" + modelPath + "\"\n\t_ \"" + identityPath + "\"\n)\n",
		"authn/internal/model/model.go":       "package model\n\nimport _ \"errors\"\n",
		"authn/internal/identity/identity.go": "package identity\n\nimport _ \"" + modelPath + "\"\n",
		"authn/internal/verdict/verdict.go":   "package verdict\n\nimport _ \"" + modelPath + "\"\n",
		"authn/internal/step/step.go":         "package step\n\nimport _ \"" + modelPath + "\"\n",
		"authn/authntest/authntest.go":        "package authntest\n\nimport (\n\t_ \"" + rootPath + "\"\n\t_ \"" + identityPath + "\"\n)\n",
		"authn/internal/mount/mounttest/m.go": "package mounttest\n\nimport _ \"" + authntestPath + "\"\n",
		"mounttestonly/mounttestonly.go":      "package mounttestonly\n",
		"mounttestonly/mounttestonly_test.go": "package mounttestonly\n\nimport _ \"" + mounttestPath + "\"\n",
		"authn/gate/gate.go":                  "package gate\n\nimport _ \"" + identityPath + "\"\n",
		"authn/login/login.go":                "package login\n\nimport _ \"" + identityPath + "\"\n",
		"authn/guard/guard.go":                "package guard\n\nimport (\n\t_ \"time\"\n\t_ \"" + rootPath + "\"\n\t_ \"" + speedHTTPPath + "\"\n\t_ \"" + ginPath + "\"\n)\n",
		"authn/static/static.go":              "package static\n",
		"authn/static/static_test.go":         "package static\n\nimport _ \"" + identityPath + "\"\n",
		"authn/authn_windows.go":              "package authn\n\nimport _ \"os\"\n",
		"testonly/testonly.go":                "package testonly\n",
		"testonly/testonly_test.go":           "package testonly\n\nimport _ \"" + authntestPath + "\"\n",
		"onlytagged/onlytagged.go":            "//go:build prodfixtures\n\npackage onlytagged\n\nimport _ \"fmt\"\n",
		"tenant/tenant.go":                    "package tenant\n\nimport _ \"" + rootPath + "\"\n",
		"tenant/resolve/resolve.go":           "package resolve\n\nimport _ \"" + tenantPath + "\"\n",
	})
	pkgs := listModule(t, dir, isolated)
	for name, got := range map[string][]string{
		"root":      rootImportViolations(pkgs),
		"layering":  layerViolations(pkgs),
		"authntest": authntestViolations(pkgs),
		"writers":   identityWriterViolations(pkgs),
		"unused":    unusedWriterViolations(pkgs),
		"tenant":    tenantViolations(pkgs),
		"guard":     guardImportViolations(pkgs),
	} {
		if len(got) != 0 {
			t.Errorf("the %s rule reports %q on a clean tree", name, got)
		}
	}
}

func anyContains(list []string, fragment string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, fragment) })
}

// TestTheListOfWritersIsHeldToTheCodeBehindIt holds the other direction of the
// writer rule on small trees: an entry of the list whose package exists and does
// not import the identity package is reported, an entry whose package does not
// exist yet is not, and one that imports it from a test file alone is.
func TestTheListOfWritersIsHeldToTheCodeBehindIt(t *testing.T) {
	writes := "package p\n\nimport _ \"" + identityPath + "\"\n"
	tree := func(files map[string]string) []listed {
		files["go.mod"] = "module " + moduleImportPath + "\n\ngo 1.26\n"
		files["authn/internal/identity/identity.go"] = "package identity\n"
		return listModule(t, writeTree(t, files), isolated)
	}
	names := func(vs []string) []string {
		var out []string
		for _, v := range vs {
			name, _, _ := strings.Cut(v, " ")
			out = append(out, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		return out
	}

	t.Run("every entry that exists writes", func(t *testing.T) {
		pkgs := tree(map[string]string{
			"authn/authn.go":               writes,
			"authn/gate/gate.go":           writes,
			"authn/login/login.go":         writes,
			"authn/authntest/authntest.go": writes,
		})
		if got := unusedWriterViolations(pkgs); len(got) != 0 {
			t.Errorf("violations %q on a tree where every listed package writes", got)
		}
	})
	t.Run("the login engine is not there yet", func(t *testing.T) {
		pkgs := tree(map[string]string{
			"authn/authn.go":               writes,
			"authn/gate/gate.go":           writes,
			"authn/authntest/authntest.go": writes,
		})
		if got := unusedWriterViolations(pkgs); len(got) != 0 {
			t.Errorf("violations %q for an entry whose package does not exist", got)
		}
	})
	t.Run("an entry that exists and does not write", func(t *testing.T) {
		pkgs := tree(map[string]string{
			"authn/authn.go":               writes,
			"authn/gate/gate.go":           writes,
			"authn/login/login.go":         "package login\n",
			"authn/authntest/authntest.go": writes,
		})
		if got := names(unusedWriterViolations(pkgs)); !slices.Equal(got, []string{"authn/login"}) {
			t.Errorf("violations name %q, want the login engine alone", got)
		}
	})
	t.Run("an entry that writes from a test file alone", func(t *testing.T) {
		pkgs := tree(map[string]string{
			"authn/authn.go":               writes,
			"authn/gate/gate.go":           writes,
			"authn/login/login.go":         "package login\n",
			"authn/login/login_test.go":    writes,
			"authn/authntest/authntest.go": writes,
		})
		if got := names(unusedWriterViolations(pkgs)); !slices.Equal(got, []string{"authn/login"}) {
			t.Errorf("violations name %q: the identity package imported from a test file is not what a writer is", got)
		}
	})
}

// The token bridge is the module's one place the JOSE library is named, and the
// library's floor is a requirement the pkg module states for itself. These two
// hold the design's statements about it: the bridge is the only non-test file
// that imports the library, no non-test file names the claims-without-
// verification escape hatch, and pkg/go.mod requires at least the version the
// design fixes.
const (
	goJosePath       = "github.com/go-jose/go-jose/v4"
	goJoseFloor      = "v4.1.5"
	tokenPackagePath = rootPath + "/token"
	// unsafeClaims is the library's escape hatch, which decodes a token's
	// claims without checking its signature.
	unsafeClaims = "UnsafeClaimsWithoutVerification"
)

// The module binds several third-party libraries, each to one leaf package: the
// TOTP library to the TOTP factor, the WebAuthn library to the passkey factor and
// login method, the two SMS carrier SDKs to their adapters, and the SAML and
// XML-signature libraries to the SAML method. A host that imports none of the
// leaves carries none of the libraries, so each is confined to its leaf. Their
// versions, and those of the other libraries the selection table fixes to an
// exact version, are pinned in pinnedLibraries below.
const (
	otpPath         = "github.com/pquerna/otp"
	otpVersion      = "v1.5.0"
	totpPackagePath = rootPath + "/mfa/totp"

	webauthnPath        = "github.com/go-webauthn/webauthn"
	webauthnVersion     = "v0.18.2"
	webauthnPackagePath = rootPath + "/mfa/webauthn"

	aliyunSMSPath        = "github.com/alibabacloud-go/dysmsapi-20170525/v5"
	aliyunSMSVersion     = "v5.6.0"
	aliyunSMSPackagePath = rootPath + "/sms/aliyun"

	tencentSMSPath        = "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms"
	tencentSMSVersion     = "v1.3.172"
	tencentSMSPackagePath = rootPath + "/sms/tencent"

	gosaml2Path     = "github.com/russellhaering/gosaml2"
	gosaml2Version  = "v0.12.0"
	goxmldsigPath   = "github.com/russellhaering/goxmldsig"
	goxmldsigFloor  = "v1.6.1"
	samlPackagePath = rootPath + "/sso/saml"

	scsPath         = "github.com/alexedwards/scs/v2"
	scsVersion      = "v2.9.0"
	argon2idPath    = "github.com/alexedwards/argon2id"
	argon2idVersion = "v1.0.0"
	uuidPath        = "github.com/google/uuid"
	uuidVersion     = "v1.6.0"
	xCryptoPath     = "golang.org/x/crypto"
	xCryptoVersion  = "v0.57.0"
	xTextPath       = "golang.org/x/text"
	xTextVersion    = "v0.42.0"
)

// bridgeLibrary is one third-party library the module binds to a single leaf
// package: the leaf package whose non-test files, and its subpackages, are the
// only ones that may import it.
type bridgeLibrary struct {
	path string
	leaf string
}

// bridgeLibraries are the libraries this file holds to a leaf package by the
// package name rather than by a file list, so the leaf's own files may grow
// without the guard changing. The JOSE bridge is not here: its importer is a
// single file, and TestTheTokenBridgeIsTheOnlyGoJoseUserAndTheFloorIsHeld holds
// it to that file and to the claims-without-verification escape hatch besides.
var bridgeLibraries = []bridgeLibrary{
	{path: otpPath, leaf: totpPackagePath},
	{path: webauthnPath, leaf: webauthnPackagePath},
	{path: aliyunSMSPath, leaf: aliyunSMSPackagePath},
	{path: tencentSMSPath, leaf: tencentSMSPackagePath},
	{path: gosaml2Path, leaf: samlPackagePath},
	{path: goxmldsigPath, leaf: samlPackagePath},
}

// pinnedLibrary is one third-party library the design's selection table fixes
// to an exact version, which pkg/go.mod must state and no other.
type pinnedLibrary struct {
	path    string
	version string
}

// pinnedLibraries are the libraries the module requires whose version the
// design's selection table fixes exactly rather than as a floor. The design
// states floors for the JOSE, goxmldsig and x/oauth2 bridges, each held by its
// own test; every other library it names is fixed to the table version, so
// pkg/go.mod must state exactly that version for each: an upgrade goes through a
// dedicated change, not go get -u, and a transitive requirement could otherwise
// move one.
//
// The TOTP, WebAuthn, SAML, goxmldsig and two SMS libraries are also the
// bridgeLibraries above, which hold each to its leaf; go-oidc is also a
// multiLeafLibrary, held to its leaves. The session store, the Argon2id encoder
// and the uuid, x/crypto and x/text libraries have no single leaf, so they are
// pinned here alone.
var pinnedLibraries = []pinnedLibrary{
	{path: goOIDCPath, version: goOIDCVersion},
	{path: gosaml2Path, version: gosaml2Version},
	{path: otpPath, version: otpVersion},
	{path: webauthnPath, version: webauthnVersion},
	{path: aliyunSMSPath, version: aliyunSMSVersion},
	{path: tencentSMSPath, version: tencentSMSVersion},
	{path: scsPath, version: scsVersion},
	{path: argon2idPath, version: argon2idVersion},
	{path: uuidPath, version: uuidVersion},
	{path: xCryptoPath, version: xCryptoVersion},
	{path: xTextPath, version: xTextVersion},
}

// The OIDC and OAuth 2.0 bridges bind their library to more than one leaf: the
// google login method and, once the enterprise protocol modules are in, the
// authn.sso.oidc method both import go-oidc; the google and github login methods
// and the enterprise OIDC method all import x/oauth2. Each library is confined
// to the set of leaves that bridge it; go-oidc's version is pinned to the table
// version in pinnedLibraries below, and x/oauth2 is held at or above the floor
// the design fixes, in floorLibraries.
const (
	goOIDCPath         = "github.com/coreos/go-oidc/v3"
	goOIDCVersion      = "v3.21.0"
	xOAuth2Path        = "golang.org/x/oauth2"
	xOAuth2Floor       = "v0.37.0"
	googlePackagePath  = rootPath + "/login/google"
	githubPackagePath  = rootPath + "/login/github"
	ssoOIDCPackagePath = rootPath + "/sso/oidc"
)

// multiLeafBridge is one third-party library the module binds to a set of leaf
// packages: each of them, and its subpackages, may import the library and no
// other package of the module may. It is the multi-leaf form of bridgeLibrary,
// for a library more than one leaf bridges.
type multiLeafBridge struct {
	path   string
	leaves []string
}

// multiLeafLibraries are the libraries more than one package bridges, each held
// to the leaves that bridge it. A leaf that is not in the tree yet is listed all
// the same: the entry is held to the code from the day the package appears, and
// an import from a package that is not one of the leaves is reported either way.
var multiLeafLibraries = []multiLeafBridge{
	{path: goOIDCPath, leaves: []string{googlePackagePath, ssoOIDCPackagePath}},
	{path: xOAuth2Path, leaves: []string{googlePackagePath, githubPackagePath, ssoOIDCPackagePath}},
}

// floorLibrary is one third-party library the design holds at or above a version
// floor rather than pinning to an exact table version, because it carries a
// security advisory below that floor.
type floorLibrary struct {
	path  string
	floor string
}

// floorLibraries are the libraries the design holds at or above a floor rather
// than pinning: x/oauth2 and goxmldsig here. go-jose has its own test, which
// holds the bridge file and the claims-without-verification escape hatch besides.
var floorLibraries = []floorLibrary{
	{path: xOAuth2Path, floor: xOAuth2Floor},
	{path: goxmldsigPath, floor: goxmldsigFloor},
}

// floorViolation reports, if any, how pkg/go.mod fails to hold lib at or above
// its floor. A version below the floor is reported, and so is a missing
// requirement, because a transitive requirement alone could resolve below the
// floor. A version at or above the floor is not reported: the floor is a lower
// bound, so an upgrade beyond it is what the design asks for.
func floorViolation(gomod string, lib floorLibrary) string {
	version, ok := requiredVersion(gomod, lib.path)
	if !ok {
		return "pkg/go.mod does not require " + lib.path + ", so a transitive requirement could pull it below the floor"
	}
	if compareVersions(version, lib.floor) < 0 {
		return fmt.Sprintf("pkg/go.mod requires %s %s, want at least %s", lib.path, version, lib.floor)
	}
	return ""
}

// libraryConfinementViolations reports the packages of the module that import
// the library at libPath in a non-test file but are not any of the leaf packages
// that bind it, nor a package under one of them. An import anywhere else would
// put the library into that package's closure, and so into a host that never
// asked for it. A test file is not what puts a library into a closure, so the
// rule reads the non-test imports that go list and readSourceImports collect.
func libraryConfinementViolations(pkgs []listed, libPath string, leaves ...string) []string {
	var out []string
	for _, p := range pkgs {
		if p.Standard || p.Dir == "" || underAnyLeaf(p.ImportPath, leaves) {
			continue
		}
		if slices.ContainsFunc(p.Imports, func(imp string) bool { return imp == libPath || strings.HasPrefix(imp, libPath+"/") }) {
			out = append(out, p.ImportPath+" imports "+libPath+" outside "+strings.Join(leaves, ", "))
		}
	}
	slices.Sort(out)
	return out
}

// underAnyLeaf reports whether path is one of the leaf packages or sits under
// one: a bridge library that a leaf bridges is the leaf's whole subtree.
func underAnyLeaf(path string, leaves []string) bool {
	for _, leaf := range leaves {
		if path == leaf || strings.HasPrefix(path, leaf+"/") {
			return true
		}
	}
	return false
}

// libraryImporters lists every non-test file of the module that imports the
// library at libPath or one of its subpackages, as slash-separated paths
// relative to the module root, sorted. The subpackages count as the library: a
// bridge that imports github.com/example/lib/sub still binds the library whole.
func libraryImporters(t *testing.T, root string, pkgs []listed, libPath string) []string {
	t.Helper()
	var out []string
	for _, p := range pkgs {
		if p.Standard || p.Dir == "" {
			continue
		}
		for _, name := range p.sourceFiles() {
			imps, err := readImports(filepath.Join(p.Dir, name))
			if err != nil {
				t.Fatalf("reading the imports of %s: %v", name, err)
			}
			if slices.ContainsFunc(imps, func(imp string) bool { return imp == libPath || strings.HasPrefix(imp, libPath+"/") }) {
				rel, err := filepath.Rel(root, filepath.Join(p.Dir, name))
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, filepath.ToSlash(rel))
			}
		}
	}
	slices.Sort(out)
	return out
}

// namesUnsafeClaims reports whether a non-test file names the library's
// claims-without-verification escape hatch. It reads the parsed identifiers, so
// a mention in a comment is not one, and a file that does not parse is left to
// the build to report.
func namesUnsafeClaims(path string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == unsafeClaims {
			found = true
		}
		return !found
	})
	return found
}

// requiredVersion reads the version pkg/go.mod requires of an import path,
// whether the requirement is in a block or stands alone.
func requiredVersion(gomod, path string) (string, bool) {
	for line := range strings.Lines(gomod) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == path {
			return fields[1], true
		}
	}
	return "", false
}

// versionNumbers reads the major, minor and patch of a vX.Y.Z version, ignoring
// any prerelease or build suffix.
func versionNumbers(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out
		}
		out[i] = n
	}
	return out
}

// compareVersions orders two vX.Y.Z versions.
func compareVersions(a, b string) int {
	pa, pb := versionNumbers(a), versionNumbers(b)
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func TestTheTokenBridgeIsTheOnlyGoJoseUserAndTheFloorIsHeld(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listModule(t, root, nil)

	t.Run("only the token bridge imports the JOSE library", func(t *testing.T) {
		got := libraryImporters(t, root, pkgs, goJosePath)
		want := []string{strings.TrimPrefix(tokenPackagePath, moduleImportPath+"/") + "/jose.go"}
		if !slices.Equal(got, want) {
			t.Errorf("the non-test files importing %s are %v, want %v", goJosePath, got, want)
		}
	})

	t.Run("no non-test file names the claims-without-verification escape hatch", func(t *testing.T) {
		for _, p := range pkgs {
			if p.Standard || p.Dir == "" {
				continue
			}
			for _, name := range p.sourceFiles() {
				if namesUnsafeClaims(filepath.Join(p.Dir, name)) {
					t.Errorf("%s/%s names %s", p.ImportPath, name, unsafeClaims)
				}
			}
		}
	})

	t.Run("pkg/go.mod requires the library at or above the floor", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		version, ok := requiredVersion(string(data), goJosePath)
		if !ok {
			t.Fatalf("pkg/go.mod does not require %s, so a transitive requirement could pull it below the floor", goJosePath)
		}
		if compareVersions(version, goJoseFloor) < 0 {
			t.Errorf("pkg/go.mod requires %s %s, want at least %s", goJosePath, version, goJoseFloor)
		}
	})
}

// pinViolation reports, if any, how pkg/go.mod fails to state the exact version
// the design pins for lib. A version other than the pinned one, above or below,
// is a violation: the design fixes these libraries to the table version and
// routes an upgrade through a dedicated change, so a requirement that floats is
// not what it asks for. A missing requirement is reported too, because a
// transitive requirement alone could resolve to anything.
func pinViolation(gomod string, lib pinnedLibrary) string {
	version, ok := requiredVersion(gomod, lib.path)
	if !ok {
		return "pkg/go.mod does not require " + lib.path + ", so a transitive requirement could move it"
	}
	if version != lib.version {
		return fmt.Sprintf("pkg/go.mod requires %s %s, want exactly %s", lib.path, version, lib.version)
	}
	return ""
}

// TestEachBridgeLibraryIsConfinedToItsLeaf is the executable form of the
// design's per-library confinement statement: the pquerna/otp, go-webauthn,
// Aliyun Dysmsapi, Tencent Cloud SMS, gosaml2 and goxmldsig bindings appear in
// the non-test files of their own leaf package and nowhere else.
//
// The leaf package is looked for in the listing first and has to import the
// library, so a rule that reads no file of it is reported rather than passing on
// an empty read.
func TestEachBridgeLibraryIsConfinedToItsLeaf(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listModule(t, root, nil)

	for _, lib := range bridgeLibraries {
		t.Run(lib.path, func(t *testing.T) {
			leaf, ok := find(pkgs, lib.leaf)
			if !ok {
				t.Fatalf("the listing does not hold %s, so the rule about %s has nothing to read", lib.leaf, lib.path)
			}
			if !slices.ContainsFunc(leaf.Imports, func(imp string) bool {
				return imp == lib.path || strings.HasPrefix(imp, lib.path+"/")
			}) {
				t.Fatalf("the leaf %s does not import %s, so the rule about it has nothing to read", lib.leaf, lib.path)
			}
			for _, v := range libraryConfinementViolations(pkgs, lib.path, lib.leaf) {
				t.Error(v)
			}
		})
	}
}

// TestEachPinnedLibraryStatesTheTableVersion is the other half of the design's
// per-library statement, over every library the module requires and the
// selection table fixes to an exact version: pkg/go.mod states that version and
// no other, so a requirement that floats above or below it, or is missing, is
// reported.
func TestEachPinnedLibraryStatesTheTableVersion(t *testing.T) {
	root := moduleRoot(t)
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	for _, lib := range pinnedLibraries {
		t.Run(lib.path, func(t *testing.T) {
			if v := pinViolation(string(gomod), lib); v != "" {
				t.Error(v)
			}
		})
	}
}

// TestTheVersionPinFailsOnADriftedRequirement holds the pin above to be a pin
// and not a floor: a go.mod that states a version above the table's, or below
// it, or does not name the library at all, is reported, so the check cannot pass
// on the real tree by only ever accepting a lower bound. A requirement at the
// exact version is not reported.
func TestTheVersionPinFailsOnADriftedRequirement(t *testing.T) {
	lib := pinnedLibrary{path: otpPath, version: otpVersion}
	line := func(version string) string { return "\t" + otpPath + " " + version + "\n" }
	if v := pinViolation(line(otpVersion), lib); v != "" {
		t.Errorf("a go.mod at the pinned version is reported: %q", v)
	}
	for _, gomod := range []string{line("v1.6.0"), line("v1.4.0"), ""} {
		if v := pinViolation(gomod, lib); v == "" {
			t.Errorf("a go.mod %q is not reported, so the pin would let it through", gomod)
		}
	}
}

// TestTheBridgeConfinementFailsOnAViolatingTree runs the confinement rule over
// a tree that imports a bridge library from outside its leaf, so the rule can
// fail and is not passing on the real tree by accident. The leaf's own files
// may import the library and its subpackages, and its test file may import the
// library; an import of the library or of a subpackage of it from another
// package is reported, the second of which shows the rule reads the library's
// subpackages too.
func TestTheBridgeConfinementFailsOnAViolatingTree(t *testing.T) {
	tree := writeTree(t, map[string]string{
		"go.mod":                        "module " + moduleImportPath + "\n\ngo 1.26\n",
		"authn/mfa/totp/factor.go":      "package totp\n\nimport _ \"" + otpPath + "\"\n",
		"authn/mfa/totp/code.go":        "package totp\n\nimport _ \"" + otpPath + "/totp\"\n",
		"authn/mfa/totp/factor_test.go": "package totp\n\nimport _ \"" + otpPath + "\"\n",
		"authn/static/static.go":        "package static\n\nimport _ \"" + otpPath + "\"\n",
		"authn/subpkg/subpkg.go":        "package subpkg\n\nimport _ \"" + otpPath + "/hotp\"\n",
	})
	pkgs := listModule(t, tree, isolated)

	got := libraryConfinementViolations(pkgs, otpPath, totpPackagePath)
	want := []string{
		moduleImportPath + "/authn/static imports " + otpPath + " outside " + totpPackagePath,
		moduleImportPath + "/authn/subpkg imports " + otpPath + " outside " + totpPackagePath,
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations are %q, want %q: the leaf's own files and its test file are allowed, and an import of the "+
			"library or one of its subpackages from another package is reported", got, want)
	}
}

// TestTheGuardImportRuleFailsOnAViolatingTree runs the guard rule over a tree
// whose guard package imports a factor leaf, so the rule can fail and is not
// passing on the real tree by accident. The standard library, the root package,
// the HTTP entry point and gin are allowed; an import of any other package of
// the capability, a factor module among them, is reported.
func TestTheGuardImportRuleFailsOnAViolatingTree(t *testing.T) {
	tree := writeTree(t, map[string]string{
		"go.mod":               "module " + moduleImportPath + "\n\ngo 1.26\n",
		"authn/guard/guard.go": "package guard\n\nimport (\n\t_ \"time\"\n\t_ \"" + rootPath + "\"\n\t_ \"" + speedHTTPPath + "\"\n\t_ \"" + ginPath + "\"\n\t_ \"" + rootPath + "/mfa/totp\"\n)\n",
	})
	pkgs := listModule(t, tree, isolated)

	got := guardImportViolations(pkgs)
	want := []string{guardPath + " imports " + rootPath + "/mfa/totp: the guard package may import the standard library, the root package, the HTTP entry point and gin only"}
	if !slices.Equal(got, want) {
		t.Errorf("violations are %q, want %q: the standard library, the root package, the HTTP entry point and gin "+
			"are allowed, and any other import is reported", got, want)
	}
}

// TestTheMultiLeafBridgesAreConfinedToTheirLeaves holds the libraries more than
// one leaf bridges, each to the set of leaves that bridge it: a non-test import
// from a package outside the set is reported. It also holds the floored
// libraries among them at or above their floor, where pkg/go.mod requires one
// below the floor or does not require it at all.
//
// Each library gets a positive control: at least one leaf has to import it, so
// the rule cannot pass on a listing that holds none of the leaves and reads no
// import to judge. The exact pin of a multi-leaf library is held by
// TestEachPinnedLibraryStatesTheTableVersion, over the same library.
func TestTheMultiLeafBridgesAreConfinedToTheirLeaves(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listModule(t, root, nil)
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	for _, lib := range multiLeafLibraries {
		t.Run("confine "+lib.path, func(t *testing.T) {
			bound := false
			for _, leaf := range lib.leaves {
				p, ok := find(pkgs, leaf)
				if ok && slices.ContainsFunc(p.Imports, func(imp string) bool {
					return imp == lib.path || strings.HasPrefix(imp, lib.path+"/")
				}) {
					bound = true
				}
			}
			if !bound {
				t.Fatalf("no leaf of %s imports it, so the rule about it has nothing to read", lib.path)
			}
			for _, v := range libraryConfinementViolations(pkgs, lib.path, lib.leaves...) {
				t.Error(v)
			}
		})
	}

	for _, lib := range floorLibraries {
		t.Run("floor "+lib.path, func(t *testing.T) {
			if v := floorViolation(string(gomod), lib); v != "" {
				t.Error(v)
			}
		})
	}
}

// TestTheFloorFailsBelowIt holds the floor above to be a floor and not a pin: a
// go.mod that states a version below the floor, or does not name the library at
// all, is reported; a requirement exactly at the floor, and one above it, are
// not. Without this the floor check could pass on the real tree while accepting
// any lower bound, or none.
func TestTheFloorFailsBelowIt(t *testing.T) {
	lib := floorLibrary{path: xOAuth2Path, floor: xOAuth2Floor}
	line := func(version string) string { return "\t" + xOAuth2Path + " " + version + "\n" }
	for _, gomod := range []string{line(xOAuth2Floor), line("v0.38.0")} {
		if v := floorViolation(gomod, lib); v != "" {
			t.Errorf("a go.mod %q is reported: %q", gomod, v)
		}
	}
	for _, gomod := range []string{line("v0.36.0"), ""} {
		if v := floorViolation(gomod, lib); v == "" {
			t.Errorf("a go.mod %q is not reported, so the floor would let it through", gomod)
		}
	}
}

// TestTheMultiLeafConfinementFailsOnAViolatingTree runs the confinement rule
// over a tree in which a library more than one leaf bridges is imported from
// each of its allowed leaves and from a package that is not one of them, so the
// rule can fail and is not passing on the real tree by accident. An import from
// any allowed leaf is allowed, and an import from another package is reported.
func TestTheMultiLeafConfinementFailsOnAViolatingTree(t *testing.T) {
	tree := writeTree(t, map[string]string{
		"go.mod":                  "module " + moduleImportPath + "\n\ngo 1.26\n",
		"authn/login/google/g.go": "package google\n\nimport _ \"" + xOAuth2Path + "\"\n",
		"authn/login/github/g.go": "package github\n\nimport _ \"" + xOAuth2Path + "\"\n",
		"authn/sso/oidc/o.go":     "package oidc\n\nimport _ \"" + xOAuth2Path + "\"\n",
		"authn/elsewhere/e.go":    "package elsewhere\n\nimport _ \"" + xOAuth2Path + "\"\n",
	})
	pkgs := listModule(t, tree, isolated)

	leaves := []string{googlePackagePath, githubPackagePath, ssoOIDCPackagePath}
	got := libraryConfinementViolations(pkgs, xOAuth2Path, leaves...)
	want := []string{moduleImportPath + "/authn/elsewhere imports " + xOAuth2Path + " outside " + strings.Join(leaves, ", ")}
	if !slices.Equal(got, want) {
		t.Errorf("violations are %q, want %q: an import from any allowed leaf is allowed, and an import from "+
			"another package is reported", got, want)
	}
}
