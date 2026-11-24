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

// TestName checks that name.
func TestName(t *testing.T) {
	if got := parser(t).Name(); got != "json" {
		t.Errorf("Name() = %q, want %q", got, "json")
	}
}

// TestNestedObject checks that nested object.
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

// TestNonObjectTopLevelRejected checks that non object top level rejected.
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

// TestNullDocumentIsEmpty checks that null document is empty.
func TestNullDocumentIsEmpty(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte(`null`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Unmarshal() = %#v, want an empty mapping", got)
	}
}

// TestDuplicateKeyRejected checks that duplicate key rejected.
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

// TestTrailingDataRejected checks that trailing data rejected.
func TestTrailingDataRejected(t *testing.T) {
	for _, data := range []string{`{"a": 1} {"a": 2}`, `{"a": 1} x`, `{"a": 1}]`} {
		if got, err := parser(t).Unmarshal([]byte(data)); err == nil {
			t.Errorf("Unmarshal(%s) = %#v, want an error for the data after the document", data, got)
		}
	}
}

// TestKeysAndValuesSurviveTheWalk checks that keys and values survive the walk.
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

type memorySource struct{ data string }

func (memorySource) Scheme() string { return "mem" }

func (s memorySource) Fetch(context.Context, *url.URL) ([]byte, string, error) {
	return []byte(s.data), "json", nil
}

// TestIntegersKeepEveryDigit checks that integers keep every digit.
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
	if got, err := load(`{"counters": {"n": 1e3}}`); err != nil || got != 1000 {
		t.Errorf("1e3 decoded to %d with %v, want 1000", got, err)
	}
}

// TestMalformedDocumentRejected checks that malformed document rejected.
func TestMalformedDocumentRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte(`{"a": }`)); err == nil {
		t.Fatal("Unmarshal() succeeded on malformed JSON, want an error")
	}
}

// TestModuleNameIsTheRegisteredName checks that module name is the registered name.
func TestModuleNameIsTheRegisteredName(t *testing.T) {
	if got := json.Module().Name; got != json.ModuleName {
		t.Errorf("the module registers as %q and exports %q as its name", got, json.ModuleName)
	}
	if _, registered := core.ProcessRegistry.Lookup(json.ModuleName); !registered {
		t.Errorf("importing the package registers no module named %q", json.ModuleName)
	}
}

// TestRegistersAsFormatResource checks that registers as format resource.
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

// TestJSONFormatAddsNoParsingLibrary checks that JSON format adds no parsing library.
func TestJSONFormatAddsNoParsingLibrary(t *testing.T) {
	reached := dependenciesOf(t, ".")
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

const (
	repoPrefix  = "github.com/vislake/speed/"
	rootPackage = repoPrefix + "pkg/config"
)

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
