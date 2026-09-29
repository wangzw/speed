package ratelimit

import (
	"context"
	"fmt"
	"math"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// limiter is this module's product. It delivers the Limiter capability.
//
// It holds configuration and nothing derived from it. The capabilities the
// decision needs are all taken up in Init, which is also where the layer is
// registered.
type limiter struct {
	endpoints []string
	store     settings.Store
	counters  kv.KV
	signal    *signal

	// now reads the wall clock. The window index is derived from it, so a
	// test pins it to make the arithmetic reproducible.
	now func() time.Time
}

func newLimiter(endpoints []string) *limiter {
	return &limiter{
		endpoints: append([]string(nil), endpoints...),
		signal:    newSignal(),
		now:       time.Now,
	}
}

// limit makes *limiter satisfy Limiter.
//
// Nothing calls it. The interface is an ordering anchor, and the registry only
// requires that the product implement whatever the descriptor delivers.
func (*limiter) limit() {}

// wire takes up the capabilities and registers the layer on every endpoint the
// configuration names.
func (l *limiter) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return takeUp("the entry point", err)
	}
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return takeUp("settings", err)
	}
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return takeUp("the key-value primitives", err)
	}
	l.store = store
	l.counters = counters

	for _, name := range l.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return takeUp("endpoint "+strconv.Quote(name), err)
		}
		endpoint.Use(l.middleware())
	}
	return nil
}

func takeUp(what string, err error) error {
	return fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
}

// middleware is the layer this module registers.
//
// Provides carries the same token the descriptor delivers the capability
// under, which is the only anchor another layer has for placing itself inside
// or outside rate limiting. The three After entries are what this layer needs
// to read an identity string and a tenant, and to sit inside the audit
// request layer: pointing at audit's own token is the way that module settled
// for modules that want to order against it, rather than a numeric preference.
//
// Order carries no constraint here — every position this layer needs is fixed
// by the After entries — so it stays at the zero value.
func (l *limiter) middleware() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*Limiter)(nil)},
		After: []core.Token{
			(*authn.Gate)(nil),
			(*tenant.Resolution)(nil),
			(*audit.Audit)(nil),
		},
		Order: 0,
		Wrap:  l.wrap,
	}
}

func (l *limiter) wrap(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		identity, identified := authn.FromContext(r.Context())
		l.signal.settle(l.decide(w, r, next, identity, identified))
	})
}

// decision is what one request's pass through this layer produced: the class of
// failure that made the layer stand aside, if any, and the classes the decision
// evaluated on the way there.
//
// The two travel together because only a class a decision evaluated can be
// cleared by it. A decision returns at the first class that trips, so every
// class behind that one was never asked about; clearing one of those would
// report a recovery for a condition nothing checked.
type decision struct {
	// tripped is the class that made this layer stand aside, or the empty
	// reason when nothing did.
	tripped reason

	// evaluated holds, in the order they were reached, the classes this
	// decision asked about. The class in tripped, when there is one, is the
	// last entry.
	evaluated []reason

	// tenant is the tenant segment the decision read its thresholds and
	// counted under, empty for a request that carries no tenant. The signal
	// names it on the line a thresholds failure first writes.
	tenant string
}

// decide runs one request through the count and answers it.
//
// The identity arrives as an argument rather than being taken out of the
// context in here: Go keeps the package that writes an identity to the
// authentication capability directory, so reading one is the single step of
// this layer a test in this package cannot drive. Everything below it can.
//
// The returned decision names the class of failure that made this layer stand
// aside, if any, and the classes it evaluated; the caller accounts for it.
// Every failing path lets the request through: a refusal would turn one bad
// backend or one bad row into a refusal of the whole endpoint, and rate
// limiting here is an abuse surface rather than a data boundary.
func (l *limiter) decide(w nethttp.ResponseWriter, r *nethttp.Request, next nethttp.Handler, identity authn.Identity, identified bool) decision {
	if !identified {
		// No identity means nothing to count against. An exempt route, a host
		// that took no authentication module into the command, and a layer
		// whose After edge did not land all arrive here.
		next.ServeHTTP(w, r)
		return decision{}
	}

	layer, tenantSegment := l.scopeOf(r.Context())
	d := decision{tenant: tenantSegment}
	window, limit := l.thresholds(layer, &d)
	if d.tripped != "" {
		next.ServeHTTP(w, r)
		return d
	}

	allowed, retryAfter := l.count(r.Context(), identity.Key(), tenantSegment, window, limit, &d)
	if d.tripped != "" {
		next.ServeHTTP(w, r)
		return d
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.WriteHeader(nethttp.StatusTooManyRequests)
		return d
	}
	next.ServeHTTP(w, r)
	return d
}

// scopeOf names the layer this request reads its thresholds from, and the
// segment its counts are kept under.
//
// A request that carries a tenant reads that tenant's row and counts inside
// it; one that carries none reads the global row and counts under an empty
// segment, which no tenant identifier can be.
func (l *limiter) scopeOf(ctx context.Context) (settings.Layer, string) {
	if id, ok := tenant.FromContext(ctx); ok {
		return settings.ForTenant(string(id)), string(id)
	}
	return settings.Global(), ""
}

