// Package mounttest is a test-only Mounter for the tests of the modules that
// mount routes, standing in for the authn.mount module and the gateway on a gin
// engine driven with httptest; every registration goes through the real mount.Wrap.
package mounttest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// Kind is the kind of a registration, which is the access level the gateway is
// told about the route.
type Kind string

// The kinds of registration, one per Mounter method that registers a route.
const (
	Public   Kind = "public"
	Optional Kind = "optional"
	Guarded  Kind = "guarded"
	Callback Kind = "callback"
)

// Registration is one route a test's code mounted.
type Registration struct {
	// Kind is what the registration was made as.
	Kind         Kind
	Methods      []string // one method, or the methods of a Callback
	Path         string   // Base() joined to the rel the caller passed
	Impersonable bool     // registered with mount.Impersonable()
}

// Defaults of a Mounter that no option changed.
const (
	DefaultBase      = mount.DefaultBasePath
	DefaultPublicURL = "https://app.example.com"
	DefaultEndpoint  = "main"
)

func defaultBases() map[speedhttp.Scope]string {
	return map[speedhttp.Scope]string{
		"":                     mount.DefaultBasePath,
		speedhttp.ScopeTenants: mount.DefaultTenantsBasePath,
		speedhttp.ScopeSystem:  mount.DefaultSystemBasePath,
	}
}

// Option sets one thing about the Mounter New builds.
type Option func(*Mounter)

// WithBase sets the base path every scope is joined to. The default is the
// default base of each scope, from the mount package.
func WithBase(base string) Option {
	return func(m *Mounter) {
		for scope := range m.bases {
			m.bases[scope] = base
		}
	}
}

// WithPublicURL sets the origin the deployment is reached at from outside. The
// default is DefaultPublicURL, which is the first trusted origin of the
// cross-origin check.
func WithPublicURL(u string) Option { return func(m *Mounter) { m.publicURL = u } }

// WithTrustedOrigins adds origins the cross-origin check trusts beside the public
// URL's.
func WithTrustedOrigins(origins ...string) Option {
	own := slices.Clone(origins)
	return func(m *Mounter) { m.trusted = own }
}

// WithEndpoint sets the endpoint name the Mounter reports, which is the realm of
// a handler's own 401. The default is DefaultEndpoint.
func WithEndpoint(name string) Option { return func(m *Mounter) { m.endpoint = name } }

// Mounter implements the Mounter interface of the mount package on a gin engine.
// Its registrations and the identity of its requests are safe to use from the
// goroutines of a test.
type Mounter struct {
	t         testing.TB
	engine    *gin.Engine
	csrf      *http.CrossOriginProtection
	bases     map[speedhttp.Scope]string
	publicURL string
	endpoint  string
	trusted   []string

	mu       sync.Mutex
	regs     []Registration
	identity []authntest.Option // nil: no identity
}

var _ mount.Mounter = (*Mounter)(nil)

// New builds a Mounter with an empty engine. A public URL or a trusted origin
// the cross-origin check cannot use fails the test.
func New(t testing.TB, opts ...Option) *Mounter {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := &Mounter{t: t, bases: defaultBases(), publicURL: DefaultPublicURL, endpoint: DefaultEndpoint}
	for _, o := range opts {
		o(m)
	}
	csrf, err := mount.CrossOrigin(m.publicURL, m.trusted)
	if err != nil {
		t.Fatalf("mounttest: the origins cannot be trusted: %v", err)
		return m
	}
	m.csrf = csrf
	m.engine = gin.New()
	// A panicking handler ends in a 500 so a test gets a response, and this
	// recovery keeps the headers the wrapper set, which the entry point's own
	// fallback does not.
	m.engine.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	return m
}

// As sets the identity the following requests on Optional and Guarded routes
// carry, built by authntest.Context from opts; an option the gateway would not
// stamp fails the test when a request arrives. No option is the default identity.
func (m *Mounter) As(opts ...authntest.Option) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity = append([]authntest.Option{}, opts...)
}

