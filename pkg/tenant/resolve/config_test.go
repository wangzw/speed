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
	reg.Register(core.Module{Name: "reader", Resources: []any{schema()}})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

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
	if n := strings.Count(err.Error(), ConfigNamespace); n != 1 {
		t.Fatalf("the failure names %s %d times, want once: %v", ConfigNamespace, n, err)
	}
}

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
