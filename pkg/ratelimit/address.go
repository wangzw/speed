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

// This file is the module's second layer, the address shield, and it is not the
// layer limit.go carries.
//
// The layer in limit.go is the module's quota. It runs in the throttling stage,
// charges a settled identity string inside a tenant, and answers 429 once that
// identity has spent the window. The shield here is a different thing: it runs
// in the guard stage, before any identity is settled, when the only key a
// request carries is the address it came from. It counts that address in this
// process's memory and spends by outcome, so a source whose requests are refused
// is shut out before it reaches the authentication backend.
//
// A quota is fair to a known identity and belongs after identity is known; the
// shield is a coarse defence against a source that has not identified itself at
// all, and belongs before. The two share the module and the chain listing and
// nothing else: a deployment reads the quota's window and limit and the shield's
// rate, burst and penalty as separate settings, and names the endpoints each
// runs on in separate lists.

const (
	// addressLayerName is the shield's name in the chain listing. The quota
	// layer carries the bare module name, so the shield needs its own token to
	// be told apart from it there.
	addressLayerName = ModuleName + ".address"

	// addressEndpointsKey is the input item naming the endpoints the shield
	// guards. It is separate from endpoints on purpose: the two layers are
	// configured and switched apart, so a host that wants the quota on one
	// endpoint and the shield on another, or the shield on none, says so here,
	// and an endpoint named in neither list runs neither layer.
	addressEndpointsKey = "address-endpoints"

	// The shield's three settings items, and the full paths they are read
	// under. As for the quota's items, a read names the declared full path and
	// not the bare key, or it covers no declaration and stands aside on every
	// request. A key segment holds lowercase letters, digits and hyphens, so
	// the words are joined with hyphens and not underscores.
	addressRateKey    = "address-rate"
	addressBurstKey   = "address-burst"
	addressPenaltyKey = "address-penalty"

	addressRatePath    = ConfigNamespace + "." + addressRateKey
	addressBurstPath   = ConfigNamespace + "." + addressBurstKey
	addressPenaltyPath = ConfigNamespace + "." + addressPenaltyKey
)

// addressRateInDomain, addressBurstInDomain and addressPenaltyInDomain are the
// shield's three domains: the values it acts on.
//
// Each is the one statement of its domain, judged in the same two places the
// quota's are: the settings declaration carries it, so a write outside it is
// refused before the row is written, and shieldThresholds applies it to what a
// decision reads, since a row stored before the domain was declared, or written
// around the store, is served as it is and the layer stands aside on it rather
// than act on a value it cannot use.
func addressRateInDomain(perSecond int) bool { return perSecond > 0 }

func addressBurstInDomain(size int) bool { return size > 0 }

// A penalty of zero is in domain: it turns the outcome charge off and leaves the
// shield a plain per-address rate cap, which is a configuration an operator may
// want. A negative penalty refunds tokens for a refusal and would reward exactly
// the traffic the shield exists to stop.
func addressPenaltyInDomain(points int) bool { return points >= 0 }

const (
	// addressIdle is how long a bucket the shield has not seen stays in memory
	// before a sweep may drop it. It is far longer than any refill takes, so a
	// bucket dropped after this long is one an address has stopped using, not
	// one whose budget was still being spent down.
	addressIdle = 10 * time.Minute

	// addressCap is the most buckets the shield holds at once. Past it a sweep
	// drops entries whatever their age, so a flood of distinct addresses — which
	// this layer does not stop, and does not claim to — at least cannot grow the
	// map without bound. The cost of the cap is a bucket dropped before it aged
	// out, which only resets one address's budget.
	addressCap = 1 << 16
)

// shieldLimits are the numbers one shield decision acts on, read from settings.
type shieldLimits struct {
	rate    int // tokens added per second
	burst   int // the bucket's capacity, and the most a spent address holds
	penalty int // tokens a refusal costs on top of the one every request costs
}

// addressGuard holds one token bucket per client address, and holds them only
// here, in this process's memory.
//
// It makes no network call of any kind. Counting a shared counter over the
// network on every request would make this layer the amplification point it
// exists to close, so the counter is in-process and each replica counts alone.
// The price is stated in the module's documentation: the effective limit is the
// per-replica limit times the number of replicas, and a flood spread over many
// addresses is not stopped by this layer at all.
type addressGuard struct {
	mu      sync.Mutex
	buckets map[string]*addressBucket
}

// addressBucket is one address's bucket, with the rate and burst it was built
// to so a settings change can be noticed without rebuilding the map.
type addressBucket struct {
	lim   *rate.Limiter
	rate  int
	burst int
	seen  time.Time
}

func newAddressGuard() *addressGuard {
	return &addressGuard{buckets: make(map[string]*addressBucket)}
}

// bucket returns the token bucket for one address, building it on first use.
//
// The settings are read on every request, so a rate or burst changed at runtime
// takes effect on the next one: a bucket built to other numbers has them set on
// it in place, which keeps the tokens the address has already spent against the
// old bucket rather than resetting it to full.
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

