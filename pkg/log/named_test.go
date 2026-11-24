package log

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

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

// TestNamedForNamesTheLoggerAfterTheModule checks that NamedFor returns a logger named orders
// through the supplied instance.
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

// TestNamedForReportsAMissingCapability checks that NamedFor with no provider returns
// ErrMissingProvider and no logger.
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
