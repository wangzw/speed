package json_test

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/config/format/json"
	"github.com/vislake/speed/pkg/core"
)

// parser builds the format the way the configuration module gets it, out of
// the module's resources, so the tests exercise what a host actually ends up
// with rather than a value this package hands itself.
func parser(t *testing.T) config.Format {
	t.Helper()
	reg := core.New()
	reg.Register(json.Module())
	found := core.Resources[config.Format](reg)
	if len(found) != 1 {
		t.Fatalf("the module declares %d Format resources, want exactly 1", len(found))
	}
	return found[0].Value
}

func TestName(t *testing.T) {
	if got := parser(t).Name(); got != "json" {
		t.Errorf("Name() = %q, want %q", got, "json")
	}
}

// TestNestedObject pins the shape the conversion layer is written against:
// nested objects become nested map[string]any and numbers arrive as float64.
func TestNestedObject(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte(`{"cache":{"redis":{"pool-size":4,"addr":"localhost:6379"}}}`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{
		"cache": map[string]any{
			"redis": map[string]any{
				"pool-size": float64(4),
				"addr":      "localhost:6379",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal() = %#v, want %#v", got, want)
	}
}

// TestNonObjectTopLevelRejected covers the documents that parse as JSON and
// still cannot be a configuration: the top level carries the keys, so anything
// but a mapping has nowhere to put them.
func TestNonObjectTopLevelRejected(t *testing.T) {
	for name, data := range map[string]string{
		"array":  `[1, 2]`,
		"string": `"cache"`,
		"number": `7`,
		"bool":   `true`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parser(t).Unmarshal([]byte(data))
			if err == nil {
				t.Fatalf("Unmarshal() = %#v, want an error for a %s at the top level", got, name)
			}
			if !strings.Contains(err.Error(), "top level") {
				t.Errorf("err = %v, want the text to say where the problem is", err)
			}
		})
	}
}

// TestNullDocumentIsEmpty fixes the reading that a source carrying nothing is
// a legal run rather than a failure: the layers below it decide every value.
func TestNullDocumentIsEmpty(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte(`null`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Unmarshal() = %#v, want an empty mapping", got)
	}
}

func TestMalformedDocumentRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte(`{"a": }`)); err == nil {
		t.Fatal("Unmarshal() succeeded on malformed JSON, want an error")
	}
}

// TestRegistersAsFormatResource proves the blank-import contract: importing
// the package puts the parser in the process registry, where the
// configuration module finds it by resource type alone.
func TestRegistersAsFormatResource(t *testing.T) {
	var found []core.Resource[config.Format]
	for _, res := range core.Resources[config.Format](core.ProcessRegistry) {
		if res.Module == "config.format.json" {
			found = append(found, res)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the process registry holds %d Format resources from config.format.json, want 1", len(found))
	}
	if got := found[0].Value.Name(); got != "json" {
		t.Errorf("the registered parser answers for %q, want %q", got, "json")
	}
}

// TestJSONFormatAddsNoParsingLibrary is the executable form of what splitting
// the two parsers buys: a host that configures itself in JSON does not carry
// the YAML parser, which shares a go.mod with this package and is one import
// away from arriving unnoticed.
//
// The budget is the standard library, this repository's modules, and the
// closure of the root package. This package delivers its parser as a resource
// of that package and is reached from it, so whatever reading a configuration
// costs is already spent by the time anybody gets here; that cost is recorded
// in ADR-config-parsing-2026-09-20. What is checked is the increment: parsing
// JSON adds nothing on top of it.
//
// The budget is computed rather than written out, so that the root package
// exchanging one of its own dependencies does not turn into an edit here.
func TestJSONFormatAddsNoParsingLibrary(t *testing.T) {
	reached := dependenciesOf(t, ".")
	// The positive control. This package is in its own dependency listing,
	// and a listing that lost it came from a load that did not happen --
	// every judgement below would then pass over nothing.
	if !reached[rootPackage+"/format/json"] {
		t.Fatalf("the dependency listing does not hold %s/format/json, so what it read is "+
			"not this package's dependency face", rootPackage)
	}

	withinBudget := dependenciesOf(t, rootPackage)
	for pkg := range reached {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), withinBudget[pkg]:
			continue
		}
		t.Errorf("the json format package depends on %s, which is neither the standard "+
			"library, nor a module of this repository, nor part of what reading "+
			"configuration through the root package costs. Parsing JSON is encoding/json's "+
			"work, and a parsing library landing here would reach every host that reads a "+
			"JSON configuration", pkg)
	}
}

// repoPrefix is this repository's own module path, and rootPackage the
// configuration package this one registers itself with.
const (
	repoPrefix  = "github.com/vislake/speed/"
	rootPackage = repoPrefix + "pkg/config"
)

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
