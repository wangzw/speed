package observability_test

import (
	"os/exec"
	"strings"
	"testing"
)

// repoPrefix is this repository's own module path. Every package of it is
// inside every budget here.
const repoPrefix = "github.com/vislake/speed/"

// exportedUpstream is what the interface package re-exports: the two API
// packages and the two SDK packages whose exporter interfaces it names. Their
// closure is what naming them costs, and it is the whole of what the interface
// package may reach upstream.
var exportedUpstream = []string{
	"go.opentelemetry.io/otel/trace",
	"go.opentelemetry.io/otel/metric",
	"go.opentelemetry.io/otel/sdk/trace",
	"go.opentelemetry.io/otel/sdk/metric",
}

// TestTheInterfacePackageReachesNoBackendLibrary is the executable form of the
// split the module's documentation draws: the interface package carries the
// types and the two helpers, and every transport a backend binds belongs to the
// subpackage that binds it.
//
// Nothing holds that split at compile time. The whole capability is one Go
// module, so its dependency list carries every backend whichever one a host
// deploys on, and the split is a rule about imports. This is the gate that keeps
// it: a host that imports only the interface package must not end up compiling
// gRPC, and the difference between the two would otherwise be invisible until
// someone read the dependency graph.
//
// The budget is the standard library, this repository's modules, and the
// closure of the four upstream packages named above. It does not distinguish an
// alias from ordinary use, because that is not a property a dependency scan can
// see: what it can see is whether a backend's transport is in the graph at all.
func TestTheInterfacePackageReachesNoBackendLibrary(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: this package re-exports the upstream API and the
	// SDK exporter interfaces, so a listing that lost them came from a load
	// that did not happen, and every assertion below would pass on an empty
	// list.
	for _, pkg := range []string{"go.opentelemetry.io/otel/trace", "go.opentelemetry.io/otel/sdk/metric"} {
		if !root[pkg] {
			t.Fatalf("the dependency list does not hold %s, which this package re-exports: what is "+
				"checked below is not this package's dependency face", pkg)
		}
	}

	budget := map[string]bool{}
	for _, pkg := range exportedUpstream {
		for reached := range dependenciesOf(t, pkg) {
			budget[reached] = true
		}
	}
	// The negative control: if the budget itself carried a backend's transport,
	// the loop below could not tell a package that reaches one from a package
	// that does not.
	for _, backend := range []string{"google.golang.org/grpc", "google.golang.org/protobuf"} {
		if budget[backend] {
			t.Fatalf("the budget includes %s, so this check cannot catch a package that reaches it", backend)
		}
	}

	for pkg := range root {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), budget[pkg]:
			continue
		}
		t.Errorf("the interface package depends on %s. Naming the upstream API and SDK types costs their "+
			"closure and nothing besides: a transport belongs to the export backend that binds it, so "+
			"that a host compiling this package carries no transport but the ones it deploys on", pkg)
	}
}

// TestTheAssemblerReachesNoBackendTransport is the same rule one level in. The
// assembler builds the providers and hands them exporters collected from the
// registry, so it knows no backend — and a host that takes it in together with
// its own choice of destination must not have been made to compile the others.
func TestTheAssemblerReachesNoBackendTransport(t *testing.T) {
	deps := dependenciesOf(t, "./otel")
	if !deps[repoPrefix+"pkg/observability"] {
		t.Fatalf("the dependency list does not hold the interface package: a listing without it came from " +
			"a load that did not happen")
	}
	for _, backend := range []string{
		"google.golang.org/grpc",
		"google.golang.org/protobuf",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
		"go.opentelemetry.io/otel/exporters/stdout/stdouttrace",
	} {
		if deps[backend] {
			t.Errorf("the assembler depends on %s. It collects exporters through the registry and knows "+
				"no destination: reaching one here would make every host compile the backends it did "+
				"not choose", backend)
		}
	}
}

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
