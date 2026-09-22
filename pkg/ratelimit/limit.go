package ratelimit

import (
	"context"
	"fmt"
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
	return fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
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

// decide runs one request through the count and answers it.
//
// The identity arrives as an argument rather than being taken out of the
// context in here: Go keeps the package that writes an identity to the
// authentication capability directory, so reading one is the single step of
// this layer a test in this package cannot drive. Everything below it can.
//
// The returned reason is the class of failure that made this layer stand
// aside, or the empty reason when nothing failed; the caller accounts for it.
// Every failing path lets the request through: a refusal would turn one bad
// backend or one bad row into a refusal of the whole endpoint, and rate
// limiting here is an abuse surface rather than a data boundary.
func (l *limiter) decide(w nethttp.ResponseWriter, r *nethttp.Request, next nethttp.Handler, identity authn.Identity, identified bool) reason {
	if !identified {
		// No identity means nothing to count against. An exempt route, a host
		// that took no authentication module into the command, and a layer
		// whose After edge did not land all arrive here.
		next.ServeHTTP(w, r)
		return ""
	}

	layer, tenantSegment := l.scopeOf(r.Context())
	window, limit, tripped := l.thresholds(layer)
	if tripped != "" {
		next.ServeHTTP(w, r)
		return tripped
	}

	allowed, retryAfter, tripped := l.count(r.Context(), identity.Key(), tenantSegment, window, limit)
	if tripped != "" {
		next.ServeHTTP(w, r)
		return tripped
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.WriteHeader(nethttp.StatusTooManyRequests)
		return ""
	}
	next.ServeHTTP(w, r)
	return ""
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

// thresholds reads the two settings items and checks them against their
// domains.
//
// Both reads go to the settings module's memory snapshot, one key at a time:
// the two values can therefore come from different snapshots, and a decision
// may pair a window with a limit that were never in force together.
func (l *limiter) thresholds(layer settings.Layer) (time.Duration, int, reason) {
	var window time.Duration
	if err := l.store.Get(layer, windowKey, &window); err != nil {
		return 0, 0, reasonThresholdRead
	}
	var limit int
	if err := l.store.Get(layer, limitKey, &limit); err != nil {
		return 0, 0, reasonThresholdRead
	}
	if window <= 0 || limit <= 0 {
		// A limit of zero would refuse every request that carries an identity
		// string, since the increment happens before the decision and the
		// current count is at least one; a window of zero leaves the window
		// index undefined. Neither is a value this module acts on.
		return 0, 0, reasonThresholdDomain
	}
	return window, limit, ""
}

// count decides one request against the estimate for its current window.
//
// The estimate is the current window's count plus the previous window's count
// weighted by the part of this window that has not elapsed. The increment
// happens before the decision, so a refused request still consumes: counting
// only what was let through would let a caller that is already over its limit
// retry without spending anything, and the second half of the window would
// open up as the weight falls.
func (l *limiter) count(ctx context.Context, subject, tenantSegment string, window time.Duration, limit int) (bool, int, reason) {
	now := l.now().UnixNano()
	width := int64(window)
	index := now / width
	elapsed := now % width
	weight := float64(width-elapsed) / float64(width)

	previous, present, err := l.counters.Get(ctx, countKey(tenantSegment, subject, index-1))
	if err != nil {
		return true, 0, reasonCounterRead
	}
	var carried float64
	if present {
		parsed, parseErr := strconv.ParseFloat(string(previous), 64)
		if parseErr != nil {
			// The key holds something this module did not write. Standing
			// aside is more honest than reading it as an empty window.
			return true, 0, reasonCounterNotNumeric
		}
		carried = parsed
	}

	// The expiry covers two windows, so the key written in the previous window
	// is still readable throughout this one. With a single window's expiry it
	// would lapse the moment this window opened, for any identity string that
	// made its first request at the start of a window, and the weighted term
	// would collapse to nothing.
	current, err := l.counters.IncrByFloatWithTTL(ctx, countKey(tenantSegment, subject, index), 1, 2*window)
	if err != nil {
		return true, 0, reasonCounterIncrement
	}

	estimate := current + carried*weight
	return estimate <= float64(limit), retryAfterSeconds(width - elapsed), ""
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
