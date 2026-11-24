package sms_test

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

const smsRootPath = moduleImportPath + "/sms"

const authnPrefix = moduleImportPath + "/authn"

const (
	aliyunSDKPath     = "github.com/alibabacloud-go/dysmsapi-20170525/v5"
	aliyunSDKVersion  = "v5.6.0"
	aliyunCarrierLeaf = smsRootPath + "/aliyun"

	tencentSDKPath     = "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms"
	tencentSDKVersion  = "v1.3.172"
	tencentCarrierLeaf = smsRootPath + "/tencent"

	awsSDKPath     = "github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	awsSDKVersion  = "v1.42.8"
	awsCarrierLeaf = smsRootPath + "/aws"

	twilioSDKPath     = "github.com/twilio/twilio-go"
	twilioSDKVersion  = "v1.33.1"
	twilioCarrierLeaf = smsRootPath + "/twilio"
)

// TestRootClosureHoldsNoAuthnAndNoVendorSDK checks that the root package reaches neither the authentication tree nor a vendor SDK.
func TestRootClosureHoldsNoAuthnAndNoVendorSDK(t *testing.T) {
	root := dependenciesOf(t, ".")
	if !root[moduleImportPath+"/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

	withinBudget := map[string]bool{}
	for _, pkg := range []string{moduleImportPath + "/config", moduleImportPath + "/core", moduleImportPath + "/log"} {
		for reached := range dependenciesOf(t, pkg) {
			withinBudget[reached] = true
		}
	}

	for pkg := range root {
		if pkg == authnPrefix || strings.HasPrefix(pkg, authnPrefix+"/") {
			t.Errorf("the root package depends on %s: the sms capability must not reach into the "+
				"authentication tree", pkg)
			continue
		}
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, moduleImportPath+"/"), withinBudget[pkg]:
			continue
		}
		t.Errorf("the root package depends on %s. Reading configuration costs pkg/config and its own "+
			"closure, and nothing besides: a vendor SDK belongs to the carrier subpackage that binds "+
			"it, so that a host compiling this package carries no carrier but the one it deploys on", pkg)
	}
}

type carrierSDK struct {
	path string
	leaf string
}

var carrierSDKs = []carrierSDK{
	{path: aliyunSDKPath, leaf: aliyunCarrierLeaf},
	{path: tencentSDKPath, leaf: tencentCarrierLeaf},
	{path: awsSDKPath, leaf: awsCarrierLeaf},
	{path: twilioSDKPath, leaf: twilioCarrierLeaf},
}

// TestEachCarrierSDKIsConfinedToItsLeaf checks that each vendor SDK appears in
// the non-test files of its own leaf package and nowhere else.
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
	{path: aliyunSDKPath, version: aliyunSDKVersion},
	{path: tencentSDKPath, version: tencentSDKVersion},
	{path: awsSDKPath, version: awsSDKVersion},
	{path: twilioSDKPath, version: twilioSDKVersion},
}

// TestEachCarrierSDKIsPinnedToTheTableVersion checks that pkg/go.mod states the table version for each carrier SDK and no other.
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

// TestTheVersionPinFailsOnADriftedRequirement checks that a version above, below or missing is reported.
func TestTheVersionPinFailsOnADriftedRequirement(t *testing.T) {
	pin := carrierPin{path: aliyunSDKPath, version: aliyunSDKVersion}
	line := func(version string) string { return "\t" + aliyunSDKPath + " " + version + "\n" }
	if v := pinViolation(line(aliyunSDKVersion), pin); v != "" {
		t.Errorf("a go.mod at the pinned version is reported: %q", v)
	}
	for _, gomod := range []string{line("v5.7.0"), line("v5.5.0"), ""} {
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
