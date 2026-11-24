package gate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The cases built on this file run the real entry point on a loopback port,
// assembled by the real registry with the real gateway in front of it; nothing
// here stands in for the router.

// listeningMsg is the record the entry point writes once an endpoint's address
// is bound. It is the only place outside the process that says which port the
// operating system picked for an endpoint configured on port 0.
const listeningMsg = "an HTTP listener is listening"

func waitBound() time.Duration {
	return coretest.Scale(5 * time.Second)
}

type liveRoute struct {
	endpoint string
	method   string
	path     string
	// handler is what serves the route. It is unset for an exemption, and
	// for a route that wants no more than to answer with who reached it.
	handler gin.HandlerFunc
}

// liveAssembly is one drive of the real registry to Serve: the entry point module
// on a loopback port per endpoint, the gateway guarding those endpoints, each
// backend a module of its own, and a registrant making the routes a real one makes.
type liveAssembly struct {
	// endpoints are the entry point's endpoints. It defaults to a single
	// endpoint named "public".
	endpoints []string
	// guarded is what the gateway's endpoints key is written with. It defaults
	// to the key being absent, which guards every endpoint the entry point
	// declares; a list narrows the set to those.
	guarded []string
	// scope, when non-nil, is delivered as the tenant admission layer's scope
	// guard.
	scope authn.ScopeGuard
	// gateItems is further text for the gateway's own section, written as JSON
	// members without the braces, such as `"mount":{"endpoints":{"default":"admin"}}`.
	gateItems string
	// authnItems is further text for the authn section beside the gateway's,
	// written the same way, such as `"static":{"subjects":{...}}`.
	authnItems string
	// topItems is further text for the top level of the configuration, beside
	// the authn and http sections, written the same way, such as
	// `"tenant":{"resolve":{"endpoints":["public"]}}`.
	topItems     string
	backends     []authn.Verifier
	routes       []liveRoute
	exempt       []liveRoute
	optional     []liveRoute
	impersonable []liveRoute
	// modules take part beside the rest.
	modules []core.Module
	// inFlight is the gateway's in-flight width, the default when it is zero.
	inFlight int
}

// whoReached is the handler a route gets when a case does not give it one: it
// answers with who the request was made out to be and the access level the
// gateway recorded, so a case reads what got through from the body.
func whoReached(c *gin.Context) {
	who := "nobody"
	if id, ok := identity.From(c.Request.Context()); ok {
		who = id.Key()
	}
	c.String(nethttp.StatusOK, "reached as %s [%s]", who, accessName(identity.AccessFrom(c.Request.Context())))
}

func accessName(a authn.Access) string {
	switch a {
	case authn.AccessOpen:
		return "open"
	case authn.AccessOptional:
		return "optional"
	case authn.AccessRequired:
		return "required"
	}
	return "unknown"
}

type reply struct {
	status int
	header nethttp.Header
	body   string
}

type live struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	logs      *recorder
	client    *nethttp.Client
}

