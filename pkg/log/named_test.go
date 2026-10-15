package log

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// namingProduct stands in for the product this module builds, so a test can see
// the name NamedFor asks for and read the record it hands back. Named returns a
// logger over a buffer carrying the module attribute, the way the configured
// product does.
type namingProduct struct {
	buf   bytes.Buffer
	names []string
}

func (p *namingProduct) Named(name string) *slog.Logger {
	p.names = append(p.names, name)
	return slog.New(slog.NewTextHandler(&p.buf, nil)).With(ModuleAttrKey, name)
}

func (p *namingProduct) Redaction() Redaction { return processRedaction }

var _ Logger = (*namingProduct)(nil)

// TestNamedForNamesTheLoggerAfterTheModule pins the half that names, and pins it
// on the record rather than on the call: the logger it hands back writes records
// carrying the module name, and the name it asked the instance for is the one
// it was given.
func TestNamedForNamesTheLoggerAfterTheModule(t *testing.T) {
	product := &namingProduct{}
	reg := core.New()
	reg.Register(Module())
	reg.Supply(product, ModuleName)
	driveTo(t, reg, core.StageNew)

	logger, err := NamedFor(reg, "orders")
	if err != nil {
		t.Fatalf("NamedFor failed with the capability present: %v", err)
	}
	if logger == nil {
		t.Fatal("NamedFor returned no logger with the capability present")
	}

	logger.Info("a record")

	if out := product.buf.String(); !strings.Contains(out, ModuleAttrKey+"=orders") {
		t.Errorf("the record through the returned logger is %q, want it to carry %s=orders",
			out, ModuleAttrKey)
	}
	if len(product.names) != 1 || product.names[0] != "orders" {
		t.Errorf("the instance's Named was called with %v, want [orders]", product.names)
	}
}

// TestNamedForReportsAMissingCapability pins the other half: with no module
// delivering the logging capability, the error is the lookup's own and
// errors.Is reaches core.ErrMissingProvider, so a caller reads the absence the
// way every other optional dependency's absence is read. No logger comes back
// with it.
func TestNamedForReportsAMissingCapability(t *testing.T) {
	reg := core.New()

	logger, err := NamedFor(reg, "orders")
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("with no provider NamedFor returned %v, want core.ErrMissingProvider", err)
	}
	if logger != nil {
		t.Errorf("with no provider NamedFor returned the logger %v, want none", logger)
	}
}
