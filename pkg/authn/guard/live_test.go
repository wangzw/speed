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

// The cases built on this file hold what only the real entry point and the real
// gateway can decide: that a route this middleware guards is reached the way the
// requirement says, and that the full header a client reads off a refusal is the
// one the design names. The entry point, the gateway and the logging capability
// are the real modules, on a real loopback port, with this package between the
// gateway and the handler.

// listeningMsg is the record the entry point writes once an endpoint's address is
// bound.
const listeningMsg = "an HTTP listener is listening"

// waitBound bounds every request these cases send.
const waitBound = 5 * time.Second

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

// The credentials the backend below understands.
const (
	singleToken = "single"
	multiToken  = "multi"
)

// stepUpBackend takes Bearer credentials: singleToken is a single-factor
// identity, multiToken is a multi-factor one, and anything else does not hold.
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

// who answers with the account the gateway made the caller out to be, so that a
// case reads from the body that the request reached the handler past the
// middleware.
func who(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		c.String(nethttp.StatusOK, "no identity")
		return
	}
	c.String(nethttp.StatusOK, string(id.Account()))
}

// protect is a module that registers one route on the entry point's own endpoint
// with Require in front of it, the way a host's own module does.
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

// recorder is a slog handler that keeps every record written to it.
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

// attrText renders a record's attribute as the string it holds.
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

// recordingLogging delivers the logging capability over a logger the case holds.
type recordingLogging struct{ logger *slog.Logger }

func (l recordingLogging) Named(string) *slog.Logger { return l.logger }
func (recordingLogging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

var _ log.Logger = recordingLogging{}

// loggingModule is the module that delivers the logging capability over logs.
func loggingModule(logs *recorder) core.Module {
	return core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return recordingLogging{logger: slog.New(logs)}, nil
		},
	}
}

// reply is what a client got back.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

// send issues one request against the bound address.
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

// bearer is an edit that puts a Bearer credential on a request.
func bearer(token string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// boundAddress reads the endpoint's bound address out of the records the entry
// point wrote when it bound it.
func boundAddress(logs *recorder, endpoint string) (string, error) {
	for _, r := range logs.recordsOf() {
		if r.Message == listeningMsg && attrText(r, "listener") == endpoint {
			return attrText(r, "address"), nil
		}
	}
	return "", fmt.Errorf("the entry point wrote no record of binding endpoint %q", endpoint)
}

// runLive drives the real entry point, the real gateway, a stand-in verifying
// backend and one route this middleware guards, to Serve, and hands the case the
// running address. The assembly is stopped and closed when the case returns.
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
				Timeout: waitBound,
				// A connection of its own per request, so that what the server
				// does to one is seen by that request alone.
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

// TestAStepUpRouteOnTheRealGatewayAdmitsOnlyAStrongEnoughIdentity holds the
// middleware on the assembly it is meant for: a route a host guarded with a
// multi-factor requirement answers a single-factor credential with the step-up
// 401 and its full challenge, lets a multi-factor credential through, and a
// request with no credential at all is refused by the gateway before the
// middleware, with a different challenge — so the two 401s are told apart.
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

// problemTypeOf reads the type member of a problem details body, and is empty for
// a body that is not one.
func problemTypeOf(t *testing.T, body string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	typ, _ := m["type"].(string)
	return typ
}
