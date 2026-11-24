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
	mfatestPath      = rootPath + "/mfa/mfatest"
	mfaPackagePath   = rootPath + "/mfa"
	factorStorePath  = rootPath + "/mfa/internal/factorstore"
	totpSeedPath     = rootPath + "/mfa/internal/totpseed"
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
// non-test file: the root package, the gateway, the login engine and authntest.
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
	// not it builds under this GOOS, GOARCH and set of build tags. Test imports
	// are not included.
	Imports []string
}

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

func readImports(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, spec := range f.Imports {
		out = append(out, strings.Trim(spec.Path.Value, "\"`"))
	}
	return out, nil
}

func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

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

// moduleRootFrom finds the module that holds source, falling back to the module
// of the working directory when -trimpath recorded no directory.
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
// Every non-test file is parsed for its imports, including files a build
// constraint leaves out; standard library packages are not parsed.
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

// listModule lists every package of the module at dir, including directories
// that go list matches with no pattern because every file is for another
// platform or under a custom tag.
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
// are the standard library and the four value-type packages.
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

// authntestViolations is the third rule: no package imports authntest,
// mounttest or mfatest in a non-test file, except those packages themselves and
// mounttest, which may import authntest.
func authntestViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if p.ImportPath != authntestPath && p.ImportPath != mounttestPath && slices.Contains(p.Imports, authntestPath) {
			out = append(out, p.ImportPath+" imports authntest outside a test file")
		}
		if p.ImportPath != mounttestPath && slices.Contains(p.Imports, mounttestPath) {
			out = append(out, p.ImportPath+" imports mounttest outside a test file")
		}
		if p.ImportPath != mfatestPath && slices.Contains(p.Imports, mfatestPath) {
			out = append(out, p.ImportPath+" imports mfatest outside a test file")
		}
	}
	slices.Sort(out)
	return out
}

// internalStoreImporters are the packages that may import the multi-factor
// module's internal statement packages in a non-test file: the module itself,
// the TOTP factor and the test-support package.
var internalStoreImporters = map[string][]string{
	factorStorePath: {mfaPackagePath, mfatestPath},
	totpSeedPath:    {totpPackagePath, mfatestPath},
}

// internalStoreViolations is the seventh rule: no other package imports a
// multi-factor module's internal statement package in a non-test file.
func internalStoreViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		for store, allowed := range internalStoreImporters {
			if slices.Contains(allowed, p.ImportPath) {
				continue
			}
			if slices.Contains(p.Imports, store) {
				out = append(out, p.ImportPath+" imports "+store+" outside a test file: only "+strings.Join(allowed, ", ")+" may")
			}
		}
	}
	slices.Sort(out)
	return out
}

// identityWriterViolations is the fourth rule: no package outside
// identityWriters imports the identity package in a non-test file.
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

// unusedWriterViolations is the other direction of the fourth rule: a listed
// package that exists but imports no identity package in a non-test file is
// reported; one that is not in the listing at all is not.
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

func isTenant(path string) bool {
	return path == tenantPath || strings.HasPrefix(path, tenantPath+"/")
}

func inAuthnTree(path string) bool {
	return path == rootPath || strings.HasPrefix(path, rootPath+"/")
}

// tenantViolations is the fifth rule: no package of the authn tree imports or
// reaches the tenant module through a package outside the tree, over every
// non-test file it holds.
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
// nothing else.
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
// through the imports of every non-test file, over the listing of the module.
func closure(pkgs []listed) []string { return reachable(pkgs, rootPath) }

// closureViolations is the transitive form of the first rule, over the closure
// of the root package.
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

