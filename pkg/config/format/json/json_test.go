package json_test

import (
	"context"
	"errors"
	"math"
	"net/url"
	"os/exec"
	"reflect"
	"strconv"
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

// TestName checks the format name the parser reports.
func TestName(t *testing.T) {
	if got := parser(t).Name(); got != "json" {
		t.Errorf("Name() = %q, want %q", got, "json")
	}
}

// TestNestedObject pins the shape the conversion layer is written against:
// nested objects become nested map[string]any, a number written as an integer
// arrives as an int, as it does from YAML, and any other number as a float64.
func TestNestedObject(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte(`{"cache":{"redis":{"pool-size":4,"ratio":0.5,"cap":1e3,"addr":"localhost:6379"}}}`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{
		"cache": map[string]any{
			"redis": map[string]any{
				"pool-size": 4,
				"ratio":     0.5,
				"cap":       float64(1000),
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

// TestDuplicateKeyRejected covers an object that gives one key twice.
func TestDuplicateKeyRejected(t *testing.T) {
	for name, data := range map[string]string{
		"section":   `{"app": {"level": "info", "port": 1}, "app": {"level": "debug"}}`,
		"nested":    `{"app": {"level": "info", "level": "debug"}}`,
		"escaped":   `{"level": "info", "level": "debug"}`,
		"in a list": `{"endpoints": [{"addr": "a:1", "addr": "b:2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parser(t).Unmarshal([]byte(data))
			if err == nil {
				t.Fatalf("Unmarshal() = %#v, want an error for a key given twice", got)
			}
			if !strings.Contains(err.Error(), "twice") {
				t.Errorf("err = %v, want the text to say the key is given twice", err)
			}
		})
	}
}

// TestTrailingDataRejected covers bytes after the document.
func TestTrailingDataRejected(t *testing.T) {
	for _, data := range []string{`{"a": 1} {"a": 2}`, `{"a": 1} x`, `{"a": 1}]`} {
		if got, err := parser(t).Unmarshal([]byte(data)); err == nil {
			t.Errorf("Unmarshal(%s) = %#v, want an error for the data after the document", data, got)
		}
	}
}

// TestKeysAndValuesSurviveTheWalk pins the tree for the shapes a config file is
// made of, so that the walk that detects duplicates hands on exactly what the
// document says.
func TestKeysAndValuesSurviveTheWalk(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte(`{"a": {"b": [1, "x", true, null, {"c": []}], "d": {}}, "e\"f": "g"}`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{
		"a": map[string]any{
			"b": []any{1, "x", true, nil, map[string]any{"c": []any{}}},
			"d": map[string]any{},
		},
		`e"f`: "g",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal() = %#v, want %#v", got, want)
	}
}

// memorySource hands the configuration module a fixed document as the primary
// source, so a case can load it the way a host does.
type memorySource struct{ data string }

func (memorySource) Scheme() string { return "mem" }

func (s memorySource) Fetch(context.Context, *url.URL) ([]byte, string, error) {
	return []byte(s.data), "json", nil
}

// TestIntegersKeepEveryDigit pins that an integer a JSON document writes is
// the integer a module decodes.
func TestIntegersKeepEveryDigit(t *testing.T) {
	type counters struct{ N int64 }
	load := func(document string) (int64, error) {
		t.Helper()
		reg := core.New()
		reg.Register(json.Module())
		reg.Register(core.Module{Name: "host", Resources: []any{
			config.HostIdentity{DefaultLocator: "mem:///config.json"},
			memorySource{data: document},
		}})
		reg.Register(core.Module{Name: "counters", Resources: []any{config.Schema{
			Namespace: "counters",
			Mounts:    []config.Mount{{Value: &counters{}}},
		}}})
		r, err := config.Load(t.Context(), reg, nil)
		if err != nil {
			return 0, err
		}
		var got counters
		err = r.Decode("counters", &got)
		return got.N, err
	}

	for _, want := range []int64{9007199254740993, math.MaxInt64, math.MinInt64} {
		got, err := load(`{"counters": {"n": ` + strconv.FormatInt(want, 10) + `}}`)
		if err != nil {
			t.Fatalf("loading %d failed: %v", want, err)
		}
		if got != want {
			t.Errorf("the document wrote %d and the module decoded %d", want, got)
		}
	}
	if got, err := load(`{"counters": {"n": 9223372036854775808}}`); !errors.Is(err, config.ErrTypeMismatch) {
		t.Errorf("a value one past the int64 range decoded to %d with %v, want ErrTypeMismatch", got, err)
	}
	// A number written with a fraction or an exponent is still a number an
	// integer field takes when it is whole.
	if got, err := load(`{"counters": {"n": 1e3}}`); err != nil || got != 1000 {
		t.Errorf("1e3 decoded to %d with %v, want 1000", got, err)
	}
}

// TestMalformedDocumentRejected checks that a malformed document is refused.
func TestMalformedDocumentRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte(`{"a": }`)); err == nil {
		t.Fatal("Unmarshal() succeeded on malformed JSON, want an error")
	}
}

// TestModuleNameIsTheRegisteredName pins the constant a host writes on its
// command tree to name this module, as in IncludeDeclarations: it is the name
// the module registers under, so a rename changes both and every host that
// wrote the constant follows.
func TestModuleNameIsTheRegisteredName(t *testing.T) {
	if got := json.Module().Name; got != json.ModuleName {
		t.Errorf("the module registers as %q and exports %q as its name", got, json.ModuleName)
	}
	if _, registered := core.ProcessRegistry.Lookup(json.ModuleName); !registered {
		t.Errorf("importing the package registers no module named %q", json.ModuleName)
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

// TestJSONFormatAddsNoParsingLibrary is the executable form of what
// splitting the two parsers buys: a host that configures itself in JSON does
// not carry the YAML parser, which shares a go.mod with this package and is
// one import away from arriving unnoticed.
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
