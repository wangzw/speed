package board_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	featureboard "github.com/vislake/speed/pkg/feature/board"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases here assemble the real thing: the real registry drives the real
// board, the real entry point and, where the criterion is about them, the real
// authentication gateway and the real authorization middleware. A client sends
// real requests. What a case that builds the board by hand cannot show is that
// the registrations it makes mean what the gateways read them as.

const (
	// listening is the record the entry point writes once a listener's address
	// is bound, the only place outside the process that says which port the
	// operating system picked for a listener configured on port 0.
	listening = "an HTTP listener is listening"
	// requestBound bounds each request, so one that would hang fails the case.
	requestBound = 5 * time.Second
)

// memory is a log handler that keeps what it is handed, so a case can read back
// where the entry point bound itself.
type memory struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*memory) Enabled(context.Context, slog.Level) bool { return true }

func (m *memory) Handle(_ context.Context, r slog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r.Clone())
	return nil
}

func (m *memory) WithAttrs([]slog.Attr) slog.Handler { return m }
func (m *memory) WithGroup(string) slog.Handler      { return m }

func (m *memory) addressOf(endpoint string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if r.Message != listening {
			continue
		}
		var name, address string
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "listener":
				name = a.Value.String()
			case "address":
				address = a.Value.String()
			}
			return true
		})
		if name == endpoint {
			return address
		}
	}
	return ""
}

// logging delivers the logging capability over the memory handler.
type logging struct{ logger *slog.Logger }

func (l logging) Named(string) *slog.Logger { return l.logger }
func (logging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// bearer verifies one bearer token. It is a backend of its own, registered the
// way a backend module registers, because the gateway has to verify a real
// credential for the criteria about who may read the document.
type bearer struct{}

func (bearer) Name() string { return "test" }

func (bearer) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}
}

func (bearer) Verify(_ context.Context, c authn.Credential) authn.Verdict {
	switch c.Value {
	case "session":
		return authn.Authenticated(authn.Principal{
			Account:   "acct.alice",
			Methods:   []authn.Method{authn.MethodStatic},
			Assurance: authn.AssuranceSingle,
			AuthTime:  time.Now(),
		})
	case "impersonate":
		return authn.Authenticated(authn.Principal{
			Account:   "acct.customer",
			Actor:     "acct.admin",
			Session:   "0198f0aa-0000-7000-8000-000000000002",
			Scope:     "tenant-a",
			Methods:   []authn.Method{authn.MethodStatic},
			Assurance: authn.AssuranceSingle,
			AuthTime:  time.Now(),
		})
	}
	return authn.Denied(nil)
}

// backendModule registers a verifying backend the way a backend subpackage
// does: its own module, delivering the verification capability.
func backendModule(name string, v authn.Verifier) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return v, nil },
	}
}

// anchor stands where the admission layer would: the authorization middleware
// names its ordering anchor and never calls it.
type anchor struct{}

func (anchor) AdmissionLayer() {}

// assembly is one drive of the real board, entry point and whatever else the
// case brings.
type assembly struct {
	// body is the whole configuration document.
	body string
	// modules take part beside the board and the entry point.
	modules []core.Module
	// providers are capabilities delivered by stand-in modules, which join the
	// drive without being named.
	providers []authztest.Provider
	// participants are the further module names taken in at run level.
	participants []string
	// endpoints are the endpoint names the entry point declares, read back to
	// find the addresses it bound.
	endpoints []string
	// atStart stops the drive at Start instead of serving.
	atStart bool
}

// run drives the assembly. use receives the bound address of every endpoint,
// keyed by endpoint name, once the drive arrives.
func (a assembly) run(t *testing.T, use func(addresses map[string]string)) error {
	t.Helper()

	boardModule := featureboard.Module()
	entry := speedhttp.Module()
	modules := append([]core.Module{boardModule, entry}, a.modules...)
	reader := authntest.ConfigReaderAll(t, a.body, modules...)

	memory := &memory{}
	logger := slog.New(memory)
	loggingModule := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return logging{logger: logger}, nil },
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	names := []string{featureboard.ModuleName, speedhttp.ModuleName, loggingModule.Name}
	reg.Register(boardModule)
	reg.Register(entry)
	reg.Register(loggingModule)
	for _, m := range a.modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	for _, p := range a.providers {
		authztest.AddProvider(reg, p)
	}
	names = append(names, a.participants...)

	depth := core.StageServe
	if a.atStart {
		depth = core.StageStart
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action: func(ctx context.Context, _ *core.Registry) error {
			if use == nil {
				return nil
			}
			addresses := make(map[string]string, len(a.endpoints))
			for _, name := range a.endpoints {
				addresses[name] = memory.addressOf(name)
				if addresses[name] == "" {
					return fmt.Errorf("the entry point wrote no record of binding endpoint %q", name)
				}
			}
			use(addresses)
			return nil
		},
	})
}

// reply is what a client got back.
type reply struct {
	status int
	body   string
}

// ask issues one request against an address.
func ask(t *testing.T, address, path, credential string) reply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), requestBound)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		t.Fatalf("building GET %s: %v", path, err)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	client := &http.Client{
		Timeout:   requestBound,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s got no response: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body of GET %s: %v", path, err)
	}
	return reply{status: resp.StatusCode, body: string(body)}
}

