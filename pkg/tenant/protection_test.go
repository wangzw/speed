package tenant

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The two host-supplied seams protect the roster from writes a host declares
// off-limits. The compiler is what holds their shape: a host that implements one
// with another signature fails to build against the interface, so these
// assertions are the executable statement of the declared surface.
var (
	_ WriteGuard  = (*recordingWriteGuard)(nil)
	_ Provisioner = (*recordingProvisioner)(nil)
)

// TestTheHostSeamsKeepTheDeclaredSignatures pins the two method shapes beside
// the interface delivery above: the write operation is a value of its own type
// and the creator is a plain string, not a type from another capability. Each
// method value has to be assignable to the declared function type, so a change
// to either signature fails to build here.
func TestTheHostSeamsKeepTheDeclaredSignatures(t *testing.T) {
	takesWriteGuardAnswer((*recordingWriteGuard)(nil).Denied)
	takesProvision((*recordingProvisioner)(nil).Provision)
}

func takesWriteGuardAnswer(func(context.Context, ID, WriteOp) (bool, error)) {}
func takesProvision(func(context.Context, Tenant, string) error)             {}

// recordingWriteGuard keeps the last write it was asked about.
type recordingWriteGuard struct {
	denied bool
}

func (g *recordingWriteGuard) Denied(_ context.Context, _ ID, _ WriteOp) (bool, error) {
	return g.denied, nil
}

// recordingProvisioner keeps the last tenant and creator it was handed.
type recordingProvisioner struct {
	seen bool
}

func (p *recordingProvisioner) Provision(_ context.Context, _ Tenant, _ string) error {
	p.seen = true
	return nil
}

// TestTheWriteOperationsAreDistinctAndTheZeroValueIsTheDestructiveOne holds the
// closed set this package declares: two operations, and the zero value lands on
// the disable. A value nobody wrote must read as the write protection exists to
// refuse, the same direction Status's and DisabledBy's zero values fall.
func TestTheWriteOperationsAreDistinctAndTheZeroValueIsTheDestructiveOne(t *testing.T) {
	var op WriteOp
	if op != WriteDisable {
		t.Errorf("the zero WriteOp is %d, want WriteDisable", int(op))
	}
	if WriteDelete == WriteDisable {
		t.Error("WriteDelete and WriteDisable are the same value")
	}
	if WriteDelete != WriteDisable+1 {
		t.Errorf("WriteDelete = %d, want %d, so the closed set has no gap", int(WriteDelete), int(WriteDisable)+1)
	}
}

// TestTheProvisionerTakesTheCreatorAsAPlainIdentifier holds the dependency the
// root package keeps: the first administrator is named by an account identifier,
// and the root package does not import the authentication capability to spell
// its type. The signature assertion above is the compile-time half; this case
// drives the interface through a value built by a package that has that import,
// to show the two agree at the call site.
func TestTheProvisionerTakesTheCreatorAsAPlainIdentifier(t *testing.T) {
	var p Provisioner = &recordingProvisioner{}
	creator := "operator-account"
	if err := p.Provision(context.Background(), Tenant{ID: "acme", Name: "acme"}, creator); err != nil {
		t.Fatalf("Provision returned %v", err)
	}
	if !p.(*recordingProvisioner).seen {
		t.Fatal("Provision did not receive the tenant")
	}
}

// TestTheRosterDoesNotConsultTheWriteSeams holds the boundary between the
// host's protection and the account lifecycle. Which tenants refuse an operator
// write is the management surface's question; SetStatusByAccount has to keep
// disabling a personal tenant when its account is disabled, so the roster's own
// package may not consult WriteGuard at all. A runtime case cannot observe an
// absence, so the scan reads the roster's identifiers: a refactor that put the
// check "once for every status write" fails here even though it compiles.
func TestTheRosterDoesNotConsultTheWriteSeams(t *testing.T) {
	roster := filepath.Join(packageDir(t), "catalog")
	entries, err := os.ReadDir(roster)
	if err != nil {
		t.Fatalf("reading %s: %v", roster, err)
	}
	names := map[string]bool{
		"WriteGuard":   true,
		"WriteOp":      true,
		"WriteDisable": true,
		"WriteDelete":  true,
		"Provisioner":  true,
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(roster, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if ok && names[ident.Name] {
				t.Errorf("%s names %s: the roster must not consult the host's write seams, so "+
					"SetStatusByAccount keeps disabling a personal tenant", name, ident.Name)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatalf("no roster source was scanned under %s", roster)
	}
}

// packageDir is this test file's directory, read from the file's own location so
// that the scan finds the roster wherever go test is run from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}
