package mail_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// moduleImportPath is this repository's pkg module path. Every package of it is
// inside the budget below: a package here is imported as a module of this
// repository.
const moduleImportPath = "github.com/vislake/speed/pkg"

// mailRootPath is the capability's own root package, whose non-test dependency
// face a host compiles by importing it.
const mailRootPath = moduleImportPath + "/mail"

// allowedRootPackages is the explicit set of packages the root package may
// reach: reading its values, the registry, redaction, and the literal redaction
// rules. Nothing else of this repository is on the list, so importing any other
// capability — pkg/i18n, pkg/sms, a carrier leaf — from the root package fails
// the closure guard below.
var allowedRootPackages = []string{
	moduleImportPath + "/config",
	moduleImportPath + "/core",
	moduleImportPath + "/internal/literal",
	moduleImportPath + "/log",
}

// The one carrier SDK, confined to its own leaf package and pinned to the table
// version. It is the library a host carries only when it deploys on this
// carrier, so it must appear in its leaf's own files and nowhere else.
const (
	smtpSDKPath     = "github.com/wneessen/go-mail"
	smtpSDKVersion  = "v0.8.2"
	smtpCarrierLeaf = mailRootPath + "/smtp"
)

// TestRootClosureHoldsOnlyTheDeclaredPackages is the executable form of the
// capability's dependency-direction boundary: the root package carries the
// interface and reaches a carrier only through the module factory, so no other
// capability and no vendor SDK is in what a host compiles by importing this
// package.
//
// Nothing holds that split at compile time — a carrier subpackage and the root
// package are one module — so this case is the gate that keeps it. The
// observation is the root package's own non-test dependency face; the closure of
// a package a case imports is not what a host pays for.
func TestRootClosureHoldsOnlyTheDeclaredPackages(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: this package reaches pkg/core, so a listing that
	// lost it came from a load that did not happen and every assertion below
	// would pass on an empty list.
	if !root[moduleImportPath+"/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

	// The budget is the standard library, the explicit allowlist above and the
	// closures of those packages. A package of this repository that is not in
	// the allowlist, and a vendor SDK, are both outside it. The root package's
	// own path is on the list because go list reports it among its own
	// dependencies.
	withinBudget := map[string]bool{mailRootPath: true}
	for _, pkg := range allowedRootPackages {
		for reached := range dependenciesOf(t, pkg) {
			withinBudget[reached] = true
		}
	}

	for pkg := range root {
		if stdlibPackage(pkg) || withinBudget[pkg] {
			continue
		}
		t.Errorf("the root package depends on %s. Reading configuration costs pkg/config and its own "+
			"closure, and nothing besides: another capability and a vendor SDK each belong to the "+
			"subpackage that binds them, so that a host compiling this package carries no carrier but "+
			"the one it deploys on", pkg)
	}
}

// The carrier SDK is bound to its own leaf package, held by the package name
// rather than by a file list so a leaf's files may grow without the guard
// changing.
type carrierSDK struct {
	path string
	leaf string
}

var carrierSDKs = []carrierSDK{
	{path: smtpSDKPath, leaf: smtpCarrierLeaf},
}

// TestEachCarrierSDKIsConfinedToItsLeaf is the executable form of the design's
// per-carrier confinement: the vendor SDK appears in the non-test files of its
// own leaf package and nowhere else in the pkg module.
//
// The leaf package is looked for in the listing first and has to import the SDK,
// so a rule that reads no file of it is reported rather than passing on an empty
// read.
func TestEachCarrierSDKIsConfinedToItsLeaf(t *testing.T) {
	pkgs := listModule(t)

	for _, sdk := range carrierSDKs {
		t.Run(sdk.path, func(t *testing.T) {
			leaf, ok := find(pkgs, sdk.leaf)
			if !ok {
				t.Fatalf("the listing does not hold %s, so the rule about %s has nothing to read", sdk.leaf, sdk.path)
			}
			if !importsLibrary(leaf.Imports, sdk.path) {
				t.Fatalf("the leaf %s does not import %s, so the rule about it has nothing to read", sdk.leaf, sdk.path)
			}
			for _, p := range pkgs {
				if p.Standard || p.Dir == "" || underLeaf(p.ImportPath, sdk.leaf) {
					continue
				}
				if importsLibrary(p.Imports, sdk.path) {
					t.Errorf("%s imports %s outside %s", p.ImportPath, sdk.path, sdk.leaf)
				}
			}
		})
	}
}

// carrierPin is one SDK and the exact version the table fixes it to.
type carrierPin struct {
	path    string
	version string
}

var carrierPins = []carrierPin{
	{path: smtpSDKPath, version: smtpSDKVersion},
}

// TestEachCarrierSDKIsPinnedToTheTableVersion holds that pkg/go.mod states the
// table version for each carrier SDK and no other: an upgrade goes through a
// dedicated change, not go get -u, and a transitive requirement could otherwise
// move one.
func TestEachCarrierSDKIsPinnedToTheTableVersion(t *testing.T) {
	root := moduleRoot(t)
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	for _, pin := range carrierPins {
		t.Run(pin.path, func(t *testing.T) {
			if v := pinViolation(string(gomod), pin); v != "" {
				t.Error(v)
			}
		})
	}
}

// TestTheVersionPinFailsOnADriftedRequirement holds the pin above to be a pin
// and not a floor: a go.mod that states a version above or below the table's, or
// does not name the library at all, is reported. A requirement at the exact
// version is not.
func TestTheVersionPinFailsOnADriftedRequirement(t *testing.T) {
	pin := carrierPin{path: smtpSDKPath, version: smtpSDKVersion}
	line := func(version string) string { return "\t" + smtpSDKPath + " " + version + "\n" }
	if v := pinViolation(line(smtpSDKVersion), pin); v != "" {
		t.Errorf("a go.mod at the pinned version is reported: %q", v)
	}
	for _, gomod := range []string{line("v0.9.0"), line("v0.8.0"), ""} {
		if v := pinViolation(gomod, pin); v == "" {
			t.Errorf("a go.mod %q is not reported, so the pin would let it through", gomod)
		}
	}
}

// pinViolation reports, if any, how pkg/go.mod fails to pin lib to its exact
// table version.
func pinViolation(gomod string, pin carrierPin) string {
	version, ok := requiredVersion(gomod, pin.path)
	if !ok {
		return pin.path + " is not required in pkg/go.mod at " + pin.version
	}
	if version != pin.version {
		return fmt.Sprintf("pkg/go.mod requires %s %s, want %s", pin.path, version, pin.version)
	}
	return ""
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

// listed is what the checks below read about one package of the module.
type listed struct {
	ImportPath string
	Standard   bool
	Dir        string
	Imports    []string
}

// moduleRoot finds the directory of the pkg module from where this source file
// is, so the answer does not depend on the directory the tests run from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}
	dir := filepath.Dir(file)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("working directory: %v", err)
		}
		dir = cwd
	}
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOMOD in %s: %v", dir, err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		t.Fatalf("%s is not inside a module", dir)
	}
	return filepath.Dir(mod)
}