// evict bounds the map's size. It runs on the call that inserts a key, so a
// map below its cap does no sweeping at all.
//
// Buckets idle past addressIdle are dropped first; if the map is still at its
// cap, entries are dropped whatever their age. An address that has been silent
// for addressIdle has refilled to burst many times over, so a dropped bucket is
// indistinguishable from a kept one for any address still using the endpoint.
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

// addressMiddleware is the shield, as it is registered on an endpoint.
//
// It sits in the guard stage and carries no Position, so it stays inside a
// layer that declares PositionFirst there — which is where CORS belongs, so
// that a 429 this layer writes still carries the CORS headers a browser needs
// to read it. It carries no Provides: nothing places itself relative to the
// shield, and the Limiter token belongs to the quota layer and to that layer
// alone.
func (l *limiter) addressMiddleware() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:    addressLayerName,
		Stage:   speedhttp.StageGuard,
		Handler: l.handleAddress,
	}
}

// handleAddress is the shield's handler. It runs one request through the shield
// and accounts for the decision, as the quota's handler does.
func (l *limiter) handleAddress(c *gin.Context) {
	l.signal.settle(l.shield(c))
}

// shield runs one request through the address limiter: it charges the request
// against its address's bucket and refuses it with a 429 when the bucket is
// spent, then, once the request has been answered, charges a surcharge for the
// outcomes an attacker produces.
//
// The address is read before c.Next, and the key is fixed there. An inner layer
// that rebinds the request — the telemetry layer replaces it to attach a trace —
// must not change the key this layer counted under.
//
// Every step that cannot be taken lets the request through, as in the quota
// layer: a settings read that fails or a row outside its domain stands the
// shield aside rather than refusing the whole endpoint on one bad row.
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
		speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusTooManyRequests})
		return d
	}

	c.Next()

	// Charge by outcome. The request has been answered, so its status is known,
	// and a refusal it produced spends more of the source's budget than a
	// success did: ordinary traffic pays the one token the gate took, and a
	// source that gets refusals is drained fast enough that it stops reaching
	// the backend behind this layer.
	if refusalOutcome(c.Writer.Status()) {
		spend(lim, now, limits.penalty)
	}
	return d
}

// shieldThresholds reads the shield's three settings items, checks them against
// their domains, and records in d the classes it evaluates.
//
// The three reads go to the settings module's memory snapshot, one key at a
// time, so they can come from different snapshots and a decision may pair a rate
// with a burst that were never in force together. That is the same reading the
// quota layer does, and it is acceptable for the same reason: the numbers are a
// coarse defence and a momentary mismatch only moves one threshold.
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
		// A rate or burst of zero leaves a bucket that refills not at all or
		// holds nothing, refusing every request from every address; a negative
		// penalty refunds on a refusal. None is a value this layer acts on.
		d.tripped = reasonShieldDomain
		return shieldLimits{}
	}
	return limits
}

// refusalOutcome reports whether a status is one the shield charges the extra
// penalty for.
//
// 401 and 404 above all, and only those: a source repeating a 401 is stuffing
// credentials and one repeating a 404 is enumerating tenants, and both are
// exactly the traffic the shield is aimed at stopping before it reaches the
// authentication backend. A 2xx is ordinary traffic and pays only the one token
// every request costs — little, so a normal client barely notices the shield.
// Other refusals — a 403, a 413, a 429 from another layer, a 500 — are left at
// that same one token: they are not the signature of the attacks named here,
// and charging for them would spend a client's budget on the server's own
// faults.
func refusalOutcome(status int) bool {
	return status == nethttp.StatusUnauthorized || status == nethttp.StatusNotFound
}

// spend charges tokens against lim, taking as many as the bucket can pay.
//
// rate.Limiter offers no way to overdraw a bucket: AllowN with an n the bucket
// cannot afford consumes nothing and reports false. A penalty charged as a
// single AllowN would therefore leave a nearly empty bucket almost untouched and
// let the source straight back in. Reading the tokens available first, and
// charging only what is there, empties the bucket instead, which is the whole
// point of the penalty. The final AllowN is not a decision and its result is
// ignored: the request has already been answered.
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

// retryAfterFor is the whole number of seconds until the bucket can admit the
// next request, never below one.
//
// It is read off a reservation the bucket is not asked to keep: ReserveN reports
// how long the next token takes to arrive without spending one, and the
// reservation is cancelled when it happens to have been granted, so probing for
// the hint never itself consumes a token.
func retryAfterFor(lim *rate.Limiter, now time.Time) int {
	r := lim.ReserveN(now, 1)
	wait := r.Delay()
	if r.OK() {
		r.Cancel()
	}
	return retryAfterSeconds(int64(wait))
}

// addressKey builds the bucket key for one client address.
//
// The key is the address the entry point reports: the peer address, unless the
// endpoint was configured to trust proxies, in which case it is the address a
// trusted proxy forwarded. An IPv6 address is counted by its /64 prefix, because
// a host is routinely handed a whole /64 or larger and counting whole addresses
// would let one host walk through the addresses of its own prefix and never fill
// a bucket; an IPv4 address is counted as itself, one host having few to walk
// through. An address the standard library cannot parse is counted by the string
// it arrived as, which is the entry point's own answer and not something this
// layer should second-guess.
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
