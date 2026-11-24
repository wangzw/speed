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

const moduleImportPath = "github.com/vislake/speed/pkg"

const mailRootPath = moduleImportPath + "/mail"

var allowedRootPackages = []string{
	moduleImportPath + "/config",
	moduleImportPath + "/core",
	moduleImportPath + "/internal/literal",
	moduleImportPath + "/log",
}

const (
	smtpSDKPath     = "github.com/wneessen/go-mail"
	smtpSDKVersion  = "v0.8.2"
	smtpCarrierLeaf = mailRootPath + "/smtp"
)

// TestRootClosureHoldsOnlyTheDeclaredPackages checks that the root package reaches
// only the standard library, its allowlisted packages and their closures.
func TestRootClosureHoldsOnlyTheDeclaredPackages(t *testing.T) {
	root := dependenciesOf(t, ".")
	if !root[moduleImportPath+"/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

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

type carrierSDK struct {
	path string
	leaf string
}

var carrierSDKs = []carrierSDK{
	{path: smtpSDKPath, leaf: smtpCarrierLeaf},
}

// TestEachCarrierSDKIsConfinedToItsLeaf checks that each carrier SDK appears only
// in its own leaf package.
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

type carrierPin struct {
	path    string
	version string
}

var carrierPins = []carrierPin{
	{path: smtpSDKPath, version: smtpSDKVersion},
}

// TestEachCarrierSDKIsPinnedToTheTableVersion checks that go.mod states each
// carrier SDK's table version and no other.
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

// TestTheVersionPinFailsOnADriftedRequirement checks that a requirement above or
// below the table version, or absent, is reported.
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

func requiredVersion(gomod, path string) (string, bool) {
	for line := range strings.Lines(gomod) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == path {
			return fields[1], true
		}
	}
	return "", false
}

type listed struct {
	ImportPath string
	Standard   bool
	Dir        string
	Imports    []string
}

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

func find(pkgs []listed, path string) (listed, bool) {
	for _, p := range pkgs {
		if p.ImportPath == path {
			return p, true
		}
	}
	return listed{}, false
}

func underLeaf(path, leaf string) bool {
	return path == leaf || strings.HasPrefix(path, leaf+"/")
}

func importsLibrary(imports []string, libPath string) bool {
	return slices.ContainsFunc(imports, func(imp string) bool {
		return imp == libPath || strings.HasPrefix(imp, libPath+"/")
	})
}

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

func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
