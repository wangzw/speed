package mount

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The cases built on this file hold what only the real entry point and the real
// gateway can decide: that a route mounted through the capability is reached the
// way its access level says, and that the wrapper's check and header are on what
// the entry point serves. Both are the real modules, on a real loopback port per
// endpoint, assembled by the real registry with this module between them.

// listeningMsg is the record the entry point writes once an endpoint's address is
// bound.
const listeningMsg = "an HTTP listener is listening"

// waitBound bounds every request these cases send.
func waitBound() time.Duration {
	return coretest.Scale(5 * time.Second)
}

// fakeVerifier is a credential-verifying backend that answers from a function.
type fakeVerifier struct {
	name    string
	accepts authn.Accepts
	verify  func(authn.Credential) authn.Verdict
}

func (f *fakeVerifier) Name() string           { return f.name }
func (f *fakeVerifier) Accepts() authn.Accepts { return f.accepts }
func (f *fakeVerifier) Verify(_ context.Context, c authn.Credential) authn.Verdict {
	return f.verify(c)
}

// The credentials the backends below understand.
const (
	goodToken  = "good"
	imperToken = "impersonating"
	cookieName = "sid"
)

// bearerBackend takes Bearer credentials: goodToken is user-1, imperToken is an
// administrator impersonating user-1, and anything else does not hold.
func bearerBackend() *fakeVerifier {
	return &fakeVerifier{
		name:    "tokens",
		accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true},
		verify: func(c authn.Credential) authn.Verdict {
			p := authn.Principal{
				Account: "user-1", Methods: []authn.Method{authn.MethodStatic},
				Assurance: authn.AssuranceSingle, AuthTime: time.Now(),
			}
			switch c.Value {
			case goodToken:
				return authn.Authenticated(p)
			case imperToken:
				p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
				return authn.Authenticated(p)
			}
			return authn.Denied(nil)
		},
	}
}

// cookieBackend takes a cookie credential, which the gateway holds to its own
// cross-origin check on an unsafe method, and passes any value.
func cookieBackend() *fakeVerifier {
	return &fakeVerifier{
		name:    "sessions",
		accepts: authn.Accepts{Cookies: []string{cookieName}},
		verify: func(authn.Credential) authn.Verdict {
			return authn.Authenticated(authn.Principal{
				Account: "user-2", Methods: []authn.Method{authn.MethodStatic},
				Assurance: authn.AssuranceSingle, AuthTime: time.Now(),
			})
		},
	}
}

// backendModule registers a backend the way a backend subpackage does.
func backendModule(v authn.Verifier) core.Module {
	return core.Module{
		Name:     "authn." + v.Name(),
		Provides: []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return v, nil },
	}
}

// reachedAs is what a handler answers with: who the gateway made the caller out
// to be and the access level it recorded, so that a case reads from the body
// whether the request got through and under which registration.
func reachedAs(c *gin.Context) {
	who, level := "nobody", "unknown"
	if id, ok := authn.FromContext(c.Request.Context()); ok {
		who = string(id.Account())
		if id.Impersonating() {
			who += " via " + string(id.Actor())
		}
	}
	switch authn.AccessFromContext(c.Request.Context()) {
	case authn.AccessOpen:
		level = "open"
	case authn.AccessOptional:
		level = "optional"
	case authn.AccessRequired:
		level = "required"
	}
	c.String(nethttp.StatusOK, "%s [%s]", who, level)
}

// counted wraps a handler so that a case reads how many times it was called.
func counted(n *atomic.Int64, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		n.Add(1)
		h(c)
	}
}