// maxWindow is the longest counting window whose double still fits a
// time.Duration.
//
// The current window's key is created with an expiry of two windows, and the
// primitives read an expiry of zero or less as "no expiry at all": a window
// past this bound overflows the double, has its key written with no expiry,
// and leaves that key in the store forever.
const maxWindow = time.Duration(math.MaxInt64) / 2

// thresholds reads the two settings items, checks them against their domains,
// and records in d the classes it evaluates.
//
// Both reads go to the settings module's memory snapshot, one key at a time:
// the two values can therefore come from different snapshots, and a decision
// may pair a window with a limit that were never in force together.
//
// The settings store refuses a write outside either domain, and a read still
// hands out whatever a row holds: a row stored before the domain was declared,
// or written around the store, reaches this check, which is why it stays.
func (l *limiter) thresholds(layer settings.Layer, d *decision) (time.Duration, int) {
	d.evaluated = append(d.evaluated, reasonThresholdRead)

	var window time.Duration
	if err := l.store.Get(layer, windowPath, &window); err != nil {
		d.tripped = reasonThresholdRead
		return 0, 0
	}
	var limit int
	if err := l.store.Get(layer, limitPath, &limit); err != nil {
		d.tripped = reasonThresholdRead
		return 0, 0
	}

	d.evaluated = append(d.evaluated, reasonThresholdDomain)
	if !windowInDomain(window) || !limitInDomain(limit) {
		// A limit of zero would refuse every request that carries an identity
		// string, since the increment happens before the decision and the
		// current count is at least one; a window of zero leaves the window
		// index undefined, and a window too long to double leaves the counting
		// key without an expiry. None of these is a value this module acts on.
		d.tripped = reasonThresholdDomain
		return 0, 0
	}
	return window, limit
}

// count decides one request against the estimate for its current window.
//
// The estimate is the current window's count plus the previous window's count
// weighted by the part of this window that has not elapsed. The increment
// happens before the decision, so a refused request still consumes: counting
// only what was let through would let a caller that is already over its limit
// retry without spending anything, and the second half of the window would
// open up as the weight falls.
func (l *limiter) count(ctx context.Context, subject, tenantSegment string, window time.Duration, limit int, d *decision) (bool, int) {
	now := l.now().UnixNano()
	width := int64(window)
	index := now / width
	elapsed := now % width
	weight := float64(width-elapsed) / float64(width)

	previous, present, err := l.counters.Get(ctx, countKey(tenantSegment, subject, index-1))
	if err != nil && cancelled(ctx) {
		// The caller's context ended, not the backend: the class is left
		// alone as well as unreported, so a decision that was cut short
		// neither writes a line nor clears a failure it never observed.
		return true, 0
	}
	d.evaluated = append(d.evaluated, reasonCounterRead)
	if err != nil {
		d.tripped = reasonCounterRead
		return true, 0
	}

	d.evaluated = append(d.evaluated, reasonCounterNotNumeric)
	var carried float64
	if present {
		parsed, parseErr := strconv.ParseFloat(string(previous), 64)
		if parseErr != nil {
			// The key holds something this module did not write. Standing
			// aside is more honest than reading it as an empty window.
			d.tripped = reasonCounterNotNumeric
			return true, 0
		}
		carried = parsed
	}

	// The expiry covers two windows, so the key written in the previous window
	// is still readable throughout this one. With a single window's expiry it
	// would lapse the moment this window opened, for any identity string that
	// made its first request at the start of a window, and the weighted term
	// would collapse to nothing.
	current, err := l.counters.IncrByFloatWithTTL(ctx, countKey(tenantSegment, subject, index), 1, 2*window)
	if err != nil && cancelled(ctx) {
		return true, 0
	}
	d.evaluated = append(d.evaluated, reasonCounterIncrement)
	if err != nil {
		d.tripped = reasonCounterIncrement
		return true, 0
	}

	estimate := current + carried*weight
	return estimate <= float64(limit), retryAfterSeconds(width - elapsed)
}

// cancelled reports whether a failed call failed because the request's own
// context ended, rather than because the backend failed.
//
// A cancellation belongs to the caller — a client disconnect, a request
// deadline — not to the counting backend. Folding it into a counter class
// would make one disconnect write a failure line and raise that class's shared
// flag, so the next real backend failure would write nothing.
//
// The request's context is asked, not the error. A backend client that bounds
// a call with a timeout of its own answers with context.DeadlineExceeded while
// the request is still live, and one that loses its connection may answer with
// context.Canceled; read off the error, either would pass for the caller's
// cancellation, and an unreachable backend would let every request through
// with no line written at all.
func cancelled(ctx context.Context) bool {
	return ctx.Err() != nil
}

// retryAfterSeconds rounds the remaining part of the window up.
//
// It is never below one: a zero would tell the caller to retry at once. It
// does not promise the retry will be let through — the previous window's count
// still weighs in, and right after a boundary that weight is close to one.
func retryAfterSeconds(remaining int64) int {
	seconds := (remaining + int64(time.Second) - 1) / int64(time.Second)
	if seconds < 1 {
		return 1
	}
	return int(seconds)
}
