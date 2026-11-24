package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

const guardedEndpoint = "public"

type hostOptions struct {
	roster    tenant.Catalog
	extractor string
	responder speedhttp.ErrorResponder
	register  func(reg *core.Registry, ep speedhttp.Endpoint) error
}

type host struct {
	addr string
	logs *recorder
}

func startHost(t *testing.T, opts hostOptions) *host {
	t.Helper()
	extractor := opts.extractor
	if extractor == "" {
		extractor = HeaderExtractor
	}

	logs := newRecorder()
	served := make(chan struct{})

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(loadReader(t, extractor), config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "stand-in.catalog",
		Provides: []core.Provision{{Token: (*tenant.Catalog)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return opts.roster, nil },
	})
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInLogger{logs: logs}, nil },
	})
	if opts.responder != nil {
		reg.Register(core.Module{
			Name:     "stand-in.responder",
			Provides: []core.Provision{{Token: (*speedhttp.ErrorResponder)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return opts.responder, nil },
		})
	}
	reg.Register(core.Module{
		Name:     "registrant",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			if opts.register == nil {
				return nil
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint(guardedEndpoint)
			if err != nil {
				return err
			}
			return opts.register(reg, ep)
		},
		Serve: func(context.Context, *core.Registry, any) error {
			close(served)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
	}()
	limit := waitLimit()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(limit):
			t.Errorf("the assembly did not stop within %s", limit)
		}
	})

	select {
	case <-served:
	case <-done:
		t.Fatalf("the assembly ended before it served: %v", runErr)
	case <-time.After(limit):
		t.Fatalf("the assembly did not reach Serve within %s", limit)
	}

	listening := logs.withMessage("an HTTP listener is listening")
	if len(listening) != 1 {
		t.Fatalf("the entry point wrote %d records of the address it bound, want 1: %+v", len(listening), listening)
	}
	return &host{addr: listening[0].attrs["address"], logs: logs}
}

func loadReader(t *testing.T, extractor string) config.Reader {
	t.Helper()
	document := fmt.Sprintf(`{
		"http": {"listeners": {%q: {"address": "127.0.0.1:0"}}, "endpoints": {%q: {"listener": %q}}},
		"tenant": {"resolve": {"endpoints": [%q], "extractor": %q}}
	}`, guardedEndpoint, guardedEndpoint, guardedEndpoint, guardedEndpoint, extractor)

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "TENANTRESOLVETEST", DefaultLocator: "file:" + path}},
	})
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: Module().Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

type response struct {
	status int
	header nethttp.Header
	body   string
}

func (h *host) send(t *testing.T, method, path string, header map[string]string) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, "http://"+h.addr+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := &nethttp.Client{
		Timeout:   waitLimit(),
		Transport: &nethttp.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response to %s %s: %v", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func (h *host) get(t *testing.T, path, tenantName string) response {
	t.Helper()
	var header map[string]string
	if tenantName != "" {
		header = map[string]string{"X-Tenant": tenantName}
	}
	return h.send(t, nethttp.MethodGet, path, header)
}

func requireProblem(t *testing.T, got response, status int) {
	t.Helper()
	if got.status != status {
		t.Fatalf("the response is %d, want %d (body %q)", got.status, status, got.body)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("the response's media type is %q, want application/problem+json", ct)
	}
	var members map[string]any
	if err := json.Unmarshal([]byte(got.body), &members); err != nil {
		t.Fatalf("the response body %q is not a JSON object: %v", got.body, err)
	}
	want := map[string]any{
		"type":   "about:blank",
		"title":  nethttp.StatusText(status),
		"status": float64(status),
	}
	if len(members) != len(want) {
		t.Fatalf("the response body is %v, want exactly %v", members, want)
	}
	for k, v := range want {
		if members[k] != v {
			t.Fatalf("the response body is %v, want %s = %v", members, k, v)
		}
	}
}

type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type recorder struct {
	mu      *sync.Mutex
	records *[]record
	bound   []slog.Attr
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, records: &[]record{}}
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.bound)+rec.NumAttrs())
	for _, a := range r.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.records = append(*r.records, record{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := append(append([]slog.Attr(nil), r.bound...), attrs...)
	return &recorder{mu: r.mu, records: r.records, bound: bound}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) all() []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record(nil), *r.records...)
}

func (r *recorder) withMessage(prefix string) []record {
	var out []record
	for _, rec := range r.all() {
		if strings.HasPrefix(rec.msg, prefix) {
			out = append(out, rec)
		}
	}
	return out
}

type standInLogger struct{ logs *recorder }

func (l standInLogger) Named(name string) *slog.Logger {
	return slog.New(l.logs.WithAttrs([]slog.Attr{slog.String("logger", name)}))
}

func (standInLogger) Redaction() log.Redaction { return noRedaction{} }

var _ log.Logger = standInLogger{}

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

type fakeRoster struct {
	byName map[string]tenant.Tenant
	byID   map[tenant.ID]tenant.Tenant
	err    error
}

func (f fakeRoster) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	if f.err != nil {
		return tenant.Tenant{}, false, f.err
	}
	t, ok := f.byName[name]
	return t, ok, nil
}

func (f fakeRoster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	if f.err != nil {
		return tenant.Tenant{}, false, f.err
	}
	t, ok := f.byID[id]
	return t, ok, nil
}

func (fakeRoster) Put(context.Context, tenant.Tenant) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) Create(context.Context, tenant.Tenant) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) Delete(context.Context, tenant.ID) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) SetOwner(context.Context, tenant.ID, string) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) SetOwnerIfEmpty(context.Context, tenant.ID, string) (bool, error) {
	return false, errors.New("not used by the resolution layer")
}

func (fakeRoster) SetProfile(context.Context, tenant.ID, tenant.Profile) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) List(context.Context) ([]tenant.Tenant, error) {
	return nil, errors.New("not used by the resolution layer")
}

func (fakeRoster) Enter(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, errors.New("not used by the resolution layer")
}

func (fakeRoster) EnterRegistered(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, errors.New("not used by the resolution layer")
}

func standardRoster() fakeRoster {
	tenants := []tenant.Tenant{
		{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
		{ID: "t_paused", Name: "paused", Status: tenant.StatusDisabled},
		{ID: "t_deleted", Name: "deleted", Status: tenant.StatusDeleted},
		{ID: "system", Name: "platform", Status: tenant.StatusActive},
	}
	byName := make(map[string]tenant.Tenant, len(tenants))
	byID := make(map[tenant.ID]tenant.Tenant, len(tenants))
	for _, t := range tenants {
		byName[t.Name] = t
		byID[t.ID] = t
	}
	return fakeRoster{byName: byName, byID: byID}
}