// liveStack is one drive of the real registry to Serve.
type liveStack struct {
	// endpoints are the entry point's endpoints, "main" by default.
	endpoints []string
	// mountEndpoint is the ordinary scope's endpoint; empty leaves the key out.
	mountEndpoint string
	// gateItems is text for the gateway's section, JSON members without the
	// braces, such as `"trusted-origins":["https://other.example.com"]`.
	gateItems string
	// publicURL is the public URL; a default is used when it is empty.
	publicURL string
	backends  []authn.Verifier
	// scope, when set, is delivered as the tenant admission layer's scope guard.
	scope authn.ScopeGuard
	// register runs in the Init of a module that requires the capability.
	register func(mount.Mounter)
	// hand runs in the Init of a module that takes the entry point and the
	// gateway up and registers on them directly, the way a host's own module
	// does.
	hand func(speedhttp.Router, authn.Gate)
}

// defaultPublicURL is the public URL the cases use unless one says otherwise.
const defaultPublicURL = "https://app.example.com"

func (s liveStack) body(endpoints []string) string {
	declared := make([]string, len(endpoints))
	listeners := make([]string, len(endpoints))
	for i, name := range endpoints {
		declared[i] = strconv.Quote(name) + `:{"listener":` + strconv.Quote(name) + `}`
		listeners[i] = strconv.Quote(name) + `:{"address":"127.0.0.1:0"}`
	}
	publicURL := s.publicURL
	if publicURL == "" {
		publicURL = defaultPublicURL
	}
	mounts := `"public-url":` + strconv.Quote(publicURL)
	if s.mountEndpoint != "" {
		mounts += `,"endpoints":{"default":` + strconv.Quote(s.mountEndpoint) + `}`
	}
	items := `"mount":{` + mounts + `}`
	if s.gateItems != "" {
		items += "," + s.gateItems
	}
	return `{"authn":{"gate":{` + items + `}},"http":{"listeners":{` + strings.Join(listeners, ",") +
		`},"endpoints":{` + strings.Join(declared, ",") + `}}}`
}

// live is what a case holds while the assembly serves.
type live struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	client    *nethttp.Client
	mounter   mount.Mounter
}

// reply is what a client got back.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

