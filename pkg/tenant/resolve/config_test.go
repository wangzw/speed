package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// readThrough loads a JSON document through the real configuration path and
// hands back a reader this module's own section decodes from.
//
// The module's own declaration is what is exercised: prepare has to fail on a
// key nobody wrote, and the route that finds the first one is the real decode.
// A stub reader would prove the struct tags match the stub instead.
//
// Load is that path without a lifecycle behind it, and a reader is the whole of
// what these cases want.
func readThrough(t *testing.T, document string) config.Reader {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "TENANTTEST", DefaultLocator: "file:" + path}},
	})
	// The declaration under test reaches the load as a registered module's
	// resource, which is the only way one ever reaches it.
	reg.Register(core.Module{Name: "reader", Resources: []any{schema()}})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// TestPrepareStatesEnabled is the stance a configured module takes. It states
// StateEnabled rather than StateAuto because it delivers its capability
// exclusively: StateAuto is the cell resolution disables when a second provider
// claims the capability, and this module must fail that startup instead of
// giving way.
func TestPrepareStatesEnabled(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": ["public"], "extractor": "header"}}
	}`))
	if err != nil {
		t.Fatalf("prepare over a complete configuration: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}

// TestPrepareFailsWithoutTheEndpointList pins the rule that replaced the
// stand-down. A module taken in at run level has to start, so a required key
// nobody wrote is a startup failure carried out of Prepare, not a stance that
// disables the module and lets the run go on. Under the old behaviour prepare
// answered StateDisabled with a reason and a nil error, and this test failed on
// the nil error alone.
func TestPrepareFailsWithoutTheEndpointList(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"extractor": "header"}}
	}`))
	if err == nil {
		t.Fatalf("prepare with no endpoint list = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Fatalf("the failure does not name the key that is missing: %v", err)
	}
}

// TestPrepareFailsWithoutTheExtractor is the other required key, reached the
// same way. The two are told apart by name because they point at different
// lines to write.
func TestPrepareFailsWithoutTheExtractor(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": ["public"]}}
	}`))
	if err == nil {
		t.Fatalf("prepare with no extractor = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+extractorItem) {
		t.Fatalf("the failure does not name the key that is missing: %v", err)
	}
}

// TestPrepareFailsOnAnEmptyEndpointList covers the same fact by the route the
// decoder does not take: a list written as empty is given, so the decoder
// accepts it and prepare is what refuses it. A decoder that never saw the key
// raises its own failure instead, which the case above pins.
func TestPrepareFailsOnAnEmptyEndpointList(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": [], "extractor": "header"}}
	}`))
	if err == nil {
		t.Fatalf("prepare with an empty endpoint list = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Fatalf("the failure does not name the key that is empty: %v", err)
	}
}

// TestPrepareFailsOnAnEmptyExtractorName is the extractor half of the route
// above: a name written as the empty string is a value the decoder accepts and
// this module refuses, because nothing selects it.
func TestPrepareFailsOnAnEmptyExtractorName(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": ["public"], "extractor": ""}}
	}`))
	if err == nil {
		t.Fatalf("prepare with an empty extractor name = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+extractorItem) {
		t.Fatalf("the failure does not name the key whose value is empty: %v", err)
	}
}

// TestPrepareTellsTheTwoAbsencesApart pins the distinction the design draws
// between the two ways Prepare can find nothing: a registry the host never
// handed a reader, and a section missing a key. The first is the host's
// omission and carries core's ErrMissingProvider out as it stands; the second
// is this module's own configuration and names the key. Merging the two would
// point the operator at the wrong end.
func TestPrepareTellsTheTwoAbsencesApart(t *testing.T) {
	stance, err := Module().Prepare(t.Context(), core.New())
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("prepare over a registry with no reader = %v, want ErrMissingProvider", err)
	}
	if stance != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", stance)
	}
}
