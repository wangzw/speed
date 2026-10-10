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
	"github.com/vislake/speed/pkg/tenant"
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

// TestPrepareFailsWithoutTheEndpointList pins that a required key nobody wrote
// fails the startup. A module taken in at run level has to start, so the
// missing key is a failure carried out of Prepare, not a stance that disables
// the module and lets the run go on: a prepare answering StateDisabled with a
// reason and a nil error fails this case on the nil error alone.
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
// between the two ways Prepare can find nothing: a registry whose config module
// the host never supplied a reader to, and a section missing a key. The first is
// the host's omission and carries core's ErrMissingProvider out as it stands;
// the second is this module's own configuration and names the key. Merging the
// two would point the operator at the wrong end.
//
// The config module is registered and has no instance, which is what the
// omission leaves behind.
func TestPrepareTellsTheTwoAbsencesApart(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	stance, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("prepare over a registry with no reader = %v, want ErrMissingProvider", err)
	}
	if stance != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", stance)
	}
}

// TestPrepareFailsOnAnEndpointNamedTwice pins that the endpoint list is a set.
// The list is this module's own input, and the entry point's surface takes an
// endpoint by name without deduplicating, so a name written twice registers
// the resolution layer on that endpoint twice: the roster lookup and the
// identifier's binding to the logger would each run twice per request, and
// nothing would say the configuration was wrong. Other modules that read a
// listening-endpoint list handle a repeat differently: pkg/ratelimit and
// pkg/authn/gate refuse it at startup, pkg/observability/otel drops it and
// writes a line saying so, and pkg/audit/journal validates nothing and carries
// the list through, dropping the repeat in its unique. This module refuses it
// at startup.
func TestPrepareFailsOnAnEndpointNamedTwice(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": ["public", "public"], "extractor": "header"}}
	}`))
	if err == nil {
		t.Fatalf("prepare over an endpoint list naming public twice = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !errors.Is(err, tenant.ErrDuplicateEndpoint) {
		t.Fatalf("the failure is not this module's duplicate-endpoint sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), `"public"`) {
		t.Fatalf("the failure does not name the endpoint that is repeated: %v", err)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Fatalf("the failure does not name the key the repeat was written in: %v", err)
	}
	// The namespace appears once, as the prefix of the item path. Writing it
	// again ahead of the path spells "tenant.resolve: tenant.resolve.endpoints",
	// and the second copy says nothing the path does not already carry.
	if n := strings.Count(err.Error(), ConfigNamespace); n != 1 {
		t.Fatalf("the failure names %s %d times, want once: %v", ConfigNamespace, n, err)
	}
}

// TestPrepareAcceptsEachEndpointOnce is the other half of the rule above: a
// list of distinct names is what this module is for, and the duplicate check
// must not refuse the configuration it accepts the single entry of.
func TestPrepareAcceptsEachEndpointOnce(t *testing.T) {
	got, err := prepare(readThrough(t, `{
		"tenant": {"resolve": {"endpoints": ["public", "admin"], "extractor": "header"}}
	}`))
	if err != nil {
		t.Fatalf("prepare over two distinct endpoints: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}
