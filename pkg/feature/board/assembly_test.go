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
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/feature"
	featureboard "github.com/vislake/speed/pkg/feature/board"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

const (
	listening = "an HTTP listener is listening"
)

func requestBound() time.Duration {
	return coretest.Scale(5 * time.Second)
}

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

type logging struct{ logger *slog.Logger }

func (l logging) Named(string) *slog.Logger { return l.logger }
func (logging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

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

type anchor struct{}

func (anchor) AdmissionLayer() {}

type assembly struct {
	body         string
	modules      []core.Module
	providers    []authztest.Provider
	participants []string
	endpoints    []string
	atStart      bool
}

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

type reply struct {
	status int
	body   string
}

func ask(t *testing.T, address, path, credential string) reply {
	t.Helper()
	limit := requestBound()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		t.Fatalf("building GET %s: %v", path, err)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	client := &http.Client{
		Timeout:   limit,
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

const serveBody = `{
	"http": {"listeners": {"public": {"address": "127.0.0.1:0"}},
	         "endpoints": {"public": {"listener": "public"}}},
	"authn": {"gate": {"endpoints": ["public"]}},
	"feature": {"board": {"endpoint": "public"}}
}`

// TestTheDocumentIsReadOnlyByAnIdentityAndByAnImpersonatingOneToo checks that
// anonymous requests get 401 while a session and an impersonating identity read it.
func TestTheDocumentIsReadOnlyByAnIdentityAndByAnImpersonatingOneToo(t *testing.T) {
	a := assembly{
		body:         serveBody,
		modules:      []core.Module{gate.Module(), backendModule("authn.test", bearer{})},
		participants: []string{gate.ModuleName, "authn.test"},
		endpoints:    []string{"public"},
	}
	err := a.run(t, func(addresses map[string]string) {
		address := addresses["public"]

		if got := ask(t, address, "/api/v1/features", ""); got.status != http.StatusUnauthorized {
			t.Errorf("an anonymous GET /api/v1/features answered %d, want 401; body %q", got.status, got.body)
		}
		if got := ask(t, address, "/api/v1/features", "session"); got.status != http.StatusOK {
			t.Errorf("a session's GET /api/v1/features answered %d, want 200; body %q", got.status, got.body)
		}
		if got := ask(t, address, "/api/v1/features", "impersonate"); got.status != http.StatusOK {
			t.Errorf("an impersonating GET /api/v1/features answered %d, want 200; body %q", got.status, got.body)
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAnEndpointTheGatewayDoesNotGuardFailsTheStart checks that publishing on an
// endpoint the gateway does not guard fails the gateway's Start.
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

// TestAProcessWithoutTheGatewayFailsTheStart checks that a process without an
// authentication gateway fails rather than publishing the document to everyone.
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

// TestTheRouteIsDeclaredOpenOnTheAuthorizationMiddleware checks that an
// authenticated request reaches the handler on an endpoint refusing undeclared routes.
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
		got := ask(t, addresses["public"], "/api/v1/features", "session")
		if got.status != http.StatusOK {
			t.Errorf("an authenticated GET /api/v1/features on a guarded endpoint answered %d, want 200; "+
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
