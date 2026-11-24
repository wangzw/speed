package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const listeningMsg = "an HTTP listener is listening"

const waitBound = 5 * time.Second

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
	singleToken = "single"
	multiToken  = "multi"
)

func stepUpBackend() *fakeVerifier {
	return &fakeVerifier{
		name:    "tokens",
		accepts: authn.Accepts{Schemes: []string{"bearer"}},
		verify: func(c authn.Credential) authn.Verdict {
			base := authn.Principal{Account: "user-1", AuthTime: time.Now()}
			switch c.Value {
			case singleToken:
				base.Methods = []authn.Method{authn.MethodPassword}
				base.Assurance = authn.AssuranceSingle
				return authn.Authenticated(base)
			case multiToken:
				base.Methods = []authn.Method{authn.MethodPassword, authn.MethodTOTP}
				base.Assurance = authn.AssuranceMulti
				return authn.Authenticated(base)
			}
			return authn.Denied(nil)
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

func who(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		c.String(nethttp.StatusOK, "no identity")
		return
	}
	c.String(nethttp.StatusOK, string(id.Account()))
}

func protect(req authn.Requirement) core.Module {
	return core.Module{
		Name: "stand-in.protected",
		New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			endpoint, err := router.Endpoint("main")
			if err != nil {
				return err
			}
			endpoint.Route(nethttp.MethodGet, "/stepup", Require(req), who)
			return nil
		},
	}
}

type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *recorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recorder) WithGroup(string) slog.Handler      { return h }

func (h *recorder) recordsOf() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.records)
}

func attrText(r slog.Record, key string) string {
	var text string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			text = a.Value.String()
			return false
		}
		return true
	})
	return text
}

type recordingLogging struct{ logger *slog.Logger }

func (l recordingLogging) Named(string) *slog.Logger { return l.logger }
func (recordingLogging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

var _ log.Logger = recordingLogging{}

func loggingModule(logs *recorder) core.Module {
	return core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return recordingLogging{logger: slog.New(logs)}, nil
		},
	}
}

type reply struct {
	status int
	header nethttp.Header
	body   string
}

func send(t *testing.T, client *nethttp.Client, addr, method, path string, edits ...func(*nethttp.Request)) reply {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, path, err)
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s got no response: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func bearer(token string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func boundAddress(logs *recorder, endpoint string) (string, error) {
	for _, r := range logs.recordsOf() {
		if r.Message == listeningMsg && attrText(r, "listener") == endpoint {
			return attrText(r, "address"), nil
		}
	}
	return "", fmt.Errorf("the entry point wrote no record of binding endpoint %q", endpoint)
}

func runLive(t *testing.T, req authn.Requirement, use func(client *nethttp.Client, addr string)) {
	t.Helper()

	const body = `{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}},` +
		`"http":{"listeners":{"main":{"address":"127.0.0.1:0"}},"endpoints":{"main":{"listener":"main"}}}}`

	gateModule, entry := gate.Module(), speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, body, gateModule, entry)

	logs := &recorder{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(gateModule)
	reg.Register(entry)

	host := protect(req)
	reg.Register(host)
	backend := backendModule(stepUpBackend())
	reg.Register(backend)
	logging := loggingModule(logs)
	reg.Register(logging)
	reg.Supply(reader, config.ModuleName)

	names := []string{gate.ModuleName, speedhttp.ModuleName, host.Name, backend.Name, logging.Name}
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(context.Context, *core.Registry) error {
			addr, err := boundAddress(logs, "main")
			if err != nil {
				return err
			}
			client := &nethttp.Client{
				Timeout:   waitBound,
				Transport: &nethttp.Transport{DisableKeepAlives: true},
			}
			use(client, addr)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAStepUpRouteOnTheRealGatewayAdmitsOnlyAStrongEnoughIdentity holds the guarded route's 401, an admitted multi-factor credential and the gateway's different 401.
func TestAStepUpRouteOnTheRealGatewayAdmitsOnlyAStrongEnoughIdentity(t *testing.T) {
	runLive(t, authn.Requirement{Assurance: authn.AssuranceMulti}, func(client *nethttp.Client, addr string) {
		wantChallenge := `Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`

		below := send(t, client, addr, nethttp.MethodGet, "/stepup", bearer(singleToken))
		if below.status != nethttp.StatusUnauthorized {
			t.Errorf("a single-factor credential answered %d, want 401; the body was %q", below.status, below.body)
		}
		if got := below.header.Values("WWW-Authenticate"); !slices.Equal(got, []string{wantChallenge}) {
			t.Errorf("WWW-Authenticate = %q, want %q", got, wantChallenge)
		}
		if typ := problemTypeOf(t, below.body); typ != "urn:speed:authn:insufficient_user_authentication" {
			t.Errorf("type = %q, want urn:speed:authn:insufficient_user_authentication", typ)
		}

		met := send(t, client, addr, nethttp.MethodGet, "/stepup", bearer(multiToken))
		if met.status != nethttp.StatusOK || met.body != "user-1" {
			t.Errorf("a multi-factor credential answered %d %q, want 200 user-1", met.status, met.body)
		}

		none := send(t, client, addr, nethttp.MethodGet, "/stepup")
		if none.status != nethttp.StatusUnauthorized {
			t.Errorf("a request with no credential answered %d, want 401", none.status)
		}
		if ch := none.header.Get("WWW-Authenticate"); ch == "" || strings.Contains(ch, insufficientUserAuthentication) {
			t.Errorf("the gateway's own 401 carries the challenge %q, want one without the step-up code", ch)
		}
	})
}

func problemTypeOf(t *testing.T, body string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	typ, _ := m["type"].(string)
	return typ
}
