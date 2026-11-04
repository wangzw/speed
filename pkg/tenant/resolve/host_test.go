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
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases that drive the resolution layer stand on a real assembly: the entry
// point module itself, listening on a port the operating system picks, with
// this module's own descriptor registered beside it. What the layer answers is
// read off a socket, so the routing, the response shape and the chain order are
// the entry point's and not a stand-in's. The roster, the logger and the
// responder are stand-ins for the modules that deliver those capabilities.

// waitLimit bounds every wait in these cases, so that a startup which never
// reaches Serve fails the case with a message instead of the whole binary at the
// global timeout.
const waitLimit = 10 * time.Second

// guardedEndpoint is the one endpoint every assembly declares and the layer
// guards.
const guardedEndpoint = "public"

// hostOptions is what one assembly is built from.
type hostOptions struct {
	// roster answers the resolution layer's lookups.
	roster tenant.Catalog
	// extractor is the extractor the configuration selects. Empty selects the
	// header extractor.
	extractor string
	// responder, when set, is delivered as the ErrorResponder capability, so
	// that it decides the shape of every error response.
	responder speedhttp.ErrorResponder
	// register runs in the Init callback of a module that depends on the entry
	// point, with the guarded endpoint. It is where a case puts its routes and
	// its own layers, the way a real registrant does.
	register func(reg *core.Registry, ep speedhttp.Endpoint) error
}

// host is one assembly that is serving.
type host struct {
	addr string
	logs *recorder
}

// startHost builds an assembly from opts and runs it until the entry point is
// serving. The run is ended when the case is, and a run that ends with an error
// fails the case.
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
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
	})

	select {
	case <-served:
	case <-done:
		t.Fatalf("the assembly ended before it served: %v", runErr)
	case <-time.After(waitLimit):
		t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
	}

	listening := logs.withMessage("an HTTP listener is listening")
	if len(listening) != 1 {
		t.Fatalf("the entry point wrote %d records of the address it bound, want 1: %+v", len(listening), listening)
	}
	return &host{addr: listening[0].attrs["address"], logs: logs}
}

// loadReader loads the configuration this assembly runs on through the real
// loader: the entry point declares one endpoint on a port of the system's
// choosing, and this module guards it with the extractor the case names.
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
	// The declarations reach the load as the resources of registered modules,
	// which is the only way one ever reaches it.
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: Module().Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// response is what a client got back.
type response struct {
	status int
	header nethttp.Header
	body   string
}

// send issues one request over a connection of its own and reads the response
// to its end.
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
		Timeout:   waitLimit,
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

// get is send for a GET carrying this tenant header. An empty name sends no
// tenant header at all.
func (h *host) get(t *testing.T, path, tenantName string) response {
	t.Helper()
	var header map[string]string
	if tenantName != "" {
		header = map[string]string{"X-Tenant": tenantName}
	}
	return h.send(t, nethttp.MethodGet, path, header)
}

// requireProblem asserts the default error shape: this status, the problem
// details media type, and a body holding exactly the members a default response
// writes for a Problem that carries a status and nothing else.
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

// record is one log record as a case reads it: the level, the message, and
// every attribute, bound or passed, rendered as text.
type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// recorder is a slog handler that keeps what it is handed. Handlers derived from
// it share its storage.
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

// withMessage returns the records written with a message that starts with
// prefix.
func (r *recorder) withMessage(prefix string) []record {
	var out []record
	for _, rec := range r.all() {
		if strings.HasPrefix(rec.msg, prefix) {
			out = append(out, rec)
		}
	}
	return out
}

// standInLogger delivers the Logger capability over a recorder. Every logger it
// hands out carries the name it was asked for as the attribute "logger", which
// is how a case tells the logger a request's context carries — the entry
// point's — from the one this module holds for itself.
type standInLogger struct{ logs *recorder }

func (l standInLogger) Named(name string) *slog.Logger {
	return slog.New(l.logs.WithAttrs([]slog.Attr{slog.String("logger", name)}))
}

func (standInLogger) Redaction() log.Redaction { return noRedaction{} }

var _ log.Logger = standInLogger{}

// noRedaction takes registrations and masks nothing: what these cases observe of
// the Logger capability is where records go, not what is masked.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

// fakeRoster answers ByAlias and Get from fixed tables, or with a fixed error.
// The resolution layer tries ByAlias and then Get, and the path guard calls
// both.
type fakeRoster struct {
	byAlias map[string]tenant.Tenant
	byID    map[tenant.ID]tenant.Tenant
	err     error
}

func (f fakeRoster) ByAlias(_ context.Context, alias string) (tenant.Tenant, bool, error) {
	if f.err != nil {
		return tenant.Tenant{}, false, f.err
	}
	t, ok := f.byAlias[alias]
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

func (fakeRoster) SetAlias(context.Context, tenant.ID, string) error {
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
		{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive},
		{ID: "t_paused", Alias: "paused", Status: tenant.StatusDisabled},
		{ID: "t_deleted", Alias: "deleted", Status: tenant.StatusDeleted},
		{ID: "system", Alias: "platform", Status: tenant.StatusActive},
	}
	byAlias := make(map[string]tenant.Tenant, len(tenants))
	byID := make(map[tenant.ID]tenant.Tenant, len(tenants))
	for _, t := range tenants {
		byAlias[t.Alias] = t
		byID[t.ID] = t
	}
	return fakeRoster{byAlias: byAlias, byID: byID}
}
