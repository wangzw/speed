package saml

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beevik/etree"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/humanverify"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/authn/mfa"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	speedhttp "github.com/vislake/speed/pkg/http"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// The deployment the chain runs as: a loopback public address, the default
// base path, and the identifier of the provider the fixture signs for, which is
// UUID text as a register's identifier is, because a session records it.
const (
	chainProvider  = "0198f0aa-0000-7000-8000-0000000000aa"
	chainPublicURL = "http://127.0.0.1"
	chainBase      = "/api/v1/auth"
	chainListening = "an HTTP listener is listening"
	chainBound     = 5 * time.Second
)

// The parts of the bounce page the chain reads back.
var (
	chainAction = regexp.MustCompile(`<form id="resume" method="post" action="([^"]*)">`)
	chainInput  = regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)">`)
)

// chainLog keeps the records the entry point writes, so the chain can read back
// the address the operating system picked for the endpoint.
type chainLog struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *chainLog) Enabled(context.Context, slog.Level) bool { return true }
func (c *chainLog) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *chainLog) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *chainLog) WithGroup(string) slog.Handler      { return c }

func (c *chainLog) address() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message != chainListening {
			continue
		}
		var address string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "address" {
				address = a.Value.String()
			}
			return true
		})
		return address
	}
	return ""
}

// chainLogging delivers the logging capability over the kept records.
type chainLogging struct{ logger *slog.Logger }

func (l chainLogging) Named(string) *slog.Logger { return l.logger }
func (chainLogging) Redaction() log.Redaction    { return chainNoRules{} }

type chainNoRules struct{}

func (chainNoRules) AddKeys(...string)              {}
func (chainNoRules) AddPattern(string, log.Matcher) {}

// chain is one running assembly of the login engine and this method.
type chain struct {
	t       *testing.T
	ctx     context.Context
	address string
	client  *http.Client
	idp     *idp
	metrics *sdkmetric.ManualReader
}

// chainReply is what one request got back.
type chainReply struct {
	status int
	header http.Header
	body   string
}

// runChain assembles the real login stack with this method, a provider register
// holding the fixture's provider and a metric reader, serves it on loopback and
// runs use against it.
func runChain(t *testing.T, use func(c *chain, admin authn.AccountAdmin)) {
	t.Helper()
	identity := newIDP(t)
	row := providerRow(providerConfigJSON(t, identity, nil))
	row.ID = chainProvider
	providers := newFakeProviders(row)
	reader := sdkmetric.NewManualReader()
	kept := &chainLog{}
	logger := slog.New(kept)

	const (
		providersName = "stand-in.providers"
		metricsName   = "stand-in.metrics"
	)
	modules := []core.Module{
		sqlite.Module(),
		{
			Name:     "stand-in.logging",
			Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
			New:      func(context.Context, *core.Registry) (any, error) { return chainLogging{logger: logger}, nil },
		},
		{Name: providersName, Provides: []core.Provision{{Token: (*authn.SSOProviders)(nil), Exclusive: true}}},
		{Name: metricsName, Provides: []core.Provision{{Token: (*observability.MeterProvider)(nil), Exclusive: true}}},
		speedhttp.Module(), kvmemory.Module(), eventmemory.Module(), settings.Module(), authnmount.Module(),
		gate.Module(), humanverify.Module(), account.Module(), provisioningtest.Module(), session.Module(),
		mfa.Module(), login.Module(), Module(),
	}
	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": filepath.Join(t.TempDir(), "authn.db")}},
		"http": map[string]any{
			"listeners": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{"public": map[string]any{"listener": "public"}},
		},
		"kv": map[string]any{"memory": map[string]any{"replicas": 1}},
		"authn": map[string]any{
			"gate": map[string]any{"mount": map[string]any{
				"endpoints":  map[string]any{"default": "public"},
				"public-url": chainPublicURL,
			}},
			"humanverify": map[string]any{"mode": "off", "phone-mode": "off"},
		},
		"settings": map[string]any{},
		"event":    map[string]any{},
	}
	cfg := loadChainConfig(t, document, modules)

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(cfg, config.ModuleName)
	reg.Supply(providers, providersName)
	reg.Supply(observability.MeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))), metricsName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			address := kept.address()
			if address == "" {
				return fmt.Errorf("the entry point wrote no record of binding the public endpoint")
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			use(&chain{
				t: t, ctx: ctx, address: address, idp: identity, metrics: reader,
				client: &http.Client{
					Timeout: chainBound,
					// Every redirect is read as the answer it is.
					CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				},
			}, admin)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// loadChainConfig loads the configuration the way a host does.
func loadChainConfig(t *testing.T, document map[string]any, modules []core.Module) config.Reader {
	t.Helper()
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(locator, body, 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDSAMLCHAIN", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// post sends a POST with the given body, content type and headers, and no
// cookie but the one a header names.
func (c *chain) post(path, contentType, body string, hdr ...string) chainReply {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, "http://"+c.address+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatalf("building POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return c.do(req)
}

// get sends a GET carrying the given cookie.
func (c *chain) get(path, cookie string) chainReply {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, "http://"+c.address+path, nil)
	if err != nil {
		c.t.Fatalf("building GET %s: %v", path, err)
	}
	req.Header.Set("Cookie", cookie)
	return c.do(req)
}

func (c *chain) do(req *http.Request) chainReply {
	c.t.Helper()
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s got no response: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("reading %s %s: %v", req.Method, req.URL.Path, err)
	}
	return chainReply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

// cookieOf is the "name=value" pair of the cookie a reply set under a name that
// contains part.
func cookieOf(t *testing.T, r chainReply, part string) string {
	t.Helper()
	for _, line := range r.header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(line, ";")
		if name, _, _ := strings.Cut(pair, "="); strings.Contains(name, part) {
			return strings.TrimSpace(pair)
		}
	}
	t.Fatalf("the reply set no %s cookie; Set-Cookie %v", part, r.header.Values("Set-Cookie"))
	return ""
}

// holdsLoopbackCookies checks every cookie a reply set on the chain's loopback
// http public URL: its name carries no __Host- or __Secure- prefix and it is not
// Secure, since WebKit refuses either over http, and it keeps HttpOnly, Path=/,
// no Domain and an explicit SameSite.
func holdsLoopbackCookies(t *testing.T, r chainReply) {
	t.Helper()
	cookies := (&http.Response{Header: r.header}).Cookies()
	if len(cookies) == 0 {
		t.Fatalf("the reply set no cookie; Set-Cookie %v", r.header.Values("Set-Cookie"))
	}
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, "__") {
			t.Errorf("the cookie %q carries a prefix on the loopback http address %s", c.Name, chainPublicURL)
		}
		if c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite == http.SameSiteDefaultMode {
			t.Errorf("the cookie %s is Secure=%v HttpOnly=%v Path=%q Domain=%q SameSite=%v, want no Secure, "+
				"HttpOnly, Path=/, no Domain and a SameSite", c.Name, c.Secure, c.HttpOnly, c.Path, c.Domain, c.SameSite)
		}
	}
}