// send issues one request against a bound endpoint.
func (l *live) send(endpoint, method, path string, edits ...func(*nethttp.Request)) reply {
	l.t.Helper()
	addr, ok := l.addresses[endpoint]
	if !ok {
		l.t.Errorf("endpoint %q has no bound address", endpoint)
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

// headers returns an edit that sets each pair on a request.
func headers(pairs ...string) func(*nethttp.Request) {
	return func(r *nethttp.Request) {
		for i := 0; i+1 < len(pairs); i += 2 {
			r.Header.Set(pairs[i], pairs[i+1])
		}
	}
}

// bearer returns an edit that puts a Bearer credential on a request.
func bearer(token string) func(*nethttp.Request) {
	return headers("Authorization", "Bearer "+token)
}

// withCookie puts the session cookie on a request.
func withCookie(r *nethttp.Request) { r.AddCookie(&nethttp.Cookie{Name: cookieName, Value: "x"}) }

// run drives the assembly to Serve and hands the case the running entry point;
// the assembly is stopped and closed when the case returns. It returns what the
// drive returned, so that a case about a start that fails reads the error.
func (s liveStack) run(t *testing.T, use func(l *live)) error {
	t.Helper()

	endpoints := s.endpoints
	if len(endpoints) == 0 {
		endpoints = []string{"main"}
	}
	own, gateModule, entry := Module(), gate.Module(), speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, s.body(endpoints), own, gateModule, entry)

	logs := &recorder{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(gateModule)
	reg.Register(own)
	reg.Register(entry)
	names := []string{gate.ModuleName, ModuleName, speedhttp.ModuleName}

	logging := loggingModule(logs)
	reg.Register(logging)
	names = append(names, logging.Name)

	var mounted mount.Mounter
	user := registrant(func(m mount.Mounter) {
		mounted = m
		if s.register != nil {
			s.register(m)
		}
	})
	reg.Register(user)
	names = append(names, user.Name)

	if s.hand != nil {
		hand := core.Module{
			Name: "stand-in.hand",
			New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
			Init: func(_ context.Context, reg *core.Registry, _ any) error {
				router, err := core.Resolve[speedhttp.Router](reg)
				if err != nil {
					return err
				}
				g, err := core.Resolve[authn.Gate](reg)
				if err != nil {
					return err
				}
				s.hand(router, g)
				return nil
			},
		}
		reg.Register(hand)
		names = append(names, hand.Name)
	}
	backends := s.backends
	if backends == nil {
		backends = []authn.Verifier{bearerBackend()}
	}
	for _, v := range backends {
		m := backendModule(v)
		reg.Register(m)
		names = append(names, m.Name)
	}
	if s.scope != nil {
		reg.Register(standIn("stand-in.admission", (*authn.ScopeGuard)(nil)))
		reg.Supply(s.scope, "stand-in.admission")
		names = append(names, "stand-in.admission")
	}
	reg.Supply(reader, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(ctx context.Context, _ *core.Registry) error {
			addresses, err := boundAddresses(logs, endpoints)
			if err != nil {
				return err
			}
			use(&live{
				t: t, ctx: ctx, addresses: addresses, mounter: mounted,
				client: &nethttp.Client{
					Timeout: waitBound(),
					// A connection of its own per request, so that what the
					// server does to one is seen by that request alone.
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
}

// serve is run for a case that expects the start to succeed.
func (s liveStack) serve(t *testing.T, use func(l *live)) {
	t.Helper()
	if err := s.run(t, use); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// boundAddresses reads each endpoint's bound address out of the records the
// entry point wrote when it bound them.
func boundAddresses(logs *recorder, endpoints []string) (map[string]string, error) {
	found := make(map[string]string, len(endpoints))
	for _, r := range logs.recordsOf() {
		if r.Message == listeningMsg {
			found[attrText(r, "listener")] = attrText(r, "address")
		}
	}
	for _, name := range endpoints {
		if found[name] == "" {
			return nil, fmt.Errorf("the entry point wrote no record of binding endpoint %q", name)
		}
	}
	return found, nil
}

// requireStatus reports a reply that is not the status the case expects.
func requireStatus(t *testing.T, what string, got reply, want int) {
	t.Helper()
	if got.status != want {
		t.Errorf("%s answered %d, want %d; the body was %q", what, got.status, want, got.body)
	}
}

// requireNoStore reports a reply that does not forbid being stored.
func requireNoStore(t *testing.T, what string, got reply) {
	t.Helper()
	if v := got.header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("%s carries Cache-Control %q, want no-store", what, v)
	}
}

// TestEachAccessLevelHoldsOnTheRealGatewayForTheRoutesTheModuleMounted holds the
// pairing from the side a client sees: a route registered Public is reached
// with no credential and the gateway reads none, Optional is reached with none
// and still refuses a bad one, Guarded refuses a request with none, and a route
// registered Impersonable lets an impersonating identity through where the
// others do not. Each answer differs between the levels, so a registration
// written at another level than the call asked for would show.
func TestEachAccessLevelHoldsOnTheRealGatewayForTheRoutesTheModuleMounted(t *testing.T) {
	liveStack{
		register: func(m mount.Mounter) {
			m.Public("GET", "/pub", reachedAs)
			m.Optional("GET", "/opt", reachedAs)
			m.Guarded("GET", "/guard", reachedAs)
			m.Guarded("GET", "/imp", reachedAs, mount.Impersonable())
			m.Optional("GET", "/opt-imp", reachedAs, mount.Impersonable())
		},
	}.serve(t, func(l *live) {
		for _, c := range []struct {
			name   string
			path   string
			edit   func(*nethttp.Request)
			status int
			body   string
		}{
			{"public, no credential", "/api/v1/auth/pub", nil, 200, "nobody [open]"},
			{"public, a credential that does not hold", "/api/v1/auth/pub", bearer("bad"), 200, "nobody [open]"},
			{"public, a good credential", "/api/v1/auth/pub", bearer(goodToken), 200, "nobody [open]"},
			{"optional, no credential", "/api/v1/auth/opt", nil, 200, "nobody [optional]"},
			{"optional, a credential that does not hold", "/api/v1/auth/opt", bearer("bad"), 401, ""},
			{"optional, a good credential", "/api/v1/auth/opt", bearer(goodToken), 200, "user-1 [optional]"},
			{"guarded, no credential", "/api/v1/auth/guard", nil, 401, ""},
			{"guarded, a credential that does not hold", "/api/v1/auth/guard", bearer("bad"), 401, ""},
			{"guarded, a good credential", "/api/v1/auth/guard", bearer(goodToken), 200, "user-1 [required]"},
			{"guarded, an impersonating identity", "/api/v1/auth/guard", bearer(imperToken), 401, ""},
			{"guarded and impersonable, an impersonating identity", "/api/v1/auth/imp", bearer(imperToken), 200, "user-1 via admin-9 [required]"},
			{"guarded and impersonable, no credential", "/api/v1/auth/imp", nil, 401, ""},
			{"optional and impersonable, an impersonating identity", "/api/v1/auth/opt-imp", bearer(imperToken), 200, "user-1 via admin-9 [optional]"},
			{"optional and impersonable, no credential", "/api/v1/auth/opt-imp", nil, 200, "nobody [optional]"},
		} {
			var edits []func(*nethttp.Request)
			if c.edit != nil {
				edits = append(edits, c.edit)
			}
			got := l.send("main", "GET", c.path, edits...)
			requireStatus(t, c.name, got, c.status)
			if c.body != "" && got.body != c.body {
				t.Errorf("%s answered %q, want %q", c.name, got.body, c.body)
			}
		}
	})
}

// TestARouteIsMountedOnTheMountEndpointAndNotOnTheOthers holds that the routes
// land on the endpoint the capability settled on, with several declared.
func TestARouteIsMountedOnTheMountEndpointAndNotOnTheOthers(t *testing.T) {
	liveStack{
		endpoints:     []string{"admin", "public"},
		mountEndpoint: "public",
		register:      func(m mount.Mounter) { m.Public("GET", "/pub", reachedAs) },
	}.serve(t, func(l *live) {
		if l.mounter.Endpoint() != "public" {
			t.Errorf("the capability reports endpoint %q, want public", l.mounter.Endpoint())
		}
		requireStatus(t, "the route on the mount endpoint", l.send("public", "GET", "/api/v1/auth/pub"), 200)
		requireStatus(t, "the same path on the other endpoint, with a credential",
			l.send("admin", "GET", "/api/v1/auth/pub", bearer(goodToken)), 404)
	})
}

// TestAHandWrittenPairWithATypoIsTheFailureTheModuleExcludes is the control for
// the pairing: a route and an exemption written separately, the exemption with a
// character wrong, leaves the route guarded, and a client with no credential is
// refused at a route its author meant to be public. The same route mounted
// through the capability is reached.
func TestAHandWrittenPairWithATypoIsTheFailureTheModuleExcludes(t *testing.T) {
	liveStack{
		register: func(m mount.Mounter) { m.Public("GET", "/status", reachedAs) },
		hand: func(r speedhttp.Router, g authn.Gate) {
			endpoint, err := r.Endpoint("main")
			if err != nil {
				t.Errorf("taking up the endpoint: %v", err)
				return
			}
			endpoint.Route("GET", "/hand/status", reachedAs)
			g.Exempt("main", "GET", "/hand/statuz")
		},
	}.serve(t, func(l *live) {
		requireStatus(t, "the hand-written route with a mistyped exemption", l.send("main", "GET", "/hand/status"), 401)
		requireStatus(t, "the same route mounted through the capability", l.send("main", "GET", "/api/v1/auth/status"), 200)
	})
}

// TestAPublicRouteThatIsAlsoImpersonableIsRefusedAtStart holds that the
// gateway's own conflict rule sees the two registrations the module makes for
// one route: an exempt route reads no credential, so it admits no impersonating
// identity, and asking for both is a startup failure and not a route that
// silently behaves as one of them.
func TestAPublicRouteThatIsAlsoImpersonableIsRefusedAtStart(t *testing.T) {
	err := liveStack{
		register: func(m mount.Mounter) { m.Public("GET", "/both", reachedAs, mount.Impersonable()) },
	}.run(t, func(*live) {
		t.Error("the assembly served although a route was registered both exempt and impersonable")
	})
	if !errors.Is(err, authn.ErrAccessConflict) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrAccessConflict)
	}
}

// TestTheRoutesEndpointHasToBeOneTheGatewayGuards holds the integration with the
// gateway's own check on the endpoint: with a module that mounts routes in the
// assembly and the key naming an endpoint the gateway does not guard, the start
// fails with ErrRoutesUnguarded.
func TestTheRoutesEndpointHasToBeOneTheGatewayGuards(t *testing.T) {
	err := liveStack{
		endpoints:     []string{"admin", "public"},
		mountEndpoint: "public",
		gateItems:     `"endpoints":["admin"]`,
		// Only a guarded route: it registers nothing with the gateway, so nothing
		// but the endpoint check can notice that it would be served unguarded.
		register: func(m mount.Mounter) { m.Guarded("GET", "/me", reachedAs) },
	}.run(t, func(*live) { t.Error("the assembly served with the routes on an endpoint the gateway does not guard") })
	if !errors.Is(err, authn.ErrRoutesUnguarded) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrRoutesUnguarded)
	}
}

// TestTheRealEntryPointWithSeveralEndpointsAndNoKeyFailsWithErrRoutesUnconfigured
// holds the first-phase leftover end to end: the endpoints the real entry point
// declares are what the rule counts.
func TestTheRealEntryPointWithSeveralEndpointsAndNoKeyFailsWithErrRoutesUnconfigured(t *testing.T) {
	err := liveStack{
		endpoints: []string{"admin", "public"},
		register:  func(m mount.Mounter) { m.Public("GET", "/pub", reachedAs) },
	}.run(t, func(*live) { t.Error("the assembly served with no endpoint to mount the routes on") })
	if !errors.Is(err, authn.ErrRoutesUnconfigured) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrRoutesUnconfigured)
	}
	if !strings.Contains(err.Error(), "admin, public") || !strings.Contains(err.Error(), "authn.gate.mount.endpoints.default") {
		t.Errorf("the failure %q does not name the key and the endpoints to choose from", err)
	}
}

// fakeScope is a scope guard that covers the endpoints it names.
type fakeScope map[string]bool

func (s fakeScope) Covers(endpoint string) bool { return s[endpoint] }

// TestTheEndpointTheModuleDefaultsToIsJudgedByTheGatewayAgainstTheAdmissionLayer
// holds the other half of the empty key: the module settles the only endpoint
// and the gateway, which finds the module in the assembly by the capability it
// delivers, judges that endpoint like a configured one. An admission layer that
// covers it fails the start, and one that covers another endpoint does not.
func TestTheEndpointTheModuleDefaultsToIsJudgedByTheGatewayAgainstTheAdmissionLayer(t *testing.T) {
	register := func(m mount.Mounter) { m.Guarded("GET", "/me", reachedAs) }
	err := liveStack{scope: fakeScope{"main": true}, register: register}.run(t, func(*live) {
		t.Error("the assembly served with the routes on an endpoint the admission layer covers")
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrInvalidConfig)
	}

	liveStack{endpoints: []string{"main", "other"}, mountEndpoint: "main", scope: fakeScope{"other": true}, register: register}.
		serve(t, func(l *live) {
			requireStatus(t, "a route on an endpoint the admission layer does not cover",
				l.send("main", "GET", "/api/v1/auth/me", bearer(goodToken)), 200)
		})
}
