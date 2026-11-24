package gate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const (
	// listening is the record the entry point writes once a listener's address
	// is bound, the only place outside the process that says which port the
	// operating system picked for a listener configured on port 0.
	listening    = "an HTTP listener is listening"
	requestBound = 5 * time.Second
)

type memory struct {
	mu      sync.Mutex
	records []slog.Record
}

func (m *memory) Enabled(context.Context, slog.Level) bool { return true }
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

type reply struct {
	status int
	header nethttp.Header
	body   string
}

type host struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	client    *nethttp.Client
}

func (h *host) send(endpoint, method, path string, edits ...func(*nethttp.Request)) reply {
	h.t.Helper()
	req, err := nethttp.NewRequestWithContext(h.ctx, method, "http://"+h.addresses[endpoint]+path, nil)
	if err != nil {
		h.t.Errorf("building %s %s: %v", method, path, err)
		return reply{}
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Errorf("%s %s on %q got no response: %v", method, path, endpoint, err)
		return reply{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Errorf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func as(user, password string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.SetBasicAuth(user, password) }
}

// whoReached is the handler behind the gateway: it answers with the account the
// request was made out to be, the backend that verified it and the challenge name
// that re-authenticating it would be written under.
func whoReached(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		c.String(nethttp.StatusOK, "nobody")
		return
	}
	c.String(nethttp.StatusOK, "%s via %s as %s", id.Account(), id.Backend(), id.Challenge())
}

// table writes the subject table of the password backend: one entry per subject,
// each in the form the case names.
func table(t *testing.T) map[string]string {
	t.Helper()
	argon, err := static.Hash("alice-password")
	if err != nil {
		t.Fatalf("writing an Argon2id entry: %v", err)
	}
	cheap := func(plaintext string) string {
		encoded, err := static.HashPBKDF2(plaintext, 2)
		if err != nil {
			t.Fatalf("writing a PBKDF2 entry: %v", err)
		}
		return encoded
	}
	fields := strings.Split(cheap("carol-password"), "$") // "", name, "i=2,l=32", salt, hash
	return map[string]string{
		"alice": argon,
		"bob":   cheap("bob-password"),
		"carol": "pbkdf2-sha256$2$" + fields[3] + "$" + fields[4],
		"dave":  cheap("dave-password"),
		"erin":  cheap("erin-password"),
		"frank": cheap("frank-password"),
	}
}

// serve drives the real registry to Serve with the gateway, the password backend
// and the entry point declaring the two endpoints, and hands the case the running
// host.
func serve(t *testing.T, gateItems string, use func(h *host)) {
	t.Helper()

	endpoints := []string{"public", "ops"}
	declared := make([]string, len(endpoints))
	listeners := make([]string, len(endpoints))
	for i, name := range endpoints {
		declared[i] = fmt.Sprintf("%q:{\"listener\":%q}", name, name)
		listeners[i] = fmt.Sprintf("%q:{\"address\":\"127.0.0.1:0\"}", name)
	}
	subjects, err := json.Marshal(table(t))
	if err != nil {
		t.Fatalf("encoding the subject table: %v", err)
	}
	gateSection := `"gate":{` + gateItems + `}`
	body := `{"authn":{` + gateSection + `,"static":{"subjects":` + string(subjects) + `}},` +
		`"http":{"listeners":{` + strings.Join(listeners, ",") + `},"endpoints":{` +
		strings.Join(declared, ",") + `}}}`

	gateModule, staticModule, entry := gate.Module(), static.Module(), speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, body, gateModule, staticModule, entry)

	memory := &memory{}
	logger := slog.New(memory)
	stand := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return logging{logger: logger}, nil },
	}
	registrant := core.Module{
		Name: "stand-in.registrant",
		New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, resolved := core.Resolve[speedhttp.Router](reg)
			if resolved != nil {
				return resolved
			}
			for endpoint, path := range map[string]string{"public": "/orders", "ops": "/health"} {
				e, found := router.Endpoint(endpoint)
				if found != nil {
					return found
				}
				e.Route("GET", path, whoReached)
			}
			return nil
		},
	}

	reg := core.New()
	reg.Register(config.Module())
	names := []string{}
	for _, m := range []core.Module{gateModule, staticModule, entry, stand, registrant} {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)

	err = reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(ctx context.Context, _ *core.Registry) error {
			addresses := make(map[string]string, len(endpoints))
			for _, name := range endpoints {
				addresses[name] = memory.addressOf(name)
				if addresses[name] == "" {
					return fmt.Errorf("the entry point wrote no record of binding endpoint %q", name)
				}
			}
			use(&host{
				t:         t,
				ctx:       ctx,
				addresses: addresses,
				client: &nethttp.Client{
					Timeout:   requestBound,
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// requireBasicChallenge reports a refusal that does not tell the caller to send a
// Basic credential for the realm named.
func requireBasicChallenge(t *testing.T, what string, got reply, realm string) {
	t.Helper()
	if got.status != nethttp.StatusUnauthorized {
		t.Errorf("%s answered %d, want 401; the body was %q", what, got.status, got.body)
		return
	}
	if want := fmt.Sprintf("Basic realm=%q", realm); got.header.Get("WWW-Authenticate") != want {
		t.Errorf("%s carries WWW-Authenticate %q, want %q", what, got.header.Get("WWW-Authenticate"), want)
	}
}

// TestEveryEndpointIsGuardedWithNoConfigurationAndAsksForBasic holds that,
// unconfigured, both endpoints challenge for Basic in their own realm.
func TestEveryEndpointIsGuardedWithNoConfigurationAndAsksForBasic(t *testing.T) {
	serve(t, ``, func(h *host) {
		requireBasicChallenge(t, "the public endpoint with no credential", h.send("public", "GET", "/orders"), "public")
		requireBasicChallenge(t, "the endpoint nobody listed with no credential", h.send("ops", "GET", "/health"), "ops")

		for _, c := range []struct{ endpoint, path string }{{"public", "/orders"}, {"ops", "/health"}} {
			got := h.send(c.endpoint, "GET", c.path, as("erin", "erin-password"))
			if got.status != nethttp.StatusOK {
				t.Errorf("%s with the right password answered %d, want 200; the body was %q", c.endpoint, got.status, got.body)
			}
			if got.header.Get("WWW-Authenticate") != "" {
				t.Errorf("a pass on %s carries a challenge", c.endpoint)
			}
		}
	})
}

// TestAPassIsStampedWithTheStaticAccountAndTheBackendThatVerifiedIt holds what a
// handler is told about the passing identity.
func TestAPassIsStampedWithTheStaticAccountAndTheBackendThatVerifiedIt(t *testing.T) {
	serve(t, ``, func(h *host) {
		got := h.send("public", "GET", "/orders", as("erin", "erin-password"))
		if want := "static.erin via static as Basic"; got.status != nethttp.StatusOK || got.body != want {
			t.Errorf("answered %d with %q, want 200 and %q", got.status, got.body, want)
		}
	})
}

// TestAWrongPasswordIsRefusedWithTheBasicChallenge holds that a wrong, unknown or
// malformed credential all get the same 401.
func TestAWrongPasswordIsRefusedWithTheBasicChallenge(t *testing.T) {
	serve(t, ``, func(h *host) {
		wrong := h.send("public", "GET", "/orders", as("bob", "not-bob-password"))
		requireBasicChallenge(t, "a wrong password", wrong, "public")
		unknown := h.send("public", "GET", "/orders", as("nobody", "bob-password"))
		requireBasicChallenge(t, "a name that is not in the table", unknown, "public")
		garbage := h.send("public", "GET", "/orders", func(r *nethttp.Request) {
			r.Header.Set("Authorization", "Basic !!not-base64!!")
		})
		requireBasicChallenge(t, "a credential that is not base64", garbage, "public")

		if wrong.body != unknown.body || wrong.body != garbage.body {
			t.Errorf("the three refusals read %q, %q and %q, and a client should not be able to tell them apart",
				wrong.body, unknown.body, garbage.body)
		}
	})
}

// TestEntriesInEveryFormTheTableAcceptsAuthenticate holds that each written table
// form admits its own password and refuses another.
func TestEntriesInEveryFormTheTableAcceptsAuthenticate(t *testing.T) {
	serve(t, ``, func(h *host) {
		for _, c := range []struct{ name, form, password string }{
			{"alice", "Argon2id", "alice-password"},
			{"bob", "PBKDF2 in the PHC form", "bob-password"},
			{"carol", "PBKDF2 in the legacy form", "carol-password"},
		} {
			got := h.send("public", "GET", "/orders", as(c.name, c.password))
			if want := "static." + c.name + " via static as Basic"; got.status != nethttp.StatusOK || got.body != want {
				t.Errorf("%s entry: answered %d with %q, want 200 and %q", c.form, got.status, got.body, want)
			}
			requireBasicChallenge(t, c.form+" entry with another password",
				h.send("public", "GET", "/orders", as(c.name, "someone-else")), "public")
		}
	})
}

// TestFailuresPastTheFreeAttemptsLockTheNameEvenAgainstItsRightPassword holds
// the back-off from the outside.
func TestFailuresPastTheFreeAttemptsLockTheNameEvenAgainstItsRightPassword(t *testing.T) {
	serve(t, ``, func(h *host) {
		if got := h.send("public", "GET", "/orders", as("dave", "dave-password")); got.status != nethttp.StatusOK {
			t.Fatalf("the right password answered %d before any failure", got.status)
		}
		for i := 1; i <= 6; i++ {
			requireBasicChallenge(t, fmt.Sprintf("failure %d", i),
				h.send("public", "GET", "/orders", as("dave", "wrong")), "public")
		}
		requireBasicChallenge(t, "the right password after the free attempts were used",
			h.send("public", "GET", "/orders", as("dave", "dave-password")), "public")

		if got := h.send("public", "GET", "/orders", as("erin", "erin-password")); got.status != nethttp.StatusOK {
			t.Errorf("another name answered %d while dave was locked, want 200: the lock is per name", got.status)
		}

		for i := 1; i <= 5; i++ {
			requireBasicChallenge(t, fmt.Sprintf("frank's failure %d", i),
				h.send("public", "GET", "/orders", as("frank", "wrong")), "public")
		}
		if got := h.send("public", "GET", "/orders", as("frank", "frank-password")); got.status != nethttp.StatusOK {
			t.Errorf("the right password after five failures answered %d, want 200: the five free attempts "+
				"are free", got.status)
		}
	})
}

// TestParallelRequestsWithTheRightPasswordAreNeverRefusedByTheirOwnTraffic holds
// that a burst presenting a checked password is all admitted.
func TestParallelRequestsWithTheRightPasswordAreNeverRefusedByTheirOwnTraffic(t *testing.T) {
	const workers, requests = 24, 150
	serve(t, ``, func(h *host) {
		if got := h.send("public", "GET", "/orders", as("erin", "erin-password")); got.status != nethttp.StatusOK {
			t.Fatalf("the first check of the right password answered %d", got.status)
		}

		var wg sync.WaitGroup
		refused := make(chan int, workers*requests)
		start := make(chan struct{})
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client := &nethttp.Client{Timeout: 10 * time.Second, Transport: &nethttp.Transport{MaxIdleConnsPerHost: 1}}
				defer client.CloseIdleConnections()
				<-start
				for range requests {
					req, err := nethttp.NewRequestWithContext(h.ctx, "GET", "http://"+h.addresses["public"]+"/orders", nil)
					if err != nil {
						refused <- 0
						continue
					}
					req.SetBasicAuth("erin", "erin-password")
					resp, err := client.Do(req)
					if err != nil {
						refused <- 0
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != nethttp.StatusOK {
						refused <- resp.StatusCode
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		close(refused)
		counts := map[int]int{}
		for status := range refused {
			counts[status]++
		}
		if len(counts) != 0 {
			t.Errorf("requests with the right password were not all admitted, by status (0 is no response): %v", counts)
		}
	})
}

// TestNarrowingTheEndpointsLeavesTheOthersWithNoLayer holds that an endpoint left
// out of the list serves requests with no identity.
func TestNarrowingTheEndpointsLeavesTheOthersWithNoLayer(t *testing.T) {
	serve(t, `"endpoints":["public"]`, func(h *host) {
		requireBasicChallenge(t, "the guarded endpoint with no credential", h.send("public", "GET", "/orders"), "public")
		got := h.send("ops", "GET", "/health")
		if got.status != nethttp.StatusOK || got.body != "nobody" {
			t.Errorf("the endpoint left out of the list answered %d with %q, want 200 and no identity", got.status, got.body)
		}
	})
}