// send issues one request against a bound endpoint; a failure to get any response
// is reported and answered with the zero reply, because the case runs inside the drive.
func (l *live) send(endpoint, method, path string, edits ...func(*nethttp.Request)) reply {
	l.t.Helper()
	addr, ok := l.addresses[endpoint]
	if !ok {
		l.t.Errorf("endpoint %q has no bound address, so no request could be sent to it", endpoint)
		return reply{}
	}
	req, err := nethttp.NewRequestWithContext(l.ctx, method, "http://"+addr+path, nil)
	if err != nil {
		l.t.Errorf("building %s %s: %v", method, path, err)
		return reply{}
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Errorf("%s %s on %q got no response: %v", method, path, endpoint, err)
		return reply{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.t.Errorf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func asAlice(r *nethttp.Request) { r.SetBasicAuth("alice", "secret") }

func asBearer(token string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.Header.Set(authHeader, "Bearer "+token) }
}

func (h *recorder) recordsOf() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

func (h *recorder) recordNamed(msg string) (slog.Record, bool) {
	for _, r := range h.recordsOf() {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func attrText(r slog.Record, key string) string {
	v, ok := attr(r, key)
	if !ok {
		return ""
	}
	return v.String()
}

type recordingLogging struct{ logger *slog.Logger }

func (l recordingLogging) Named(string) *slog.Logger { return l.logger }
func (recordingLogging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

var _ log.Logger = recordingLogging{}

func (a liveAssembly) body(endpoints []string) string {
	declared := make([]string, len(endpoints))
	listeners := make([]string, len(endpoints))
	for i, name := range endpoints {
		declared[i] = strconv.Quote(name) + `:{"listener":` + strconv.Quote(name) + `}`
		listeners[i] = strconv.Quote(name) + `:{"address":"127.0.0.1:0"}`
	}
	var items []string
	if a.guarded != nil {
		quoted := make([]string, len(a.guarded))
		for i, name := range a.guarded {
			quoted[i] = strconv.Quote(name)
		}
		items = append(items, `"endpoints":[`+strings.Join(quoted, ",")+`]`)
	}
	if a.inFlight > 0 {
		items = append(items, `"in-flight":`+strconv.Itoa(a.inFlight))
	}
	if a.gateItems != "" {
		items = append(items, a.gateItems)
	}
	authn := `"gate":{` + strings.Join(items, ",") + `}`
	if a.authnItems != "" {
		authn += "," + a.authnItems
	}
	top := `{"authn":{` + authn + `},"http":{"listeners":{` + strings.Join(listeners, ",") +
		`},"endpoints":{` + strings.Join(declared, ",") + `}}`
	if a.topItems != "" {
		top += "," + a.topItems
	}
	return top + "}"
}

func (a liveAssembly) registrant() core.Module {
	return core.Module{
		Name: "stand-in.registrant",
		New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			gate, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			for _, r := range a.routes {
				endpoint, err := router.Endpoint(r.endpoint)
				if err != nil {
					return err
				}
				handler := r.handler
				if handler == nil {
					handler = whoReached
				}
				endpoint.Route(r.method, r.path, handler)
			}
			for _, e := range a.exempt {
				gate.Exempt(e.endpoint, e.method, e.path)
			}
			for _, e := range a.optional {
				gate.Optional(e.endpoint, e.method, e.path)
			}
			for _, e := range a.impersonable {
				gate.Impersonable(e.endpoint, e.method, e.path)
			}
			return nil
		},
	}
}

func (a liveAssembly) serve(t *testing.T, use func(l *live)) *recorder {
	t.Helper()

	endpoints := a.endpoints
	if len(endpoints) == 0 {
		endpoints = []string{"public"}
	}

	gateModule := Module()
	entry := speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, a.body(endpoints), append([]core.Module{gateModule, entry}, a.modules...)...)

	logs := &recorder{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(gateModule)
	reg.Register(entry)
	names := []string{ModuleName, speedhttp.ModuleName}

	logging := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return recordingLogging{logger: slog.New(logs)}, nil
		},
	}
	registrant := a.registrant()
	for _, m := range append([]core.Module{logging, registrant}, a.modules...) {
		reg.Register(m)
		names = append(names, m.Name)
	}
	for _, v := range a.backends {
		m := backendModule(v)
		reg.Register(m)
		names = append(names, m.Name)
	}
	if a.scope != nil {
		reg.Register(standIn("stand-in.admission", (*authn.ScopeGuard)(nil)))
		reg.Supply(a.scope, "stand-in.admission")
		names = append(names, "stand-in.admission")
	}
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(ctx context.Context, _ *core.Registry) error {
			addresses, err := boundAddresses(logs, endpoints)
			if err != nil {
				return err
			}
			use(&live{
				t:         t,
				ctx:       ctx,
				addresses: addresses,
				logs:      logs,
				client: &nethttp.Client{
					Timeout: waitBound(),
					// A connection of its own per request, so no idle
					// connection holds the drain.
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return logs
}

func boundAddresses(logs *recorder, endpoints []string) (map[string]string, error) {
	found := make(map[string]string, len(endpoints))
	for _, r := range logs.recordsOf() {
		if r.Message != listeningMsg {
			continue
		}
		found[attrText(r, "listener")] = attrText(r, "address")
	}
	for _, name := range endpoints {
		if found[name] == "" {
			return nil, fmt.Errorf("the entry point wrote no record of binding endpoint %q, so "+
				"there is no address to send to", name)
		}
	}
	return found, nil
}

func requireStatus(t *testing.T, what string, got reply, want int) {
	t.Helper()
	if got.status != want {
		t.Errorf("%s answered %d, want %d; the body was %q", what, got.status, want, got.body)
	}
}

// requireChallenge reports a reply that does not tell the caller what to send.
func requireChallenge(t *testing.T, what string, got reply) {
	t.Helper()
	if got.header.Get(challengeHeader) == "" {
		t.Errorf("%s carries no %s header, so the caller is not told what credential to send",
			what, challengeHeader)
	}
}

type sighting struct {
	ran     atomic.Bool
	carried atomic.Bool
}

// observer is a module that registers one layer of its own on the public endpoint,
// with the placement it declares, and records what the layer saw. It requires the
// gate capability, so the gateway's layer is registered first.
func observer(name string, seen *sighting, place func(*speedhttp.Middleware)) core.Module {
	return core.Module{
		Name: "stand-in." + name,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*authn.Gate)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			endpoint, err := router.Endpoint("public")
			if err != nil {
				return err
			}
			layer := speedhttp.Middleware{
				Name: name,
				Handler: func(c *gin.Context) {
					seen.ran.Store(true)
					_, carried := identity.From(c.Request.Context())
					seen.carried.Store(carried)
					c.Next()
				},
			}
			place(&layer)
			endpoint.Use(layer)
			return nil
		},
	}
}

// TestALayerInAnEarlierStageSitsOutsideTheGateway holds where the stages put
// a layer relative to the gateway.
func TestALayerInAnEarlierStageSitsOutsideTheGateway(t *testing.T) {
	var outside, inside sighting
	var got reply
	liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
		modules: []core.Module{
			observer("out of stage", &outside, func(mw *speedhttp.Middleware) {
				mw.Stage = speedhttp.StageGuard
			}),
			observer("inside", &inside, func(mw *speedhttp.Middleware) {
				mw.Stage = speedhttp.StageIdentify
				mw.After = []core.Token{(*authn.Gate)(nil)}
			}),
		},
	}.serve(t, func(l *live) {
		got = l.send("public", "GET", "/orders", asAlice)
	})

	requireStatus(t, "a request with a good credential", got, nethttp.StatusOK)
	if !outside.ran.Load() || !inside.ran.Load() {
		t.Fatalf("the layers ran: out of stage %v, inside %v; both should have", outside.ran.Load(),
			inside.ran.Load())
	}
	if !inside.carried.Load() {
		t.Error("a layer declared inside the gateway ran without an identity, so the request " +
			"was not admitted by the gateway")
	}
	if outside.carried.Load() {
		t.Error("a layer in an earlier stage ran inside the gateway, with the identity already " +
			"stamped: the stages have to place it outside the identifying one")
	}
}
