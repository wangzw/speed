package admission

import (
	"context"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases built on this file drive the layer's handler the way a request
// reaches it: a context holding what the layers outside would have left in it,
// a handler behind that answers 200 when the request gets through, and a reply
// read off the recorder. Every collaborator is a stub whose answers the case
// chooses and whose calls the case counts.

const (
	acme   = "acme"
	globex = "globex"

	alice authn.AccountID = "alice"
	bob   authn.AccountID = "bob"

	// idpOfAcme and idpOfGlobex are provider identifiers in the canonical
	// UUID form an identity holds.
	idpOfAcme   = "11111111-1111-4111-8111-111111111111"
	idpOfGlobex = "22222222-2222-4222-8222-222222222222"
	idpUnknown  = "33333333-3333-4333-8333-333333333333"
)

// boundTenant is how the resolution layer binds a tenant onto a context: the
// identifier and the log field both.
var boundTenant = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

// inTenant returns ctx as the resolution layer leaves it for this tenant.
func inTenant(ctx context.Context, id string) context.Context {
	return tenantctx.Bind(ctx, id, boundTenant)
}

// base is a context carrying the rig's logger, the way the entry point injects
// the request's logger before any layer runs.
func (r *rig) base() context.Context {
	return log.WithLogger(context.Background(), slog.New(r.logs))
}

// as returns a context holding an identity of alice unless an option names
// another account, in the tenant, the way the layers outside this one leave it.
func (r *rig) as(tenantID string, opts ...authntest.Option) context.Context {
	r.t.Helper()
	opts = append([]authntest.Option{authntest.WithAccount(alice)}, opts...)
	ctx := authntest.Context(r.t, r.base(), opts...)
	if tenantID == "" {
		return ctx
	}
	return inTenant(ctx, tenantID)
}

// anon returns a context for a request with no identity on a route of the
// access level, in the tenant when one is named.
func (r *rig) anon(tenantID string, access authn.Access) context.Context {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, r.base(), access)
	if tenantID == "" {
		return ctx
	}
	return inTenant(ctx, tenantID)
}

// bare returns a context no gateway has touched, in the tenant when one is
// named.
func (r *rig) bare(tenantID string) context.Context {
	if tenantID == "" {
		return r.base()
	}
	return inTenant(r.base(), tenantID)
}

// memberStub is the account module's memberships.
type memberStub struct {
	authn.Memberships

	mu          sync.Mutex
	members     map[cacheKey]bool
	rows        map[cacheKey]authn.Membership
	err         error
	rowErr      error
	isMemberN   int
	membershipN int
	// during runs inside IsMember, before it answers.
	during func()
}

func newMemberStub() *memberStub {
	return &memberStub{members: map[cacheKey]bool{}, rows: map[cacheKey]authn.Membership{}}
}

func (m *memberStub) add(account authn.AccountID, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.members[cacheKey{account: account, tenant: tenantIDOf(tenantID)}] = true
}

func (m *memberStub) remove(account authn.AccountID, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.members, cacheKey{account: account, tenant: tenantIDOf(tenantID)})
}

func (m *memberStub) setRow(row authn.Membership) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[cacheKey{account: row.Account, tenant: tenantIDOf(row.Tenant)}] = row
}

func (m *memberStub) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *memberStub) failRow(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rowErr = err
}

func (m *memberStub) IsMember(_ context.Context, id authn.AccountID, tenantID string) (bool, error) {
	m.mu.Lock()
	m.isMemberN++
	err, member, during := m.err, m.members[cacheKey{account: id, tenant: tenantIDOf(tenantID)}], m.during
	m.mu.Unlock()
	if during != nil {
		during()
	}
	return member, err
}

func (m *memberStub) Membership(_ context.Context, tenantID string, id authn.AccountID) (authn.Membership, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.membershipN++
	row, found := m.rows[cacheKey{account: id, tenant: tenantIDOf(tenantID)}]
	return row, found, m.rowErr
}

func (m *memberStub) reads() (isMember, membership int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isMemberN, m.membershipN
}

// deciderStub is the permission model.
type deciderStub struct {
	mu     sync.Mutex
	allow  bool
	err    error
	panics bool
	asked  []authn.Decision
}

func (d *deciderStub) Decide(_ context.Context, q authn.Decision) (bool, error) {
	d.mu.Lock()
	d.asked = append(d.asked, q)
	allow, err, panics := d.allow, d.err, d.panics
	d.mu.Unlock()
	if panics {
		panic("the decider is broken")
	}
	return allow, err
}

func (d *deciderStub) questions() []authn.Decision {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]authn.Decision(nil), d.asked...)
}

// providersStub is the registry of identity providers. Only Tenant is answered.
type providersStub struct {
	authn.SSOProviders

	mu     sync.Mutex
	owners map[string]string
	err    error
	calls  int
}

func (p *providersStub) Tenant(_ context.Context, id string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.owners[id], p.err
}

func (p *providersStub) lookups() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func standardProviders() *providersStub {
	return &providersStub{owners: map[string]string{idpOfAcme: acme, idpOfGlobex: globex}}
}

// settingsStub is the settings module's store, with the fallback it gives a
// read: the tenant's own row, then the global row, then the declared default.
type settingsStub struct {
	settings.Store

	mu     sync.Mutex
	rows   map[settingRow]any
	errors map[string]error
	reads  []settingRead
}

type settingRow struct {
	layer settings.Layer
	key   string
}

type settingRead struct {
	layer settings.Layer
	key   string
}

func newSettingsStub() *settingsStub {
	return &settingsStub{rows: map[settingRow]any{}, errors: map[string]error{}}
}

func (s *settingsStub) set(layer settings.Layer, key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[settingRow{layer: layer, key: key}] = value
}

