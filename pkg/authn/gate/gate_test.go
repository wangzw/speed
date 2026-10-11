package gate

import (
	"context"
	"io"
	"log/slog"
	"maps"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// fakeVerifier stands in for a credential-verifying backend. Every field the
// tests vary is a field here, so that a case reads as the backend it describes.
type fakeVerifier struct {
	name    string
	accepts authn.Accepts
	verify  func(ctx context.Context, c authn.Credential) authn.Verdict
	// asked counts the calls into Verify, which is how a case asserts that a
	// backend was not reached at all. It is atomic because the cases about the
	// in-flight gate drive one backend from several request goroutines at once,
	// and a miscount there would show up as an assertion failing for no visible
	// reason. It may be nil, in which case calls are not counted.
	asked *atomic.Int64
	// mu guards handed.
	mu sync.Mutex
	// handed is every credential the backend was asked about, in order.
	handed []authn.Credential
}

func (f *fakeVerifier) Name() string           { return f.name }
func (f *fakeVerifier) Accepts() authn.Accepts { return f.accepts }

func (f *fakeVerifier) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	if f.asked != nil {
		f.asked.Add(1)
	}
	f.mu.Lock()
	f.handed = append(f.handed, c)
	f.mu.Unlock()
	if f.verify == nil {
		return authn.Denied(nil)
	}
	return f.verify(ctx, c)
}

// credentials is what the backend was handed, in order.
func (f *fakeVerifier) credentials() []authn.Credential {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.handed)
}

// challengingVerifier is a backend that writes its own part of the challenge.
type challengingVerifier struct {
	*fakeVerifier
	challenge func(realm string, rejected bool) []string
}

func (c challengingVerifier) Challenge(realm string, rejected bool) []string {
	return c.challenge(realm, rejected)
}

// clearingVerifier is a backend whose credential is a cookie it can clear.
type clearingVerifier struct {
	*fakeVerifier
	cleared *atomic.Int64
}

func (c clearingVerifier) ClearCookie(w nethttp.ResponseWriter) {
	c.cleared.Add(1)
	nethttp.SetCookie(w, &nethttp.Cookie{Name: c.accepts.Cookies[0], Value: "", MaxAge: -1, Path: "/"})
}

// basicBackend builds a backend that takes the Basic scheme and hands back
// accounts under the prefix.
func basicBackend(name, prefix string) *fakeVerifier {
	return &fakeVerifier{name: name, accepts: authn.Accepts{Schemes: []string{"basic"}, Accounts: prefix}}
}

// schemeBackend builds a backend that takes whole authentication schemes and
// hands back accounts in the library namespace.
func schemeBackend(name string, schemes ...string) *fakeVerifier {
	return &fakeVerifier{name: name, accepts: authn.Accepts{Schemes: schemes}}
}

// cookieBackend builds a backend that takes cookies and hands back accounts in
// the library namespace.
func cookieBackend(name string, cookies ...string) *fakeVerifier {
	return &fakeVerifier{name: name, accepts: authn.Accepts{Cookies: cookies}}
}

// principalOf is what a backend claims about a caller whose credential holds
// for the account local: an account in the backend's own namespace, one
// authentication method and the time it is now. Every field the gateway's exit
// check reads is one a well-behaved backend sets.
func principalOf(f *fakeVerifier, local string) authn.Principal {
	return authn.Principal{
		Account:   authn.AccountID(f.accepts.Accounts + local),
		Methods:   []authn.Method{authn.MethodStatic},
		Assurance: authn.AssuranceSingle,
		AuthTime:  time.Now(),
	}
}

// held is the verdict that a credential holds for the account local, from the
// backend.
func held(f *fakeVerifier, local string) authn.Verdict {
	return authn.Authenticated(principalOf(f, local))
}

// passes makes a backend answer that every credential holds, for alice.
func passes(f *fakeVerifier) *fakeVerifier {
	f.verify = func(context.Context, authn.Credential) authn.Verdict { return held(f, "alice") }
	return f
}

