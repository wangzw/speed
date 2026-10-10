package otlp

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

// leftToUpstream are the suffixes of the standard variables the upstream
// exporters read for what build leaves to them, so they still apply and are
// not on overriddenVariables.
var leftToUpstream = map[string]string{
	"TIMEOUT":             "the per-request timeout",
	"TRACES_TIMEOUT":      "the per-request timeout",
	"METRICS_TIMEOUT":     "the per-request timeout",
	"COMPRESSION":         "the compression",
	"TRACES_COMPRESSION":  "the compression",
	"METRICS_COMPRESSION": "the compression",
}

// upstreamNamespace is what the upstream joins every suffix to.
const upstreamNamespace = "OTEL_EXPORTER_OTLP"

// upstreamSources are the files of the upstream exporters that name the
// standard variables they read, by module and path inside the module.
var upstreamSources = []struct{ module, file string }{
	{"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc", "internal/otlpconfig/envconfig.go"},
	{"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp", "internal/otlpconfig/envconfig.go"},
	{"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc", "internal/oconf/envconfig.go"},
	{"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp", "internal/oconf/envconfig.go"},
}

// variableSuffix is a literal shaped as a variable name or a suffix of one.
var variableSuffix = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// TestNoUpstreamVariableIsLeftOffTheOverriddenList pins overriddenVariables to
// the upstream exporters of the version this module builds against. Every
// variable named in the files that read them, the namespace joined to each
// suffix, is either on the list or left to the upstream, and every name on the
// list is one those files name, so an upgrade that adds, renames or drops a
// variable fails here until the list is brought in line. Each name on the list
// is also one the warning reports when it alone is set.
func TestNoUpstreamVariableIsLeftOffTheOverriddenList(t *testing.T) {
	read := map[string]bool{}
	for _, src := range upstreamSources {
		literals := stringLiterals(t, filepath.Join(moduleDir(t, src.module), src.file))
		// The positive control: a file without the namespace is not where the
		// upstream reads its variables, and its suffixes would mean nothing.
		if !literals[upstreamNamespace] {
			t.Fatalf("%s/%s does not name %s, so the scan read the wrong file", src.module, src.file,
				upstreamNamespace)
		}
		for s := range literals {
			if s != upstreamNamespace && variableSuffix.MatchString(s) {
				read[s] = true
			}
		}
	}

	listed := map[string]bool{}
	for _, v := range overriddenVariables {
		suffix, ok := strings.CutPrefix(v.name, upstreamNamespace+"_")
		if !ok {
			t.Errorf("%s is on the overridden list and is not under %s", v.name, upstreamNamespace)
			continue
		}
		listed[suffix] = true
		if !read[suffix] {
			t.Errorf("%s is on the overridden list and the upstream sources no longer read it", v.name)
		}
		if _, left := leftToUpstream[suffix]; left {
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
	for suffix := range read {
		if _, left := leftToUpstream[suffix]; !listed[suffix] && !left {
			t.Errorf("the upstream reads %s_%s, which is neither on the overridden list nor left to the "+
				"upstream: decide which it is", upstreamNamespace, suffix)
		}
	}
}

// moduleDir is the directory of a module in the build list.
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

// stringLiterals lists the string literals of a Go file.
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