func (s *settingsStub) failKey(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors[key] = err
}

func (s *settingsStub) Get(layer settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, settingRead{layer: layer, key: key})
	if err := s.errors[key]; err != nil {
		return err
	}
	value, found := s.rows[settingRow{layer: layer, key: key}]
	if !found {
		value, found = s.rows[settingRow{layer: settings.Global(), key: key}]
	}
	switch out := target.(type) {
	case *bool:
		*out = found && value.(bool)
	case *time.Duration:
		if found {
			*out = value.(time.Duration)
		} else {
			*out = defaults.CheckTTL
		}
	default:
		panic("settingsStub: a target of a type no setting here is declared with")
	}
	return nil
}

func (s *settingsStub) readsOf(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reads {
		if r.key == key {
			n++
		}
	}
	return n
}

// clock is the time the cache reads.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// record is one log record a case reads back.
type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// recorder is a log handler that keeps what was written.
type recorder struct {
	mu      *sync.Mutex
	records *[]record
	bound   []slog.Attr
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, records: &[]record{}}
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.bound)+rec.NumAttrs())
	for _, a := range r.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.records = append(*r.records, record{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := append(append([]slog.Attr(nil), r.bound...), attrs...)
	return &recorder{mu: r.mu, records: r.records, bound: bound}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) all() []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record(nil), *r.records...)
}

// atLevel returns the records written at one level.
func (r *recorder) atLevel(level slog.Level) []record {
	var out []record
	for _, rec := range r.all() {
		if rec.level == level {
			out = append(out, rec)
		}
	}
	return out
}

// rig is one layer with every collaborator a stub.
type rig struct {
	t         *testing.T
	l         *layer
	members   *memberStub
	clock     *clock
	logs      *recorder
	providers *providersStub
	decider   *deciderStub
	store     *settingsStub
}

// newRig builds a layer guarding "public" over a membership stub in which
// alice is a member of acme. It has no decider, no registry of providers and no
// settings; a case adds what it needs.
func newRig(t *testing.T) *rig {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	members := newMemberStub()
	members.add(alice, acme)
	clk := &clock{now: time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)}
	l := newLayer(resolved{endpoints: []string{"public"}}, members)
	l.cache = newDecisionCache(clk.read)
	logs := newRecorder()
	l.logger = slog.New(logs)
	return &rig{t: t, l: l, members: members, clock: clk, logs: logs}
}

func (r *rig) withProviders() *providersStub {
	r.providers = standardProviders()
	r.l.providers = r.providers
	return r.providers
}

func (r *rig) withDecider(allow bool) *deciderStub {
	r.decider = &deciderStub{allow: allow}
	r.l.decider = r.decider
	return r.decider
}

func (r *rig) withSettings() *settingsStub {
	r.store = newSettingsStub()
	r.l.store = r.store
	return r.store
}

// reply is what the client got back.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

// reached reports whether the request got through to the handler behind the
// layer.
func (rp reply) reached() bool { return rp.status == nethttp.StatusOK && rp.body == "reached" }

// serve sends one request carrying ctx through the layer and a handler behind it.
func (r *rig) serve(ctx context.Context) reply {
	r.t.Helper()
	return serveThrough(r.t, r.l.middlewareFor("public").Handler, ctx)
}

func serveThrough(t *testing.T, layerHandler gin.HandlerFunc, ctx context.Context) reply {
	t.Helper()
	engine := gin.New()
	engine.Use(layerHandler)
	engine.GET("/orders", func(c *gin.Context) { c.String(nethttp.StatusOK, "reached") })
	req := httptest.NewRequestWithContext(ctx, nethttp.MethodGet, "/orders", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	return reply{status: rec.Code, header: rec.Header().Clone(), body: string(body)}
}

// counts reads the decision counts by outcome name.
func (l *layer) countsByName() map[string]int64 {
	out := make(map[string]int64, outcomeCount)
	for o := range l.counts {
		out[outcome(o).String()] = l.counts[o].Load()
	}
	return out
}

// onlyCounted fails the case unless exactly one request was counted, under the
// outcome named.
func (r *rig) onlyCounted(want outcome) {
	r.t.Helper()
	got := r.l.countsByName()
	var total int64
	for _, n := range got {
		total += n
	}
	if total != 1 || got[want.String()] != 1 {
		r.t.Fatalf("the counts are %v, want exactly one request, counted as %s", got, want)
	}
}

// levelError and levelInfo name the log levels the cases read back.
const (
	levelError = slog.LevelError
	levelInfo  = slog.LevelInfo
	levelWarn  = slog.LevelWarn
)

var errBoom = errors.New("the dependency is down")

func tenantIDOf(id string) tenant.ID { return tenant.ID(id) }

// busStub is the event bus: a publish reaches every handler subscribed to the
// type, in the publisher's goroutine, and nothing else about delivery is
// modelled.
type busStub struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newBusStub() *busStub { return &busStub{handlers: map[string][]event.Handler{}} }

func (b *busStub) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *busStub) Publish(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range hs {
		if err := h(ctx, evt); err != nil {
			return err
		}
	}
	return nil
}

func (b *busStub) subscribed(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[eventType])
}

// members returns the membership stub a product built over one holds.
func (l *layer) members() *memberStub { return l.memberships.(*memberStub) }

func newDiscardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// authntestIdentity is an identity of alice, read back out of a context.
func authntestIdentity(t *testing.T) authn.Identity {
	t.Helper()
	id, ok := authn.FromContext(authntest.Context(t, context.Background(), authntest.WithAccount(alice)))
	if !ok {
		t.Fatal("authntest built no identity")
	}
	return id
}
