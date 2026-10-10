package http

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
)

// registryReport is what testdata/rootimport reports about the process it runs
// in.
type registryReport struct {
	Modules []string `json:"modules"`
	Routers []string `json:"routers"`
	// Exclusive says whether the module delivering Router claims it
	// exclusively.
	Exclusive bool `json:"exclusive"`
}

// TestAProgramImportingOnlyTheRootPackageHasTheModuleRegistered starts a program
// whose only import from this module is the root package, and reads the process
// registry that program holds.
//
// This package registers its module when it is initialised, so a host that
// imports it can take the module into a command of its tree by the name
// ModuleName, and no registration step of its own comes between the import and
// the name. Registered is not enabled: importing the package opens no port, and
// a module only takes part in a run when a command names it.
//
// The test binary's registry could not answer this by itself: it holds
// whatever the test files of this package import beside the package, and a
// reading of it cannot tell this package's own registration from one a test
// file caused. The program puts the observation back on the host the statement
// is about.
func TestAProgramImportingOnlyTheRootPackageHasTheModuleRegistered(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run := exec.Command("go", "run", "./testdata/rootimport")
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("running the program whose only import from this module is the root package: %v\n%s",
			err, stderr.String())
	}

	var report registryReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("the program reported %q, which is not the JSON this criterion reads: %v",
			stdout.String(), err)
	}
	if len(report.Modules) == 0 {
		t.Fatalf("the program read an empty registry (%q), so its report is no observation of what "+
			"importing this package registers", stdout.String())
	}
	if !slices.Contains(report.Modules, ModuleName) {
		t.Errorf("the program's registry holds %v, and no module there is named %q: importing this "+
			"package registers nothing a host can name", report.Modules, ModuleName)
	}
	if want := []string{ModuleName}; !slices.Equal(report.Routers, want) {
		t.Errorf("the modules delivering Router in the program's registry are %v, want %v: this "+
			"package's module is the one entry point of its kind a program that imports it holds",
			report.Routers, want)
	}
	if !report.Exclusive {
		t.Error("the registered module delivers Router without the exclusive claim")
	}
}