// serveBody is the configuration of an assembly that serves the document on one
// guarded endpoint.
const serveBody = `{
	"http": {"listeners": {"public": {"address": "127.0.0.1:0"}},
	         "endpoints": {"public": {"listener": "public"}}},
	"authn": {"gate": {"endpoints": ["public"]}},
	"feature": {"board": {"endpoint": "public"}}
}`

// TestTheDocumentIsReadOnlyByAnIdentityAndByAnImpersonatingOneToo holds the
// three answers criterion d names: anonymous is refused by the gateway, a
// session reads the document, and an impersonating identity reads it as well.
//
// The last is the one that fails if the module stops registering the route as
// impersonable: the gateway refuses an impersonating identity on a route that
// does not admit one, and that refusal is what this case would see instead.
func TestTheDocumentIsReadOnlyByAnIdentityAndByAnImpersonatingOneToo(t *testing.T) {
	a := assembly{
		body:         serveBody,
		modules:      []core.Module{gate.Module(), backendModule("authn.test", bearer{})},
		participants: []string{gate.ModuleName, "authn.test"},
		endpoints:    []string{"public"},
	}
	err := a.run(t, func(addresses map[string]string) {
		address := addresses["public"]

		if got := ask(t, address, "/features", ""); got.status != http.StatusUnauthorized {
			t.Errorf("an anonymous GET /features answered %d, want 401; body %q", got.status, got.body)
		}
		if got := ask(t, address, "/features", "session"); got.status != http.StatusOK {
			t.Errorf("a session's GET /features answered %d, want 200; body %q", got.status, got.body)
		}
		if got := ask(t, address, "/features", "impersonate"); got.status != http.StatusOK {
			t.Errorf("an impersonating GET /features answered %d, want 200; body %q", got.status, got.body)
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAnEndpointTheGatewayDoesNotGuardFailsTheStart holds the host requirement
// behind the read surface's confidentiality: the endpoint the document is
// published on has to be one the gateway guards, and the gateway's own Start is
// what refuses the registration when it is not.
func TestAnEndpointTheGatewayDoesNotGuardFailsTheStart(t *testing.T) {
	body := `{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}, "ops": {"address": "127.0.0.1:0"}},
		         "endpoints": {"public": {"listener": "public"}, "ops": {"listener": "ops"}}},
		"authn": {"gate": {"endpoints": ["public"]}},
		"feature": {"board": {"endpoint": "ops"}}
	}`
	a := assembly{
		body:         body,
		modules:      []core.Module{gate.Module(), backendModule("authn.test", bearer{})},
		participants: []string{gate.ModuleName, "authn.test"},
		endpoints:    []string{"public", "ops"},
		atStart:      true,
	}
	err := a.run(t, nil)
	if !errors.Is(err, authn.ErrExemptionUnguarded) {
		t.Fatalf("the drive ended with %v, want %v", err, authn.ErrExemptionUnguarded)
	}
}

// TestAProcessWithoutTheGatewayFailsTheStart holds the failure direction: the
// document is not published at all when no authentication gateway exists,
// rather than being published to every caller.
func TestAProcessWithoutTheGatewayFailsTheStart(t *testing.T) {
	body := `{
		"http": {"listeners": {"account": {"address": "127.0.0.1:0"}},
		         "endpoints": {"account": {"listener": "account"}}},
		"feature": {"board": {"endpoint": "account"}}
	}`
	a := assembly{body: body, endpoints: []string{"account"}, atStart: true}
	err := a.run(t, nil)
	if !errors.Is(err, feature.ErrNoGate) {
		t.Fatalf("the drive ended with %v, want %v", err, feature.ErrNoGate)
	}
}

// TestTheRouteIsDeclaredOpenOnTheAuthorizationMiddleware holds the registration
// criterion g relies on. The endpoint is guarded with undeclared routes closed,
// so an authenticated request reaches the handler only because the module
// recorded the route as needing no permission. Without that registration the
// middleware answers 403 and this case fails.
func TestTheRouteIsDeclaredOpenOnTheAuthorizationMiddleware(t *testing.T) {
	body := `{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}},
		         "endpoints": {"public": {"listener": "public"}}},
		"authn": {"gate": {"endpoints": ["public"]}},
		"authz": {"guard": {"endpoints": ["public"], "undeclared": "closed"}},
		"feature": {"board": {"endpoint": "public"}}
	}`
	a := assembly{
		body: body,
		modules: []core.Module{
			gate.Module(),
			backendModule("authn.test", bearer{}),
			guard.Module(),
		},
		providers: []authztest.Provider{
			authztest.EnforcerProvider(authztest.NewEnforcer()),
			authztest.CatalogProvider(authztest.NewCatalog()),
			{Name: "stand-in.admission", Token: (*tenant.Admission)(nil), Instance: anchor{}},
		},
		participants: []string{gate.ModuleName, "authn.test", guard.ModuleName},
		endpoints:    []string{"public"},
	}
	err := a.run(t, func(addresses map[string]string) {
		got := ask(t, addresses["public"], "/features", "session")
		if got.status != http.StatusOK {
			t.Errorf("an authenticated GET /features on a guarded endpoint answered %d, want 200; "+
				"body %q. A 403 here means the route was left undeclared while the endpoint refuses "+
				"undeclared routes", got.status, got.body)
		}
		if !strings.Contains(got.body, `"features"`) {
			t.Errorf("the answer %q does not carry the feature document's outer object", got.body)
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}