// spStart begins an SP-initiated SAML login and returns the flow cookie, the
// identifier of the AuthnRequest the method drew and the RelayState it sent.
func (c *chain) spStart() (cookie, requestID, relay string) {
	c.t.Helper()
	body := `{"method":"saml","input":{"provider":"` + chainProvider + `"},"return_to":"/after"}`
	start := c.post(chainBase+"/flows", "application/json", body, "Sec-Fetch-Site", "same-origin")
	if start.status != http.StatusOK {
		c.t.Fatalf("starting the SAML flow answered %d: %s", start.status, start.body)
	}
	var reply struct {
		Next struct {
			Kind string `json:"kind"`
			URL  string `json:"url"`
		} `json:"next"`
	}
	if err := json.Unmarshal([]byte(start.body), &reply); err != nil || reply.Next.Kind != "redirect" {
		c.t.Fatalf("the start answered %s, want a redirect step (%v)", start.body, err)
	}
	target, err := url.Parse(reply.Next.URL)
	if err != nil {
		c.t.Fatalf("the redirect does not parse: %v", err)
	}
	deflated, err := base64.StdEncoding.DecodeString(target.Query().Get("SAMLRequest"))
	if err != nil {
		c.t.Fatalf("the SAMLRequest is not base64: %v", err)
	}
	inflated, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflated)))
	if err != nil {
		c.t.Fatalf("the SAMLRequest does not inflate: %v", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(inflated); err != nil {
		c.t.Fatalf("the AuthnRequest does not parse: %v", err)
	}
	requestID = doc.Root().SelectAttrValue("ID", "")
	relay = target.Query().Get("RelayState")
	if requestID == "" || relay == "" {
		c.t.Fatalf("the redirect carries request %q and RelayState %q, want both", requestID, relay)
	}
	return cookieOf(c.t, start, "authn-flow"), requestID, relay
}

// response is a valid response to requestID, signed by the fixture's provider,
// for the deployment's ACS and entity.
func (c *chain) response(requestID, assertionID string) string {
	c.t.Helper()
	acs := chainPublicURL + chainBase + "/callback/" + chainProvider
	o := defaultOpts()
	o.destination = acs
	o.scdRecipient = acs
	o.audience = chainPublicURL + chainBase + "/sso/saml/" + chainProvider + "/metadata"
	o.inResponseTo = requestID
	o.scdInResponse = requestID
	o.assertionID = assertionID
	return signResponse(c.t, c.idp, o)
}

// deliver posts a response to the ACS as the provider's page does in a browser,
// a cross-site navigation that carries no Lax cookie, and returns the bounce
// page's resume target and form.
func (c *chain) deliver(encoded, relay string) (action, form string) {
	c.t.Helper()
	body := url.Values{"SAMLResponse": {encoded}, "RelayState": {relay}}.Encode()
	page := c.post(chainBase+"/callback/"+chainProvider, "application/x-www-form-urlencoded", body,
		"Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "navigate", "Origin", "https://idp.example.com")
	if page.status != http.StatusOK || !strings.HasPrefix(page.header.Get("Content-Type"), "text/html") {
		c.t.Fatalf("the ACS answered %d %q, want the bounce page: %s", page.status, page.header.Get("Content-Type"), page.body)
	}
	if len(page.header.Values("Set-Cookie")) != 0 {
		c.t.Errorf("the ACS set cookies %v, want none", page.header.Values("Set-Cookie"))
	}
	match := chainAction.FindStringSubmatch(page.body)
	if match == nil {
		c.t.Fatalf("the bounce page carries no resume form: %s", page.body)
	}
	resume := url.Values{}
	for _, m := range chainInput.FindAllStringSubmatch(page.body, -1) {
		resume.Add(html.UnescapeString(m[1]), html.UnescapeString(m[2]))
	}
	return html.UnescapeString(match[1]), resume.Encode()
}

// assertions is the authn.sso.assertions count for SAML and an outcome.
func (c *chain) assertions(outcome string) int64 {
	c.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := c.metrics.Collect(context.Background(), &rm); err != nil {
		c.t.Fatalf("collecting the metrics: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != assertionsInstrument || !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				p, _ := dp.Attributes.Value(protocolAttribute)
				o, _ := dp.Attributes.Value(outcomeAttribute)
				if p.AsString() == providerProtocol && o.AsString() == outcome {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// TestAnSPInitiatedLoginCompletesThroughTheBounce holds the SP-initiated SAML
// login on a Lax flow cookie, over the real routes: the response the provider's
// page posts cross-site reaches the ACS without the cookie and is bounced, and
// the same-origin resume post carrying the cookie completes the login. A valid
// response for another flow, delivered into a victim's browser the same way,
// fails the victim's flow with no session, and the method counts it as other.
func TestAnSPInitiatedLoginCompletesThroughTheBounce(t *testing.T) {
	runChain(t, func(c *chain, admin authn.AccountAdmin) {
		acct, err := admin.Create(c.ctx, authn.CreateAccount{Kind: authn.KindUser, Username: "alice", Via: "admin"})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if err := admin.LinkExternal(c.ctx, acct.ID, authn.ExternalLink{
			Provider: providerName + ":" + chainProvider, Subject: "alice",
		}); err != nil {
			t.Fatalf("binding the account to the provider's subject: %v", err)
		}

		cookie, requestID, relay := c.spStart()
		action, form := c.deliver(c.response(requestID, "_assertion-own"), relay)
		if action != chainBase+"/callback/"+chainProvider+"/resume" {
			t.Fatalf("the bounce page posts to %q, want the provider's resume route", action)
		}
		done := c.post(action, "application/x-www-form-urlencoded", form,
			"Cookie", cookie, "Sec-Fetch-Site", "same-origin", "Sec-Fetch-Mode", "navigate", "Origin", chainPublicURL)
		if done.status != http.StatusSeeOther || done.header.Get("Location") != "/after" {
			t.Fatalf("the resume post answered %d to %q, want 303 to the return target: %s",
				done.status, done.header.Get("Location"), done.body)
		}
		holdsLoopbackCookies(t, done)
		if !strings.HasPrefix(cookie, "authn-flow=") {
			t.Errorf("the start set the flow cookie %q, want authn-flow on a loopback http address", cookie)
		}
		sid := cookieOf(t, done, "sid")
		if !strings.HasPrefix(sid, "authn-sid=") {
			t.Errorf("the callback set the session cookie %q, want authn-sid on a loopback http address", sid)
		}
		status := c.get(chainBase+"/session", sid)
		if status.status != http.StatusOK || !strings.Contains(status.body, `"authenticated":true`) {
			t.Fatalf("the session the login made reads %d: %s", status.status, status.body)
		}
		if got := c.assertions(outcomeAccepted); got != 1 {
			t.Errorf("authn.sso.assertions counts %d accepted, want one", got)
		}

		victim, _, _ := c.spStart()
		_, attackerRequest, attackerRelay := c.spStart()
		action, form = c.deliver(c.response(attackerRequest, "_assertion-attacker"), attackerRelay)
		forged := c.post(action, "application/x-www-form-urlencoded", form,
			"Cookie", victim, "Sec-Fetch-Site", "same-origin", "Origin", chainPublicURL)
		if forged.status != http.StatusUnauthorized || !strings.Contains(forged.body, "login-failed") {
			t.Errorf("a response for another flow answered %d, want 401 login-failed: %s", forged.status, forged.body)
		}
		for _, line := range forged.header.Values("Set-Cookie") {
			if strings.Contains(line, "sid=") {
				t.Errorf("a response for another flow wrote the session cookie %q", line)
			}
		}
		current := c.get(chainBase+"/flows/current", victim)
		if current.status != http.StatusUnauthorized || !strings.Contains(current.body, "flow-invalid") {
			t.Errorf("the victim's flow reads %d after the forged response, want 401 flow-invalid: %s",
				current.status, current.body)
		}
		if got := c.assertions(outcomeOther); got != 1 {
			t.Errorf("authn.sso.assertions counts %d as other, want the forged response", got)
		}
	})
}
