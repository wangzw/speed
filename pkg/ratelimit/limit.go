package ratelimit

import (
	"context"
	"fmt"
	"math"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

type limiter struct {
	endpoints        []string
	addressEndpoints []string
	store            settings.Store
	counters         kv.KV
	signal           *signal
	guard            *addressGuard

	now func() time.Time
}

func newLimiter(endpoints []string) *limiter {
	return &limiter{
		endpoints: append([]string(nil), endpoints...),
		signal:    newSignal(),
		guard:     newAddressGuard(),
		now:       time.Now,
	}
}

func (*limiter) limit() {}

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
	for _, name := range l.addressEndpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return takeUp("endpoint "+strconv.Quote(name), err)
		}
		endpoint.Use(l.addressMiddleware())
	}
	return nil
}

func takeUp(what string, err error) error {
	return fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
}

// middleware is the quota layer. It sits in the throttling stage, after identity
// is settled and before recording, and Provides carries the Limiter token.
func (l *limiter) middleware() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*Limiter)(nil)},
		Stage:    speedhttp.StageThrottle,
		Handler:  l.handle,
	}
}

func (l *limiter) handle(c *gin.Context) {
	identity, identified := authn.FromContext(c.Request.Context())
	l.signal.settle(l.decide(c, identity, identified))
}

type decision struct {
	tripped reason

	evaluated []reason

	tenant string
}

// decide runs one request through the count: it hands the request on with
// c.Next or refuses it with a 429 and Retry-After, and every failing path lets
// the request through.
func (l *limiter) decide(c *gin.Context, identity authn.Identity, identified bool) decision {
	if !identified {
		c.Next()
		return decision{}
	}

	ctx := c.Request.Context()
	layer, tenantSegment := l.scopeOf(ctx)
	d := decision{tenant: tenantSegment}
	window, limit := l.thresholds(layer, &d)
	if d.tripped != "" {
		c.Next()
		return d
	}

	allowed, retryAfter := l.count(ctx, identity.Key(), tenantSegment, window, limit, &d)
	if d.tripped != "" {
		c.Next()
		return d
	}
	if !allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		speedhttp.Fail(c, speedhttp.Problem{
			Status: nethttp.StatusTooManyRequests,
			Type:   typePrefix + "throttled",
		})
		return d
	}
	c.Next()
	return d
}

func (l *limiter) scopeOf(ctx context.Context) (settings.Layer, string) {
	if id, ok := tenant.FromContext(ctx); ok {
		return settings.ForTenant(string(id)), string(id)
	}
	return settings.Global(), ""
}

// maxWindow is the longest window whose double still fits a time.Duration; a
// larger one writes a counting key with no expiry.
const maxWindow = time.Duration(math.MaxInt64) / 2

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
		d.tripped = reasonThresholdDomain
		return 0, 0
	}
	return window, limit
}

// count decides against the current window's count plus the previous window's
// weighted by the part of this window that has not elapsed; the increment
// happens before the decision, so a refused request still consumes.
func (l *limiter) count(ctx context.Context, subject, tenantSegment string, window time.Duration, limit int, d *decision) (bool, int) {
	now := l.now().UnixNano()
	width := int64(window)
	index := now / width
	elapsed := now % width
	weight := float64(width-elapsed) / float64(width)

	previous, present, err := l.counters.Get(ctx, countKey(tenantSegment, subject, index-1))
	if err != nil && cancelled(ctx) {
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
			d.tripped = reasonCounterNotNumeric
			return true, 0
		}
		carried = parsed
	}

	// The expiry covers two windows, so the key written in the previous window
	// is still readable throughout this one.
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
// context ended, read off the context and not the error.
func cancelled(ctx context.Context) bool {
	return ctx.Err() != nil
}

func retryAfterSeconds(remaining int64) int {
	seconds := (remaining + int64(time.Second) - 1) / int64(time.Second)
	if seconds < 1 {
		return 1
	}
	return int(seconds)
}
