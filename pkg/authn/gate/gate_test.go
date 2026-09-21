package gate

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/configreader"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// fakeVerifier stands in for a credential-verifying backend. Every field the
// tests vary is a field here, so that a case reads as the backend it describes.
type fakeVerifier struct {
	name   string
	claim  authn.Claim
	verify func(ctx context.Context, subject string, c authn.Credential) authn.Verdict
	owns   func(ctx context.Context, subject string) (bool, error)
	// asked counts the calls into Verify, which is how a case asserts that
	// a backend was not reached at all. It is atomic because the cases
	// about the in-flight gate drive one backend from several request
	// goroutines at once, and a miscount there would show up as an
	// assertion failing for no visible reason.
	asked *atomic.Int64
}

func (f *fakeVerifier) Name() string       { return f.name }
func (f *fakeVerifier) Claim() authn.Claim { return f.claim }

func (f *fakeVerifier) Verify(ctx context.Context, subject string, c authn.Credential) authn.Verdict {
	if f.asked != nil {
		f.asked.Add(1)
	}
	if f.verify == nil {
		return authn.Denied(nil)
	}
	return f.verify(ctx, subject, c)
}

// openVerifier is a backend that also answers ownership. It is a separate type
// so that a backend stating an open claim without implementing Owner stays
// expressible, which is the shape the build has to refuse.
type openVerifier struct{ *fakeVerifier }

func (o openVerifier) Owns(ctx context.Context, subject string) (bool, error) {
	if o.owns == nil {
		return false, nil
	}
	return o.owns(ctx, subject)
}

// enumerating builds a backend that is the authority for the subjects named.
func enumerating(name string, subjects ...string) *fakeVerifier {
	return &fakeVerifier{name: name, claim: authn.Claim{Subjects: subjects}}
}

// claimingScheme builds a backend that claims whole authentication schemes.
func claimingScheme(name string, schemes ...string) *fakeVerifier {
	return &fakeVerifier{name: name, claim: authn.Claim{Schemes: schemes}}
}

// openly builds a backend that answers ownership for itself.
func openly(name string, owns func(context.Context, string) (bool, error)) openVerifier {
	return openVerifier{&fakeVerifier{
		name:  name,
		claim: authn.Claim{Open: true},
		owns:  owns,
	}}
}

// passes makes a backend answer that the credential holds for the subject it
// was asked about.
func passes(f *fakeVerifier) *fakeVerifier {
	f.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
		return authn.Authenticated(subject)
	}
	return f
}

// fakeEndpoint records what was registered on one endpoint.
type fakeEndpoint struct {
	middleware []speedhttp.Middleware
}

func (e *fakeEndpoint) Route(string, nethttp.Handler) {}
func (e *fakeEndpoint) Use(mw speedhttp.Middleware)   { e.middleware = append(e.middleware, mw) }
func (e *fakeEndpoint) Accepting() bool               { return true }

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

// quiet is a logger that writes nowhere, so that a test asserting on a
// conclusion does not also print it.
func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testGateway builds a gateway with its ownership table already in place and
// its registration window open, which is the state the Init stage leaves it in.
func testGateway(t *testing.T, endpoints []string, verifiers ...authn.Verifier) *gateway {
	t.Helper()
	return gatewayWith(t, settings{endpoints: endpoints, budget: time.Second, inFlight: 8},
		verifiers...)
}

// gatewayWith is testGateway with the settings spelled out, for the cases that
// are about the call budget or the in-flight gate rather than about a verdict.
func gatewayWith(t *testing.T, set settings, verifiers ...authn.Verifier) *gateway {
	t.Helper()

	g := newGateway(set)
	g.logger = quiet()
	table, err := buildOwnership(verifiers)
	if err != nil {
		t.Fatalf("building the ownership table for this case: %v", err)
	}
	g.table = table
	g.open()
	return g
}

// sealed advances the gateway through Start against a router carrying the
// endpoints named.
func sealed(t *testing.T, g *gateway, router speedhttp.Router) {
	t.Helper()
	if err := g.seal(router); err != nil {
		t.Fatalf("sealing the exemption table: %v", err)
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

// TestAGatewayToldToGuardNothingRefusesToStart holds the only answer this
// module has when the configuration names no endpoint for it to guard.
//
// A command that lists this module has promised it starts, so there is no
// stance in which a gateway comes up registered on nothing and every route on
// every endpoint is served without a credential. The failure names the key: for
// the operator reading it, that is the whole of what to fix.
func TestAGatewayToldToGuardNothingRefusesToStart(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"the key is absent", `{}`},
		{"the list is empty", `{"authn":{"gate":{"endpoints":[]}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, err := prepare(configreader.Over(t, Module(), c.body))
			if err == nil {
				t.Fatalf("a gateway with no endpoint to guard took the stance %v, so a command "+
					"would come up serving every route on every endpoint without a credential",
					state.State)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
				t.Errorf("the failure reads %q and does not name the key to write", err)
			}
		})
	}
}