// TestTheRootPackageImportsOnlyWhatItMay holds that the root package's non-test
// imports are the standard library and the four value-type packages.
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
	if p, ok := find(pkgs, mfatestPath); !ok || len(p.Imports) == 0 {
		t.Fatal("the listing does not hold mfatest and its imports, so the rule about who may import it has nothing to read")
	}
	for _, want := range []struct{ path, store string }{
		{mfaPackagePath, factorStorePath},
		{totpPackagePath, totpSeedPath},
		{mfatestPath, factorStorePath},
		{mfatestPath, totpSeedPath},
	} {
		if p, ok := find(pkgs, want.path); !ok || !slices.Contains(p.Imports, want.store) {
			t.Fatalf("%s is not listed as importing %s, so the rule about who may import the internal stores has nothing to read", want.path, want.store)
		}
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
	for _, v := range internalStoreViolations(pkgs) {
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

// TestTheRootPackageClosureHoldsNoThirdPartyPackage holds that the root
// package's closure holds no third-party package.
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

// TestTheModuleRootIsFoundFromAnyDirectory holds that the module root is found
// from any working directory.
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

// TestTheModuleRootIsFoundFromTrimmedSourcePaths holds that a source path
// trimmed to a module-qualified name still finds the module.
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

// violatingTree writes a module shaped like pkg in which each dependency rule
// is broken once, and returns its directory.
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
		"authn/mfa/mfatest/mfatest.go":        "package mfatest\n\nimport _ \"" + rootPath + "\"\n",
		"authn/mfa/mfa.go":                    "package mfa\n\nimport _ \"" + totpSeedPath + "\"\n",
		"authn/mfa/totp/totp.go":              "package totp\n\nimport _ \"" + factorStorePath + "\"\n",
		"authn/mfa/webauthn/webauthn.go":      "package webauthn\n\nimport _ \"" + factorStorePath + "\"\n",
		"authn/mfa/sms/sms.go":                "package sms\n\nimport _ \"" + totpSeedPath + "\"\n",
		"usesmfatest/usesmfatest.go":          "package usesmfatest\n\nimport _ \"" + mfatestPath + "\"\n",
		"testsmfatest/testsmfatest.go":        "package testsmfatest\n",
		"testsmfatest/testsmfatest_test.go":   "package testsmfatest\n\nimport _ \"" + mfatestPath + "\"\n",
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

var isolated = []string{"GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off"}

// TestTheDependencyRulesFailOnAViolatingTree runs each rule over a tree that
// breaks it and asks for the failure, so the rules cannot pass vacuously.
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
		want := []string{"onlytagged", "platform", "production", "tagged", "usesmfatest", "usesmounttest"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: a test file's import is allowed, authntest's own import of the root "+
				"is not an import of itself, a file another platform or a custom build tag leaves out of the build "+
				"is still a file that imports it, and mfatest is held the same way", names, want)
		}
	})
	t.Run("a factor package outside the owners imports an internal store", func(t *testing.T) {
		var names []string
		for _, v := range internalStoreViolations(pkgs) {
			name, _, _ := strings.Cut(v, " ")
			names = append(names, strings.TrimPrefix(name, moduleImportPath+"/"))
		}
		want := []string{"authn/mfa", "authn/mfa/sms", "authn/mfa/totp", "authn/mfa/webauthn"}
		if !slices.Equal(names, want) {
			t.Errorf("violations name %q, want %q: the module, the TOTP factor and mfatest may import the internal "+
				"stores, and another factor package may not; two of the names are a package the module owns "+
				"importing the other store, which shows the two lists are kept apart rather than merged", names, want)
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

// TestTheDependencyRulesPassOnACleanTree is the control: a clean tree reports
// no violation.
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
		"authn/mfa/mfatest/mfatest.go":        "package mfatest\n\nimport _ \"" + rootPath + "\"\n",
		"testonly/mfatestonly_test.go":        "package testonly\n\nimport _ \"" + mfatestPath + "\"\n",
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
		"stores":    internalStoreViolations(pkgs),
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

// TestTheListOfWritersIsHeldToTheCodeBehindIt holds the writer rule's other
// direction: an existing listed package that does not write is reported.
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

// The token bridge is the only non-test importer of the JOSE library, no
// non-test file names the claims-without-verification escape hatch, and
// pkg/go.mod requires at least the version fixed here.
const (
	goJosePath       = "github.com/go-jose/go-jose/v4"
	goJoseFloor      = "v4.1.5"
	tokenPackagePath = rootPath + "/token"
	unsafeClaims     = "UnsafeClaimsWithoutVerification"
)

// Each third-party library is bound to one leaf package, so a host that imports
// none of the leaves carries none of the libraries. Their versions are pinned in
// pinnedLibraries below.
const (
	otpPath         = "github.com/pquerna/otp"
	otpVersion      = "v1.5.0"
	totpPackagePath = rootPath + "/mfa/totp"

	webauthnPath        = "github.com/go-webauthn/webauthn"
	webauthnVersion     = "v0.18.2"
	webauthnPackagePath = rootPath + "/mfa/webauthn"

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
	xCryptoVersion  = "v0.59.0"
	xTextPath       = "golang.org/x/text"
	xTextVersion    = "v0.44.0"
)

// bridgeLibrary is one third-party library the module binds to a single leaf
// package: the leaf package whose non-test files, and its subpackages, are the
// only ones that may import it.
type bridgeLibrary struct {
	path string
	leaf string
}

// bridgeLibraries are the libraries held to a leaf package by the package name,
// so the leaf's own files may grow without the guard changing. The JOSE bridge
// is held separately by file, in TestTheTokenBridgeIsTheOnlyGoJoseUserAndTheFloorIsHeld.
var bridgeLibraries = []bridgeLibrary{
	{path: otpPath, leaf: totpPackagePath},
	{path: webauthnPath, leaf: webauthnPackagePath},
	{path: gosaml2Path, leaf: samlPackagePath},
	{path: goxmldsigPath, leaf: samlPackagePath},
}

// pinnedLibrary is one third-party library fixed to an exact version, which
// pkg/go.mod must state and no other.
type pinnedLibrary struct {
	path    string
	version string
}

// pinnedLibraries are the libraries the module requires at an exact version
// rather than at a floor: pkg/go.mod must state exactly that version for each,
// and a missing requirement is reported too.
var pinnedLibraries = []pinnedLibrary{
	{path: goOIDCPath, version: goOIDCVersion},
	{path: gosaml2Path, version: gosaml2Version},
	{path: otpPath, version: otpVersion},
	{path: webauthnPath, version: webauthnVersion},
	{path: scsPath, version: scsVersion},
	{path: argon2idPath, version: argon2idVersion},
	{path: uuidPath, version: uuidVersion},
	{path: xCryptoPath, version: xCryptoVersion},
	{path: xTextPath, version: xTextVersion},
}

// The OIDC and OAuth 2.0 bridges bind their library to more than one leaf:
// go-oidc's version is pinned in pinnedLibraries and x/oauth2 is held at or
// above a floor in floorLibraries.
const (
	goOIDCPath         = "github.com/coreos/go-oidc/v3"
	goOIDCVersion      = "v3.21.0"
	xOAuth2Path        = "golang.org/x/oauth2"
	xOAuth2Floor       = "v0.37.0"
	googlePackagePath  = rootPath + "/login/google"
	githubPackagePath  = rootPath + "/login/github"
	ssoOIDCPackagePath = rootPath + "/sso/oidc"
)

// multiLeafBridge is one third-party library bound to a set of leaf packages:
// each of them, and its subpackages, may import it and no other package may.
type multiLeafBridge struct {
	path   string
	leaves []string
}

// multiLeafLibraries are the libraries more than one package bridges, each held
// to the leaves that bridge it.
var multiLeafLibraries = []multiLeafBridge{
	{path: goOIDCPath, leaves: []string{googlePackagePath, ssoOIDCPackagePath}},
	{path: xOAuth2Path, leaves: []string{googlePackagePath, githubPackagePath, ssoOIDCPackagePath}},
}

// floorLibrary is one third-party library held at or above a version floor
// because it carries a security advisory below that floor.
type floorLibrary struct {
	path  string
	floor string
}

// floorLibraries are the libraries held at or above a floor rather than pinned:
// x/oauth2 and goxmldsig here. go-jose has its own test.
var floorLibraries = []floorLibrary{
	{path: xOAuth2Path, floor: xOAuth2Floor},
	{path: goxmldsigPath, floor: goxmldsigFloor},
}

// floorViolation reports, if any, how pkg/go.mod fails to hold lib at or above
// its floor: a version below it, and a missing requirement, are reported.
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

// libraryConfinementViolations reports the packages that import libPath in a
// non-test file but are not one of the leaf packages that bind it, nor under
// one of them.
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

func underAnyLeaf(path string, leaves []string) bool {
	for _, leaf := range leaves {
		if path == leaf || strings.HasPrefix(path, leaf+"/") {
			return true
		}
	}
	return false
}

// libraryImporters lists every non-test file of the module that imports libPath
// or one of its subpackages, as slash-separated paths relative to the module
// root, sorted.
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
// claims-without-verification escape hatch; a mention in a comment is not one.
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

// checks that the token bridge is the only non-test importer of go-jose and
// that pkg/go.mod holds the library at or above its floor.
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
// pinned for lib: another version, above or below, and a missing requirement
// are both reported.
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

// TestEachBridgeLibraryIsConfinedToItsLeaf holds each bridge library to the
// non-test imports of its own leaf package.
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

// TestEachPinnedLibraryStatesTheTableVersion holds that pkg/go.mod states the
// exact pinned version of every pinned library.
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

// TestTheVersionPinFailsOnADriftedRequirement holds that a version above the
// pinned one, below it, or a missing requirement is reported.
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

// TestTheBridgeConfinementFailsOnAViolatingTree holds that the confinement rule
// reports an import from outside the leaf.
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

// TestTheGuardImportRuleFailsOnAViolatingTree holds that the guard rule reports
// a factor leaf import.
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

// TestTheMultiLeafBridgesAreConfinedToTheirLeaves holds each multi-leaf library
// to the leaves that bridge it.
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

// TestTheFloorFailsBelowIt holds that a version below the floor, or a missing
// requirement, is reported.
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

// TestTheMultiLeafConfinementFailsOnAViolatingTree holds that an import from
// outside the leaves is reported.
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

// TestTheRootPackageDoesNotImportTheSendingCapability holds that the root
// package does not reach the sending capability.
func TestTheRootPackageDoesNotImportTheSendingCapability(t *testing.T) {
	const smsCapabilityPath = moduleImportPath + "/sms"
	pkgs := listModule(t, moduleRoot(t), nil)
	root, ok := find(pkgs, rootPath)
	if !ok {
		t.Fatal("the listing does not hold the root package")
	}
	if slices.Contains(root.Imports, smsCapabilityPath) {
		t.Errorf("the root package imports %s", smsCapabilityPath)
	}
}
