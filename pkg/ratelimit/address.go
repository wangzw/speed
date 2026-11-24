package ratelimit

import (
	"net"
	nethttp "net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

const (
	addressLayerName = ModuleName + ".address"

	addressEndpointsKey = "address-endpoints"

	addressRateKey    = "address-rate"
	addressBurstKey   = "address-burst"
	addressPenaltyKey = "address-penalty"

	addressRatePath    = ConfigNamespace + "." + addressRateKey
	addressBurstPath   = ConfigNamespace + "." + addressBurstKey
	addressPenaltyPath = ConfigNamespace + "." + addressPenaltyKey
)

func addressRateInDomain(perSecond int) bool { return perSecond > 0 }

func addressBurstInDomain(size int) bool { return size > 0 }

func addressPenaltyInDomain(points int) bool { return points >= 0 }

const (
	addressIdle = 10 * time.Minute
	addressCap  = 1 << 16
)

type shieldLimits struct {
	rate    int
	burst   int
	penalty int
}

// addressGuard holds one token bucket per client address in this process's
// memory and makes no network call of any kind.
type addressGuard struct {
	mu      sync.Mutex
	buckets map[string]*addressBucket
}

type addressBucket struct {
	lim   *rate.Limiter
	rate  int
	burst int
	seen  time.Time
}

func newAddressGuard() *addressGuard {
	return &addressGuard{buckets: make(map[string]*addressBucket)}
}

func (g *addressGuard) bucket(key string, limits shieldLimits, now time.Time) *rate.Limiter {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, ok := g.buckets[key]
	if !ok {
		b = &addressBucket{lim: rate.NewLimiter(rate.Limit(limits.rate), limits.burst)}
		g.buckets[key] = b
		g.evict(now)
	}
	if b.rate != limits.rate || b.burst != limits.burst {
		b.lim.SetLimit(rate.Limit(limits.rate))
		b.lim.SetBurst(limits.burst)
		b.rate = limits.rate
		b.burst = limits.burst
	}
	b.seen = now
	return b.lim
}

// evict bounds the map's size: buckets idle past addressIdle drop first, then
// any entries once the map is at its cap.
func (g *addressGuard) evict(now time.Time) {
	if len(g.buckets) < addressCap {
		return
	}
	for key, b := range g.buckets {
		if now.Sub(b.seen) > addressIdle {
			delete(g.buckets, key)
		}
	}
	for len(g.buckets) >= addressCap {
		for key := range g.buckets {
			delete(g.buckets, key)
			break
		}
	}
}

// addressMiddleware is the shield; it sits in the guard stage and leaves Order at
// zero so it stays inside a layer at OrderOutermost, which CORS needs.
func (l *limiter) addressMiddleware() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:    addressLayerName,
		Stage:   speedhttp.StageGuard,
		Handler: l.handleAddress,
	}
}

func (l *limiter) handleAddress(c *gin.Context) {
	l.signal.settle(l.shield(c))
}

func (l *limiter) shield(c *gin.Context) decision {
	var d decision

	key := addressKey(c.ClientIP())

	limits := l.shieldThresholds(&d)
	if d.tripped != "" {
		c.Next()
		return d
	}

	now := l.now()
	lim := l.guard.bucket(key, limits, now)
	if !lim.AllowN(now, 1) {
		c.Header("Retry-After", strconv.Itoa(retryAfterFor(lim, now)))
		speedhttp.Fail(c, speedhttp.Problem{
			Status: nethttp.StatusTooManyRequests,
			Type:   typePrefix + "throttled",
		})
		return d
	}

	c.Next()

	status := c.Writer.Status()
	stepUp := status == nethttp.StatusUnauthorized && speedhttp.IsStepUp(c)
	if refusalOutcome(status) && !stepUp {
		spend(lim, now, limits.penalty)
	}
	return d
}

func (l *limiter) shieldThresholds(d *decision) shieldLimits {
	d.evaluated = append(d.evaluated, reasonShieldRead)

	var limits shieldLimits
	if err := l.store.Get(settings.Global(), addressRatePath, &limits.rate); err != nil {
		d.tripped = reasonShieldRead
		return shieldLimits{}
	}
	if err := l.store.Get(settings.Global(), addressBurstPath, &limits.burst); err != nil {
		d.tripped = reasonShieldRead
		return shieldLimits{}
	}
	if err := l.store.Get(settings.Global(), addressPenaltyPath, &limits.penalty); err != nil {
		d.tripped = reasonShieldRead
		return shieldLimits{}
	}

	d.evaluated = append(d.evaluated, reasonShieldDomain)
	if !addressRateInDomain(limits.rate) || !addressBurstInDomain(limits.burst) ||
		!addressPenaltyInDomain(limits.penalty) {
		d.tripped = reasonShieldDomain
		return shieldLimits{}
	}
	return limits
}

// refusalOutcome reports whether the shield charges its extra penalty: a 404 and
// a 401 that refused a credential, not a step-up 401.
func refusalOutcome(status int) bool {
	return status == nethttp.StatusUnauthorized || status == nethttp.StatusNotFound
}

// spend charges tokens against lim, taking only what the bucket holds: AllowN
// cannot overdraw, so charging the full penalty on a nearly empty bucket would
// leave it almost untouched.
func spend(lim *rate.Limiter, now time.Time, tokens int) {
	if tokens <= 0 {
		return
	}
	if available := lim.TokensAt(now); float64(tokens) > available {
		tokens = int(available)
	}
	if tokens <= 0 {
		return
	}
	lim.AllowN(now, tokens)
}

// retryAfterFor is the whole seconds until the next request, never below one; it
// reads a reservation it cancels, so probing consumes no token.
func retryAfterFor(lim *rate.Limiter, now time.Time) int {
	r := lim.ReserveN(now, 1)
	wait := r.Delay()
	if r.OK() {
		r.Cancel()
	}
	return retryAfterSeconds(int64(wait))
}

// addressKey counts an IPv6 address by its /64 prefix and an IPv4 address as
// itself; an unparseable value is counted as the string it arrived as.
func addressKey(clientIP string) string {
	parsed := net.ParseIP(clientIP)
	if parsed == nil {
		return clientIP
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}