// fakeEndpoint records what was registered on one endpoint. It stands in for the
// entry point in the cases about what the descriptor registers and which
// endpoint names it accepts; what a real chain does with a request is held
// against the real entry point instead.
type fakeEndpoint struct {
	middleware []speedhttp.Middleware
}

func (e *fakeEndpoint) Route(string, string, ...gin.HandlerFunc) {}
func (e *fakeEndpoint) Use(mw speedhttp.Middleware)              { e.middleware = append(e.middleware, mw) }
func (e *fakeEndpoint) Accepting() bool                          { return true }

// fakeRouter hands out the endpoints it was built with and reports every other
// name the way the entry point does.
type fakeRouter struct {
	endpoints map[string]*fakeEndpoint
}

func routerWith(names ...string) *fakeRouter {
	r := &fakeRouter{endpoints: make(map[string]*fakeEndpoint, len(names))}
	for _, name := range names {
		r.endpoints[name] = &fakeEndpoint{}
	}
	return r
}

func (r *fakeRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	e, ok := r.endpoints[name]
	if !ok {
		return nil, speedhttp.ErrUnknownEndpoint
	}
	return e, nil
}

func (r *fakeRouter) Endpoints() []string { return slices.Sorted(maps.Keys(r.endpoints)) }

// fakeScope is the admission layer's statement of which endpoints it guards.
type fakeScope map[string]bool

func (s fakeScope) Covers(endpoint string) bool { return s[endpoint] }

// quiet is a logger that writes nowhere, so that a test asserting on a
// conclusion does not also print it.
func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testGateway builds a gateway with its dispatch table already in place, the
// endpoints named guarded and its registration window open, which is the state
// the Init stage leaves it in.
func testGateway(t *testing.T, guarded []string, verifiers ...authn.Verifier) *gateway {
	t.Helper()
	return gatewayWith(t, settings{endpoints: guarded, budget: time.Second, inFlight: 8}, verifiers...)
}

// gatewayWith is testGateway with the settings spelled out, for the cases that
// are about the call budget or the in-flight gate rather than about a verdict.
func gatewayWith(t *testing.T, set settings, verifiers ...authn.Verifier) *gateway {
	t.Helper()

	g, err := newGateway(set)
	if err != nil {
		t.Fatalf("building the gateway for this case: %v", err)
	}
	g.logger = quiet()
	table, err := buildDispatch(verifiers)
	if err != nil {
		t.Fatalf("building the dispatch table for this case: %v", err)
	}
	g.table = table
	g.guarded = slices.Sorted(slices.Values(set.endpoints))
	g.open()
	return g
}

// sealed advances the gateway through Start against a router carrying the
// endpoints named.
func sealed(t *testing.T, g *gateway, router speedhttp.Router) {
	t.Helper()
	g.router = router
	if err := g.seal(router); err != nil {
		t.Fatalf("sealing the registration tables: %v", err)
	}
}

// request builds a server-side request carrying a logger that writes nowhere,
// so a refusal is counted and judged without its log line reaching the test
// output.
func request(method, target string) *nethttp.Request {
	r := httptest.NewRequest(method, target, nil)
	return r.WithContext(log.WithLogger(r.Context(), quiet()))
}

// withBasic puts a Basic credential on a request.
func withBasic(r *nethttp.Request, user, password string) *nethttp.Request {
	r.SetBasicAuth(user, password)
	return r
}

// withBearer puts a Bearer credential on a request.
func withBearer(r *nethttp.Request, token string) *nethttp.Request {
	r.Header.Set(authHeader, "Bearer "+token)
	return r
}

// withCookie puts a cookie on a request.
func withCookie(r *nethttp.Request, name, value string) *nethttp.Request {
	r.AddCookie(&nethttp.Cookie{Name: name, Value: value})
	return r
}
