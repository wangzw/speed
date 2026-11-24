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

var (
	_ WriteGuard  = (*recordingWriteGuard)(nil)
	_ Provisioner = (*recordingProvisioner)(nil)
)

// TestTheHostSeamsKeepTheDeclaredSignatures checks that the WriteGuard and
// Provisioner method signatures stay as declared.
func TestTheHostSeamsKeepTheDeclaredSignatures(t *testing.T) {
	takesWriteGuardAnswer((*recordingWriteGuard)(nil).Denied)
	takesProvision((*recordingProvisioner)(nil).Provision)
}

func takesWriteGuardAnswer(func(context.Context, ID, WriteOp) (bool, error)) {}
func takesProvision(func(context.Context, Tenant, string) error)             {}

type recordingWriteGuard struct {
	denied bool
}

func (g *recordingWriteGuard) Denied(_ context.Context, _ ID, _ WriteOp) (bool, error) {
	return g.denied, nil
}

type recordingProvisioner struct {
	seen bool
}

func (p *recordingProvisioner) Provision(_ context.Context, _ Tenant, _ string) error {
	p.seen = true
	return nil
}

// TestTheWriteOperationsAreDistinctAndTheZeroValueIsTheDestructiveOne checks that
// the two write operations differ and the zero value is WriteDisable.
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

// TestTheProvisionerTakesTheCreatorAsAPlainIdentifier checks that Provisioner is
// driven with the creator as a plain string.
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

// TestTheRosterDoesNotConsultTheWriteSeams checks that the roster package never
// references WriteGuard, so SetStatusByAccount stays unguarded.
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

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}