// listModule lists every package of the pkg module with its non-test imports,
// through go list -e so a package half way through a change is still read for
// what it imports. Test imports are not what puts a library into a closure, and
// go list's own Imports field leaves them out.
func listModule(t *testing.T) []listed {
	t.Helper()
	const fields = "ImportPath,Standard,Dir,Imports"
	cmd := exec.Command("go", "list", "-e", "-json="+fields, "./...")
	cmd.Dir = moduleRoot(t)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./... in the pkg module: %v\n%s", err, stderr.String())
	}
	var pkgs []listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list ./... listed nothing, so there is no dependency face to check")
	}
	return pkgs
}

// find returns the listed package with the given import path.
func find(pkgs []listed, path string) (listed, bool) {
	for _, p := range pkgs {
		if p.ImportPath == path {
			return p, true
		}
	}
	return listed{}, false
}

// underLeaf reports whether path is the leaf package or sits under it: a
// library a leaf bridges is the leaf's whole subtree.
func underLeaf(path, leaf string) bool {
	return path == leaf || strings.HasPrefix(path, leaf+"/")
}

// importsLibrary reports whether imports holds libPath or one of its
// subpackages: a bridge that imports a subpackage still binds the library whole.
func importsLibrary(imports []string, libPath string) bool {
	return slices.ContainsFunc(imports, func(imp string) bool {
		return imp == libPath || strings.HasPrefix(imp, libPath+"/")
	})
}

// dependenciesOf lists the packages a `go list -deps` target reaches.
func dependenciesOf(t *testing.T, target string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s failed: %v\n%s", target, err, out)
	}
	reached := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if pkg := strings.TrimSpace(line); pkg != "" {
			reached[pkg] = true
		}
	}
	if len(reached) == 0 {
		t.Fatalf("go list -deps %s listed nothing, so there is no dependency face to check", target)
	}
	return reached
}

// stdlibPackage reports whether a package path names a package of the standard
// library, whose first path element never carries a dot.
func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
