package observability_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const repoPrefix = "github.com/vislake/speed/"

var exportedUpstream = []string{
	"go.opentelemetry.io/otel/trace",
	"go.opentelemetry.io/otel/metric",
	"go.opentelemetry.io/otel/sdk/trace",
	"go.opentelemetry.io/otel/sdk/metric",
}

// TestTheInterfacePackageReachesNoBackendLibrary is the executable form
// of the split the module's documentation draws.
func TestTheInterfacePackageReachesNoBackendLibrary(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: the package re-exports the upstream API and the SDK
	// exporter interfaces, so a listing that lost them came from a failed load.
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

var assemblerUpstream = []string{
	"go.opentelemetry.io/otel/attribute",
	"go.opentelemetry.io/otel/codes",
	"go.opentelemetry.io/otel/propagation",
	"go.opentelemetry.io/otel/sdk/metric/metricdata",
	"go.opentelemetry.io/otel/sdk/resource",
}

const ginPackage = "github.com/gin-gonic/gin"

const otelGinPackage = "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

// TestTheAssemblerStaysInsideItsDependencyBudget is the same rule one
// level in.
func TestTheAssemblerStaysInsideItsDependencyBudget(t *testing.T) {
	imports := importsOf(t, "./otel")
	// The positive controls: the layer is an otelgin handler on the interface
	// package, so a listing without any of them came from a failed load.
	for _, pkg := range []string{ginPackage, otelGinPackage, repoPrefix + "pkg/observability"} {
		if !imports[pkg] {
			t.Fatalf("the import list does not hold %s, which the assembler imports: what is checked "+
				"below is not its import list", pkg)
		}
	}

	budget := map[string]bool{}
	for _, root := range slices.Concat(exportedUpstream, assemblerUpstream, []string{ginPackage, otelGinPackage}) {
		for reached := range dependenciesOf(t, root) {
			budget[reached] = true
		}
	}
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
			"made of. It binds the upstream API and SDK, and gin and otelgin for the request layer: "+
			"anything else is a dependency every host that takes the assembler in would compile", pkg)
	}
}

// backendTransports are the packages a destination's transport is made of, which
// only the export backend that binds it may reach.
var backendTransports = []string{
	"google.golang.org/grpc",
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace",
}

// TestTheAssemblerReachesNoBackendTransport holds the split over everything the
// assembler reaches, this repository's modules included.
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

func dependenciesOf(t *testing.T, target string) map[string]bool {
	t.Helper()
	return listPackages(t, "-deps", target)
}

func importsOf(t *testing.T, target string) map[string]bool {
	t.Helper()
	return listPackages(t, "-f", "{{join .Imports \"\\n\"}}", target)
}

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
