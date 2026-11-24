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

const listeningMsg = "an HTTP listener is listening"

func waitBound() time.Duration {
	return coretest.Scale(5 * time.Second)
}

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

const (
	goodToken  = "good"
	imperToken = "impersonating"
	cookieName = "sid"
)

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

func counted(n *atomic.Int64, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		n.Add(1)
		h(c)
	}
}

type liveStack struct {
	endpoints     []string
	mountEndpoint string
	gateItems     string
	publicURL     string
	backends      []authn.Verifier
	scope         authn.ScopeGuard
	register      func(mount.Mounter)
	hand          func(speedhttp.Router, authn.Gate)
}

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

type live struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	client    *nethttp.Client
	mounter   mount.Mounter
}

type reply struct {
	status int
	header nethttp.Header
	body   string
}

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

func headers(pairs ...string) func(*nethttp.Request) {
	return func(r *nethttp.Request) {
		for i := 0; i+1 < len(pairs); i += 2 {
			r.Header.Set(pairs[i], pairs[i+1])
		}
	}
}

func bearer(token string) func(*nethttp.Request) {
	return headers("Authorization", "Bearer "+token)
}

func withCookie(r *nethttp.Request) { r.AddCookie(&nethttp.Cookie{Name: cookieName, Value: "x"}) }

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
					Timeout:   waitBound(),
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
}

func (s liveStack) serve(t *testing.T, use func(l *live)) {
	t.Helper()
	if err := s.run(t, use); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

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

func requireStatus(t *testing.T, what string, got reply, want int) {
	t.Helper()
	if got.status != want {
		t.Errorf("%s answered %d, want %d; the body was %q", what, got.status, want, got.body)
	}
}

func requireNoStore(t *testing.T, what string, got reply) {
	t.Helper()
	if v := got.header.Get("Cache-Control"); v != "no-store" {
		t.Errorf("%s carries Cache-Control %q, want no-store", what, v)
	}
}

// TestEachAccessLevelHoldsOnTheRealGatewayForTheRoutesTheModuleMounted holds each access level from a client's side.
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

// TestAHandWrittenPairWithATypoIsTheFailureTheModuleExcludes controls the pairing with a one-character-wrong exemption.
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

// TestAPublicRouteThatIsAlsoImpersonableIsRefusedAtStart holds that the gateway's conflict rule fails the start.
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

// TestTheRoutesEndpointHasToBeOneTheGatewayGuards holds that an unguarded endpoint fails the start with ErrRoutesUnguarded.
func TestTheRoutesEndpointHasToBeOneTheGatewayGuards(t *testing.T) {
	err := liveStack{
		endpoints:     []string{"admin", "public"},
		mountEndpoint: "public",
		gateItems:     `"endpoints":["admin"]`,
		register:      func(m mount.Mounter) { m.Guarded("GET", "/me", reachedAs) },
	}.run(t, func(*live) { t.Error("the assembly served with the routes on an endpoint the gateway does not guard") })
	if !errors.Is(err, authn.ErrRoutesUnguarded) {
		t.Fatalf("the drive returned %v, want %v", err, authn.ErrRoutesUnguarded)
	}
}

// TestTheRealEntryPointWithSeveralEndpointsAndNoKeyFailsWithErrRoutesUnconfigured holds the first-phase leftover end to end.
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

type fakeScope map[string]bool

func (s fakeScope) Covers(endpoint string) bool { return s[endpoint] }

// TestTheEndpointTheModuleDefaultsToIsJudgedByTheGatewayAgainstTheAdmissionLayer holds that the settled endpoint is judged like a configured one.
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
