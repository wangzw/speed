package yaml_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/config/format/yaml"
	"github.com/vislake/speed/pkg/core"
)

func parser(t *testing.T) config.Format {
	t.Helper()
	reg := core.New()
	reg.Register(yaml.Module())
	found := core.Resources[config.Format](reg)
	if len(found) != 1 {
		t.Fatalf("the module declares %d Format resources, want exactly 1", len(found))
	}
	return found[0].Value
}

// TestName checks that name.
func TestName(t *testing.T) {
	if got := parser(t).Name(); got != "yaml" {
		t.Errorf("Name() = %q, want %q", got, "yaml")
	}
}

// TestNestedMapping checks that nested mapping.
func TestNestedMapping(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte("cache:\n  redis:\n    pool-size: 4\n    ttl: 5m\n    tls: true\n"))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{
		"cache": map[string]any{
			"redis": map[string]any{
				"pool-size": 4,
				"ttl":       "5m",
				"tls":       true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal() = %#v, want %#v", got, want)
	}
}

// TestSequenceOfMappings checks that sequence of mappings.
func TestSequenceOfMappings(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte("endpoints:\n  - addr: a:1\n  - addr: b:2\n"))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{"endpoints": []any{
		map[string]any{"addr": "a:1"},
		map[string]any{"addr": "b:2"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal() = %#v, want %#v", got, want)
	}
}

// TestTopLevelNonStringKeyRejected checks that top level non string key rejected.
func TestTopLevelNonStringKeyRejected(t *testing.T) {
	_, err := parser(t).Unmarshal([]byte("1: a\n"))
	if err == nil {
		t.Fatal("Unmarshal() succeeded on a numeric key, want an error")
	}
	if !strings.Contains(err.Error(), "!!int") {
		t.Errorf("err = %v, want the text to name what the key resolved to", err)
	}
}

// TestNestedNonStringKeyRejected checks that nested non string key rejected.
func TestNestedNonStringKeyRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte("a:\n  1: x\n")); err == nil {
		t.Fatal("Unmarshal() succeeded on a nested numeric key, want an error")
	}
}

// TestQuotedNumericKeyAccepted checks that quoted numeric key accepted.
func TestQuotedNumericKeyAccepted(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte("\"1\": x\n"))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"1": "x"}) {
		t.Errorf("Unmarshal() = %#v, want the quoted key kept as a string", got)
	}
}

// TestAliasResolved checks that alias resolved.
func TestAliasResolved(t *testing.T) {
	got, err := parser(t).Unmarshal([]byte("base: &b\n  addr: a:1\nprimary: *b\n"))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	want := map[string]any{
		"base":    map[string]any{"addr": "a:1"},
		"primary": map[string]any{"addr": "a:1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal() = %#v, want %#v", got, want)
	}
}

// TestNonStringKeyInsideAliasRejected checks that non string key inside alias rejected.
func TestNonStringKeyInsideAliasRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte("base: &b\n  1: x\nprimary: *b\n")); err == nil {
		t.Fatal("Unmarshal() succeeded on a numeric key reached through an alias, want an error")
	}
}

// TestUndefinedAliasRejected checks that undefined alias rejected.
func TestUndefinedAliasRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte("primary: *missing\n")); err == nil {
		t.Fatal("Unmarshal() succeeded on an undefined alias, want an error")
	}
}

// TestSelfReferentialAliasRejected checks that self referential alias rejected.
func TestSelfReferentialAliasRejected(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := parser(t).Unmarshal([]byte("a: &x\n  b: *x\n"))
		done <- err
	}()
	if err := <-done; err == nil {
		t.Fatal("Unmarshal() succeeded on an anchor that holds itself, want an error")
	}
}

// TestMergeKeyRejected checks that merge key rejected.
func TestMergeKeyRejected(t *testing.T) {
	_, err := parser(t).Unmarshal([]byte("base: &b\n  addr: a:1\nprimary:\n  <<: *b\n  port: 2\n"))
	if err == nil {
		t.Fatal("Unmarshal() succeeded on a merge key, want an error")
	}
	if !strings.Contains(err.Error(), "<<") {
		t.Errorf("err = %v, want the text to name the merge key it refused", err)
	}
}

// TestMultipleDocumentsRejected checks that multiple documents rejected.
func TestMultipleDocumentsRejected(t *testing.T) {
	_, err := parser(t).Unmarshal([]byte("a: 1\n---\nb: 2\n"))
	if err == nil {
		t.Fatal("Unmarshal() succeeded on a two-document stream, want an error")
	}
	if !strings.Contains(err.Error(), "document") {
		t.Errorf("err = %v, want the text to say what it refused", err)
	}
}

// TestDuplicateKeyRejected checks that duplicate key rejected.
func TestDuplicateKeyRejected(t *testing.T) {
	for name, data := range map[string]string{
		"section":   "app:\n  level: info\n  port: 1\napp:\n  level: debug\n",
		"nested":    "app:\n  level: info\n  level: debug\n",
		"quoted":    "level: info\n\"level\": debug\n",
		"aliased":   "name: &k level\nsection:\n  level: info\n  *k : debug\n",
		"in a list": "endpoints:\n  - addr: a:1\n    addr: b:2\n",
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

// TestEmptyDocumentIsEmptyMapping checks that empty document is empty mapping.
func TestEmptyDocumentIsEmptyMapping(t *testing.T) {
	for name, data := range map[string]string{
		"nothing":      "",
		"comment only": "# nothing here\n",
		"null":         "null\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parser(t).Unmarshal([]byte(data))
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Unmarshal() = %#v, want an empty mapping", got)
			}
		})
	}
}

// TestNonMappingTopLevelRejected checks that non mapping top level rejected.
func TestNonMappingTopLevelRejected(t *testing.T) {
	for name, data := range map[string]string{
		"sequence": "- 1\n- 2\n",
		"scalar":   "cache\n",
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

// TestMalformedDocumentRejected checks that malformed document rejected.
func TestMalformedDocumentRejected(t *testing.T) {
	if _, err := parser(t).Unmarshal([]byte("a: [1, 2\n")); err == nil {
		t.Fatal("Unmarshal() succeeded on malformed YAML, want an error")
	}
}

// TestModuleNameIsTheRegisteredName checks that module name is the registered name.
func TestModuleNameIsTheRegisteredName(t *testing.T) {
	if got := yaml.Module().Name; got != yaml.ModuleName {
		t.Errorf("the module registers as %q and exports %q as its name", got, yaml.ModuleName)
	}
	if _, registered := core.ProcessRegistry.Lookup(yaml.ModuleName); !registered {
		t.Errorf("importing the package registers no module named %q", yaml.ModuleName)
	}
}

// TestRegistersAsFormatResource checks that registers as format resource.
func TestRegistersAsFormatResource(t *testing.T) {
	var found []core.Resource[config.Format]
	for _, res := range core.Resources[config.Format](core.ProcessRegistry) {
		if res.Module == "config.format.yaml" {
			found = append(found, res)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the process registry holds %d Format resources from config.format.yaml, want 1", len(found))
	}
	if got := found[0].Value.Name(); got != "yaml" {
		t.Errorf("the registered parser answers for %q, want %q", got, "yaml")
	}
}
