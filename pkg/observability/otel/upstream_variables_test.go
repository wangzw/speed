package otel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var leftToUpstream = map[string]string{
	"OTEL_SERVICE_NAME":                      "the service name, when service-name is not configured",
	"OTEL_RESOURCE_ATTRIBUTES":               "the resource's own attributes",
	"OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT":      "a span limit",
	"OTEL_ATTRIBUTE_COUNT_LIMIT":             "a span limit",
	"OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT": "a span limit",
	"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT":        "a span limit",
	"OTEL_SPAN_EVENT_COUNT_LIMIT":            "a span limit",
	"OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT":       "a span limit",
	"OTEL_SPAN_LINK_COUNT_LIMIT":             "a span limit",
	"OTEL_LINK_ATTRIBUTE_COUNT_LIMIT":        "a span limit",
	"OTEL_METRICS_EXEMPLAR_FILTER":           "the exemplar filter",
	"OTEL_GO_X_CARDINALITY_LIMIT":            "the cardinality limit",
}

var upstreamSources = []struct{ module, file string }{
	{"go.opentelemetry.io/otel/sdk", "trace/sampler_env.go"},
	{"go.opentelemetry.io/otel/sdk", "trace/internal/env/env.go"},
	{"go.opentelemetry.io/otel/sdk", "resource/env.go"},
	{"go.opentelemetry.io/otel/sdk/metric", "env.go"},
	{"go.opentelemetry.io/otel/sdk/metric", "config.go"},
}

var variableName = regexp.MustCompile(`^OTEL_[A-Z0-9_]*[A-Z0-9]$`)

// TestNoUpstreamVariableIsLeftOffTheOverriddenList pins overriddenVariables to
// the upstream SDK version this module builds against.
func TestNoUpstreamVariableIsLeftOffTheOverriddenList(t *testing.T) {
	read := map[string]bool{}
	for _, src := range upstreamSources {
		for name := range stringLiterals(t, filepath.Join(moduleDir(t, src.module), src.file)) {
			if variableName.MatchString(name) {
				read[name] = true
			}
		}
	}
	if !read["OTEL_SERVICE_NAME"] {
		t.Fatalf("the upstream sources name no OTEL_SERVICE_NAME, so the scan read nothing: %v", read)
	}

	listed := map[string]bool{}
	for _, v := range overriddenVariables {
		listed[v.name] = true
		if !read[v.name] {
			t.Errorf("%s is on the overridden list and the upstream sources no longer read it", v.name)
		}
		if _, ok := leftToUpstream[v.name]; ok {
			t.Errorf("%s is both overridden and left to the upstream", v.name)
		}
		only := func(name string) (string, bool) {
			if name == v.name {
				return "set", true
			}
			return "", false
		}
		if got := ignoredVariables(only); len(got) != 1 || got[0] != v.name+": "+v.item {
			t.Errorf("with %s alone set the warning names %q, want it and %s alone", v.name, got, v.item)
		}
	}
	for name := range read {
		if _, left := leftToUpstream[name]; !listed[name] && !left {
			t.Errorf("the upstream reads %s, which is neither on the overridden list nor left to the "+
				"upstream: decide which it is", name)
		}
	}
}

func moduleDir(t *testing.T, module string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -m %s failed: %v\n%s", module, err, out)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("go list -m %s gave no directory: the module is not downloaded", module)
	}
	return dir
}

func stringLiterals(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	found := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				found[s] = true
			}
		}
		return true
	})
	return found
}