// AsAnonymous removes the identity: an Optional route runs with none and a
// Guarded route answers 401, which is what a request with no credential gets.
func (m *Mounter) AsAnonymous() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity = nil
}

// Registrations lists what was mounted, in the order of the calls.
func (m *Mounter) Registrations() []Registration {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Registration, len(m.regs))
	for i, r := range m.regs {
		r.Methods = slices.Clone(r.Methods)
		out[i] = r
	}
	return out
}

// Registered finds the registration that carries the method at the path, which
// is the full path Base() joined to rel.
func (m *Mounter) Registered(method, path string) (Registration, bool) {
	for _, r := range m.Registrations() {
		if r.Path == path && slices.Contains(r.Methods, method) {
			return r, true
		}
	}
	return Registration{}, false
}

// Engine is the gin engine the routes are mounted on, for a test that serves it
// itself.
func (m *Mounter) Engine() *gin.Engine { return m.engine }

// ServeHTTP serves a request through the engine, so a Mounter is the handler of
// an httptest server.
func (m *Mounter) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.engine.ServeHTTP(w, r) }

// Do serves one request through the engine and returns the recorded response.
func (m *Mounter) Do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.engine.ServeHTTP(w, r)
	return w
}

// Public registers the route and exempts it: no identity, AccessOpen.
func (m *Mounter) Public(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	m.register(Public, []string{method}, rel, h, opts)
}

// Optional registers the route with authentication optional on it.
func (m *Mounter) Optional(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	m.register(Optional, []string{method}, rel, h, opts)
}

// Guarded registers the route for the gateway to guard.
func (m *Mounter) Guarded(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	m.register(Guarded, []string{method}, rel, h, opts)
}

// Callback registers a public route for each of methods and adds the bypass of
// the cross-origin check for them.
func (m *Mounter) Callback(methods []string, rel string, h gin.HandlerFunc) {
	m.register(Callback, methods, rel, h, nil)
}

// BaseIn is the base path every rel is joined to for the given scope.
func (m *Mounter) BaseIn(scope speedhttp.Scope) string { return m.bases[scope] }

// PublicURL is the origin the deployment is reached at from outside.
func (m *Mounter) PublicURL() string { return m.publicURL }

// Endpoint is the endpoint name the Mounter reports.
func (m *Mounter) Endpoint() string { return m.endpoint }

// CSRF is the cross-origin check the wrapper applies.
func (m *Mounter) CSRF() *http.CrossOriginProtection { return m.csrf }

func (m *Mounter) register(kind Kind, methods []string, rel string, h gin.HandlerFunc, opts []mount.Option) {
	spec := mount.Resolve(opts...)
	path := m.bases[spec.Scope] + rel
	chain := mount.Wrap(m.csrf, m.endpoint, h)
	if kind == Callback {
		mount.AllowCallback(m.csrf, methods, path)
	}
	for _, method := range methods {
		m.engine.Handle(method, path, m.gateway(kind, spec.Impersonable), chain)
	}
	m.mu.Lock()
	m.regs = append(m.regs, Registration{Kind: kind, Methods: slices.Clone(methods), Path: path, Impersonable: spec.Impersonable})
	m.mu.Unlock()
}

func (m *Mounter) gateway(kind Kind, impersonable bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		switch kind {
		case Public, Callback:
			ctx = authntest.Anonymous(m.t, ctx, authn.AccessOpen)
		case Optional, Guarded:
			m.mu.Lock()
			opts := m.identity
			m.mu.Unlock()
			if opts == nil {
				if kind == Guarded {
					mount.Unauthorized(c, m.endpoint, "")
					return
				}
				ctx = authntest.Anonymous(m.t, ctx, authn.AccessOptional)
				break
			}
			access := authn.AccessRequired
			if kind == Optional {
				access = authn.AccessOptional
			}
			ctx = authntest.Context(m.t, ctx, append(slices.Clone(opts), authntest.WithAccess(access))...)
			if id, ok := authn.FromContext(ctx); ok && id.Impersonating() && !impersonable {
				mount.Unauthorized(c, m.endpoint, "")
				return
			}
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
