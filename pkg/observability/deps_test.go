package observability_test

import (
	"os/exec"
	"slices"
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

// assemblerUpstream is what the assembler binds beyond the four packages the
// interface package re-exports: the API and SDK packages it builds the providers
// and the request instrument with. The budget holds each of them with its
// closure.
var assemblerUpstream = []string{
	"go.opentelemetry.io/otel/attribute",
	"go.opentelemetry.io/otel/codes",
	"go.opentelemetry.io/otel/propagation",
	"go.opentelemetry.io/otel/sdk/metric/metricdata",
	"go.opentelemetry.io/otel/sdk/resource",
}

// ginPackage is the package the request layer is written against: the layer is
// a gin.HandlerFunc, so the assembler imports it, and its closure is part of the
// assembler's budget.
const ginPackage = "github.com/gin-gonic/gin"

// TestTheAssemblerStaysInsideItsDependencyBudget is the same rule one level in.
// The assembler's budget is the interface package's, the library packages it
// binds itself, and the current closure of gin. The closure is read from the
// module graph and not written down, so a gin release that reaches further moves
// the budget with it, while a package the assembler starts importing that is in
// none of these closures is a failure.
//
// What is compared is the assembler's own import list, which is the package-level
// declaration. The third-party packages this repository's own modules reach are
// those modules' business and are held by their tests.
func TestTheAssemblerStaysInsideItsDependencyBudget(t *testing.T) {
	imports := importsOf(t, "./otel")
	// The positive controls: the layer is a gin handler and the assembler sits on
	// the interface package, so a listing without either came from a load that did
	// not happen, and every assertion below would pass on an empty list.
	for _, pkg := range []string{ginPackage, repoPrefix + "pkg/observability"} {
		if !imports[pkg] {
			t.Fatalf("the import list does not hold %s, which the assembler imports: what is checked "+
				"below is not its import list", pkg)
		}
	}

	budget := map[string]bool{}
	for _, root := range slices.Concat(exportedUpstream, assemblerUpstream, []string{ginPackage}) {
		for reached := range dependenciesOf(t, root) {
			budget[reached] = true
		}
	}
	// The negative control: if the budget carried a backend's transport, the loop
	// below could not tell an assembler that imports one from one that does not.
	for _, backend := range backendTransports {
		if budget[backend] {
			t.Fatalf("the budget includes %s, so this check cannot catch an assembler that imports it", backend)
		}
	}

	for pkg := range imports {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), budget[pkg]:
			continue
		}
		t.Errorf("the assembler imports %s, which is in the closure of none of the packages its budget is "+
			"made of. It binds the upstream API and SDK, and gin for the request layer: anything else "+
			"is a dependency every host that takes the assembler in would compile", pkg)
	}
}

// backendTransports are the packages a destination's transport is made of, which
// only the export backend that binds it may reach.
var backendTransports = []string{
	"google.golang.org/grpc",
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace",
}

// TestTheAssemblerReachesNoBackendTransport holds the same split over everything
// the assembler reaches, this repository's own modules included. The assembler
// builds the providers and hands them exporters collected from the registry, so
// it knows no backend — and a host that takes it in together with its own choice
// of destination must not have been made to compile the others.
//
// The protobuf runtime is not among them: gin binds it for request bodies, so it
// is in the assembler's closure whichever destination a host chooses.
func TestTheAssemblerReachesNoBackendTransport(t *testing.T) {
	deps := dependenciesOf(t, "./otel")
	if !deps[repoPrefix+"pkg/observability"] {
		t.Fatalf("the dependency list does not hold the interface package: a listing without it came from " +
			"a load that did not happen")
	}
	for _, backend := range backendTransports {
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
	return listPackages(t, "-deps", target)
}

// importsOf lists the packages a target imports itself, test files left out.
func importsOf(t *testing.T, target string) map[string]bool {
	t.Helper()
	return listPackages(t, "-f", "{{join .Imports \"\\n\"}}", target)
}

// listPackages runs `go list` with the arguments given and reads one package
// path from each line it prints.
func listPackages(t *testing.T, args ...string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	listed := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if pkg := strings.TrimSpace(line); pkg != "" {
			listed[pkg] = true
		}
	}
	if len(listed) == 0 {
		t.Fatalf("go list %s listed nothing, so there is no dependency face to check", strings.Join(args, " "))
	}
	return listed
}

// stdlibPackage reports whether a package path names a package of the standard
// library, whose first path element never carries a dot.
func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
