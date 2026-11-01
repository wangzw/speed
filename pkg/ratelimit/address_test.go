package ratelimit

import (
	"context"
	"errors"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

// This file holds the cases for the address shield, the module's second layer.

// --- the key ---------------------------------------------------------------

// The shield keys a bucket by the client address, and an IPv6 address counts by
// its /64 prefix: a host is routinely handed a whole /64, so counting whole
// addresses would let one host walk through the addresses of its own prefix and
// never fill a bucket. An IPv4 address is counted as itself, and an IPv4-mapped
// IPv6 one reads as the IPv4 it maps. An address the standard library cannot
// parse is counted by the string it arrived as.
func TestAddressKeyCountsAnIPv6AddressByItsSlashSixtyFour(t *testing.T) {
	cases := []struct{ in, want string }{
		{"127.0.0.1", "127.0.0.1"},
		{"::ffff:127.0.0.1", "127.0.0.1"},
		{"::1", "::"},
		{"2001:db8:1:2::1", "2001:db8:1:2::"},
		{"2001:db8:1:2:ffff:0:0:9", "2001:db8:1:2::"},
		{"not-an-address", "not-an-address"},
	}
	for _, c := range cases {
		if got := addressKey(c.in); got != c.want {
			t.Errorf("addressKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Two addresses in one /64 share a bucket; two in different /64s do not.
	if addressKey("2001:db8:1:2::1") != addressKey("2001:db8:1:2:abcd::9") {
		t.Error("two addresses in one /64 got different keys, so the prefix is not what is counted")
	}
	if addressKey("2001:db8:1:2::1") == addressKey("2001:db8:1:3::1") {
		t.Error("two addresses in different /64s got one key, so hosts would share a bucket")
	}
}

// --- the charge ------------------------------------------------------------

// spend takes as many tokens as the bucket holds, and never more than it holds.
//
// This is the property the penalty leans on: rate.Limiter cannot overdraw, so a
// penalty charged as one AllowN would leave a nearly empty bucket almost
// untouched and let the source straight back in. Draining what is there empties
// the bucket instead, and the next request from the address is refused.
func TestSpendTakesWhatTheBucketHoldsAndNoMore(t *testing.T) {
	now := pinnedAt
	// A bucket that holds ten and, at a rate of zero, never refills.
	lim := rate.NewLimiter(0, 10)

	spend(lim, now, 3)
	if got := lim.TokensAt(now); got != 7 {
		t.Fatalf("tokens = %v, want 7: a penalty smaller than the bucket spends exactly itself", got)
	}

	// A penalty larger than what is left empties the bucket rather than being
	// turned away whole.
	spend(lim, now, 20)
	if got := lim.TokensAt(now); got != 0 {
		t.Fatalf("tokens = %v, want 0: an oversized penalty must drain the bucket, not be refused whole", got)
	}
	if lim.AllowN(now, 1) {
		t.Fatal("a request was admitted from a bucket the penalty should have emptied")
	}

	spend(lim, now, 0)
	if lim.AllowN(now, 1) {
		t.Fatal("a zero penalty spent something: it must be a no-op")
	}
}

// The shield charges the extra penalty for the two statuses an attacker
// produces and for no others. A 401 is a stuffed credential and a 404 is an
// enumerated tenant; a 2xx is ordinary traffic, and the other refusals are left
// at the one token every request costs because they are not this layer's target
// and charging for them would spend a client's budget on the server's faults.
func TestRefusalOutcomeChargesOnlyTheAttackerStatuses(t *testing.T) {
	for _, status := range []int{nethttp.StatusUnauthorized, nethttp.StatusNotFound} {
		if !refusalOutcome(status) {
			t.Errorf("%d is not charged, want it charged", status)
		}
	}
	for _, status := range []int{
		nethttp.StatusOK,
		nethttp.StatusCreated,
		nethttp.StatusForbidden,
		nethttp.StatusMethodNotAllowed,
		nethttp.StatusTooManyRequests,
		nethttp.StatusInternalServerError,
	} {
		if refusalOutcome(status) {
			t.Errorf("%d is charged, want it not charged", status)
		}
	}
}

// --- the step-up exception -------------------------------------------------

// smallShieldValues is the shield at a burst and penalty a case can spend in a
// few requests: four tokens and a penalty of twenty drain the bucket on the
// first refusal, so a penalty that must not be charged is visible as a request
// that still reaches the route.
func smallShieldValues() map[string]any {
	return map[string]any{
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   4,
		addressPenaltyPath: defaults.AddressPenalty,
	}
}

// A 401 that asks for a step-up is not a stuffed credential: it pays the one
// token every request costs and no penalty, so a client walking through the
// handshake never exhausts its bucket by doing the thing it was asked to do.
// With a burst of four, four marked 401s reach the route and only the fifth is
// refused; charging the penalty would shut the source out after the first.
func TestAStepUpRefusalDoesNotSpendThePenalty(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: smallShieldValues()}
	var reached counter
	h := startHost(t, hostOptions{
		limiter:   l,
		viaModule: true,
		register: func(ep speedhttp.Endpoint) {
			ep.Route(nethttp.MethodGet, "/step-up", func(c *gin.Context) {
				reached.inc()
				speedhttp.MarkStepUp(c)
				c.Status(nethttp.StatusUnauthorized)
			})
		},
	})

	for i := 0; i < 4; i++ {
		if got := h.get(t, "/step-up"); got.status != nethttp.StatusUnauthorized {
			t.Fatalf("request %d was answered %d, want the route's marked 401", i+1, got.status)
		}
	}
	if got := reached.count(); got != 4 {
		t.Fatalf("the route served %d marked 401s, want 4: a step-up must not spend the penalty", got)
	}
	requireThrottledProblem(t, h.get(t, "/step-up"))
	if got := reached.count(); got != 4 {
		t.Fatalf("the route served %d marked 401s, want the fifth request refused by the spent bucket", got)
	}
}

// The same budget, answered by a 401 that refused a credential and carries no
// step-up mark, is charged the penalty: the first refusal drains the bucket and
// the second request never reaches the route.
func TestACredentialRefusalStillSpendsThePenalty(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: smallShieldValues()}
	h := startHost(t, hostOptions{limiter: l, viaModule: true, routeStatus: nethttp.StatusUnauthorized})

	if got := h.get(t, "/things"); got.status != nethttp.StatusUnauthorized {
		t.Fatalf("the first request was answered %d, want the route's 401", got.status)
	}
	requireThrottledProblem(t, h.get(t, "/things"))
	if got := h.reached.count(); got != 1 {
		t.Fatalf("the route served %d requests, want 1: an unmarked 401 must spend the penalty", got)
	}
}

// A 404 is an enumeration signal whether or not anything marked the response,
// so it still spends the penalty: the first request reaches the entry point's
// 404, which drains the bucket, and the second is refused by the shield.
func TestANotFoundStillSpendsThePenalty(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: smallShieldValues()}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	requireProblem(t, h.get(t, "/nowhere"), nethttp.StatusNotFound)
	requireThrottledProblem(t, h.get(t, "/nowhere"))
}

// --- the layer's metadata --------------------------------------------------

// The shield is a guard layer: it runs before identity is settled. It leaves
// Order at its zero value, so it stays inside a layer that carries
// OrderOutermost there — where CORS belongs, so a 429 it writes still carries
// the CORS headers a browser needs. It carries no Provides, so nothing places
// itself against it, and the Limiter token stays the quota layer's alone.
func TestAddressMiddlewareCarriesItsStageAndNoToken(t *testing.T) {
	mw := newLimiter(nil).addressMiddleware()

	if mw.Handler == nil {
		t.Fatal("the shield has no handler")
	}
	if mw.Stage != speedhttp.StageGuard {
		t.Fatalf("Stage = %v, want the guard stage", mw.Stage)
	}
	if mw.Order != 0 {
		t.Fatalf("Order = %v, want the zero value: CORS carries OrderOutermost and has to stay outside", mw.Order)
	}
	if len(mw.Provides) != 0 || len(mw.After) != 0 || len(mw.Before) != 0 {
		t.Fatalf("the shield declares constraints (%v/%v/%v), want none: nothing places itself against it",
			mw.Provides, mw.After, mw.Before)
	}
	if mw.Name != addressLayerName {
		t.Errorf("Name = %q, want %q", mw.Name, addressLayerName)
	}
	if mw.Name == ModuleName {
		t.Errorf("the shield's name is the quota's (%q), so the chain listing cannot tell the two apart", ModuleName)
	}
}

// --- the decision ----------------------------------------------------------

// The discriminating case. With the shield on, a source repeating a failing
// credential stops producing calls to the layer behind it once its budget is
// spent: the gate spends one token and the 401 spends the penalty, so a bucket
// of eighty is empty after four refusals and the fifth request is refused
// before it reaches the route. With the shield off the same requests all reach
// the route; a request the endpoint reports as coming from another address keys
// its own, unspent bucket, so it still reaches the route while the first is shut
// out.
func TestTheShieldStopsARefusedSourceAndKeepsAddressesApart(t *testing.T) {
	on := pinned()
	on.addressEndpoints = []string{guardedEndpoint}
	on.counters = newFakeKV()
	on.store = &fakeStore{values: shieldValues()}
	serving := startHost(t, hostOptions{
		limiter:        on,
		viaModule:      true,
		routeStatus:    nethttp.StatusUnauthorized,
		trustedProxies: []string{"127.0.0.1/32"},
	})

	// Six requests from one address, each answered 401. Four of them empty the
	// bucket; the rest are refused before reaching the route.
	for i := 0; i < 6; i++ {
		serving.get(t, "/things")
	}
	if got := serving.reached.count(); got != 4 {
		t.Fatalf("the route behind the shield served %d requests, want 4 before the source was shut out", got)
	}

	// The refusal is the entry point's error shape, carrying this module's
	// throttled type, with Retry-After on it.
	refused := serving.get(t, "/things")
	requireThrottledProblem(t, refused)
	requireRetryAfter(t, refused)
	if got := serving.reached.count(); got != 4 {
		t.Fatalf("the route served %d requests, want the count to stop at 4 while the source is refused", got)
	}

	// A request the endpoint reports as coming from another address keys its
	// own, unspent bucket: it reaches the route while the first address is shut
	// out. A layer that keyed both by one bucket would refuse it too.
	before := serving.reached.count()
	serving.send(t, nethttp.MethodGet, "/things", map[string]string{"X-Forwarded-For": "10.0.0.9"})
	if got := serving.reached.count() - before; got != 1 {
		t.Fatalf("a second address reached the route %d times, want 1: its budget is not the first address's", got)
	}

	// With the shield off, the same six requests all reach the route.
	off := pinned()
	off.counters = newFakeKV()
	off.store = &fakeStore{values: shieldValues()}
	open := startHost(t, hostOptions{
		limiter:     off,
		viaModule:   true,
		routeStatus: nethttp.StatusUnauthorized,
	})
	for i := 0; i < 6; i++ {
		open.get(t, "/things")
	}
	if got := open.reached.count(); got != 6 {
		t.Fatalf("with the shield off the route served %d requests, want all 6", got)
	}
}

// A success pays the one token the gate takes and nothing more. The bucket
// holds the burst, so exactly that many successful requests pass and the next
// is refused: a layer that charged the refusal penalty on every request as well
// would stop after four.
func TestTheShieldChargesOneTokenForASuccess(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: shieldValues()}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	for i := 0; i < defaults.AddressBurst; i++ {
		if got := h.get(t, "/things"); got.status != nethttp.StatusNoContent {
			t.Fatalf("request %d was answered %d, want the route's 204 within the burst", i+1, got.status)
		}
	}
	requireThrottledProblem(t, h.get(t, "/things"))
	if got := h.reached.count(); got != defaults.AddressBurst {
		t.Fatalf("the route served %d requests, want %d: a success costs one token, not the penalty",
			got, defaults.AddressBurst)
	}
}

// The shield is registered by the module's own descriptor, on the endpoints its
// list names, beside the quota the endpoints list names. The two are distinct
// layers on the chain: the shield in the guard stage, the quota in the
// throttling stage, and the shield outside the quota because the guard stage
// runs first.
func TestTheShieldAndTheQuotaAreDistinctLayers(t *testing.T) {
	l := pinned("public")
	l.addressEndpoints = []string{"public"}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{
		windowPath:         time.Minute,
		limitPath:          10,
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   defaults.AddressBurst,
		addressPenaltyPath: defaults.AddressPenalty,
	}}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	shield := strings.Index(h.chain, "[guard] "+addressLayerName)
	quota := strings.Index(h.chain, "[throttle] "+ModuleName)
	if shield < 0 {
		t.Fatalf("the chain %q lists no shield layer", h.chain)
	}
	if quota < 0 {
		t.Fatalf("the chain %q lists no quota layer", h.chain)
	}
	if shield >= quota {
		t.Fatalf("the chain is %q, want the shield in the guard stage ahead of the quota in the throttling stage", h.chain)
	}
}

// Every failing path lets the request through, the shield included: a settings
// read that fails stands it aside rather than refusing the whole endpoint on one
// bad row, and the failed read is reported.
func TestTheShieldLetsRequestsThroughWhenItsThresholdsAreUnreadable(t *testing.T) {
	var out lockedBuffer
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{err: settings.ErrUnknownKey}
	l.signal = newSignalTo(&out)
	h := startHost(t, hostOptions{
		limiter:     l,
		viaModule:   true,
		routeStatus: nethttp.StatusUnauthorized,
	})

	for i := 0; i < 3; i++ {
		if got := h.get(t, "/things"); got.status != nethttp.StatusUnauthorized {
			t.Fatalf("request %d was answered %d, want the route's 401: the shield must stand aside", i+1, got.status)
		}
	}
	if got := h.reached.count(); got != 3 {
		t.Fatalf("the route served %d requests, want 3: the shield must not refuse when it cannot read its settings", got)
	}
	if !containsLine(out.String(), "letting requests through", string(reasonShieldRead)) {
		t.Fatalf("the shield did not report the failed read:\n%s", out.String())
	}
}

// A threshold outside its domain is a value this layer does not act on, so the
// shield stands aside on it exactly as the quota does on a bad window or limit.
func TestTheShieldStandsAsideOnAThresholdOutsideItsDomain(t *testing.T) {
	cases := map[string]map[string]any{
		"zero rate":        {addressRatePath: 0, addressBurstPath: 10, addressPenaltyPath: 1},
		"negative rate":    {addressRatePath: -1, addressBurstPath: 10, addressPenaltyPath: 1},
		"zero burst":       {addressRatePath: 1, addressBurstPath: 0, addressPenaltyPath: 1},
		"negative penalty": {addressRatePath: 1, addressBurstPath: 10, addressPenaltyPath: -1},
	}
	for name, values := range cases {
		l := newLimiter(nil)
		l.store = &fakeStore{values: values}
		var d decision
		l.shieldThresholds(&d)
		if d.tripped != reasonShieldDomain {
			t.Errorf("%s: tripped %q, want %q", name, d.tripped, reasonShieldDomain)
		}
	}
}

// --- the defaults ----------------------------------------------------------

// The declared defaults are the numbers the shield is decided on: four tokens a
// second, a burst of eighty, and a refusal penalty of twenty. A smaller burst is
// emptied by one ordinary client's renewal and replay, and a client locked out
// of an authentication endpoint by its own routine traffic is what the larger
// burst prevents.
func TestTheShieldDefaultsAreTheDecidedNumbers(t *testing.T) {
	if defaults.AddressRate != 4 || defaults.AddressBurst != 80 || defaults.AddressPenalty != 20 {
		t.Fatalf("the shield defaults are rate %d, burst %d, penalty %d; want 4, 80, 20",
			defaults.AddressRate, defaults.AddressBurst, defaults.AddressPenalty)
	}
}

// --- the settings domains --------------------------------------------------

// The shield's three items are refused outside their domains when they are
// written, as the quota's are, and the layer stands aside on exactly the same
// values on the read side. The write-time refusal and the read-time judgement
// are held to one table here, so a domain declared narrower or wider than the
// one the layer acts on fails either way.
func TestAnAddressThresholdOutsideItsDomainIsRefusedWhereItIsWritten(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		layer := settings.Global()
		values := []struct {
			name  string
			key   string
			value int
			in    bool
		}{
			{"a rate of one", addressRatePath, 1, true},
			{"the default rate", addressRatePath, defaults.AddressRate, true},
			{"a rate of zero", addressRatePath, 0, false},
			{"a negative rate", addressRatePath, -1, false},
			{"a burst of one", addressBurstPath, 1, true},
			{"the default burst", addressBurstPath, defaults.AddressBurst, true},
			{"a burst of zero", addressBurstPath, 0, false},
			{"a penalty of zero", addressPenaltyPath, 0, true},
			{"the default penalty", addressPenaltyPath, defaults.AddressPenalty, true},
			{"a negative penalty", addressPenaltyPath, -1, false},
		}
		for _, v := range values {
			err := store.Set(ctx, settings.Write{Layer: layer, Key: v.key, Value: v.value, Actor: "test"})
			switch {
			case v.in && err != nil:
				t.Errorf("%s: writing %d to %s failed: %v, want it stored", v.name, v.value, v.key, err)
			case !v.in && !errors.Is(err, settings.ErrInvalidValue):
				t.Errorf("%s: writing %d to %s answered %v, want ErrInvalidValue", v.name, v.value, v.key, err)
			case !v.in && !strings.Contains(err.Error(), v.key):
				t.Errorf("%s: the refusal %q does not name the key %s", v.name, err, v.key)
			}

			if tripped := shieldStandsAsideOn(v.key, v.value); tripped == v.in {
				t.Errorf("%s: the shield stands aside on %d: %v, while the store takes it: %v",
					v.name, v.value, tripped, v.in)
			}
		}
	})
}

// shieldStandsAsideOn reports whether shieldThresholds stands aside on a stored
// row holding the value, the read-time judgement of the same domain. The other
// two items read as their declared defaults, which lie inside their domains.
func shieldStandsAsideOn(key string, value int) bool {
	values := map[string]any{
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   defaults.AddressBurst,
		addressPenaltyPath: defaults.AddressPenalty,
	}
	values[key] = value
	l := newLimiter(nil)
	l.store = &fakeStore{values: values}
	var d decision
	l.shieldThresholds(&d)
	return d.tripped == reasonShieldDomain
}

// --- the configuration -----------------------------------------------------

// The shield's endpoints are read from their own item, and a name repeated in
// that list is refused as one is in the quota's: two registrations are two
// layers, and two shield layers on one endpoint halve the effective limit.
func TestPrepareRefusesARepeatedAddressEndpoint(t *testing.T) {
	reg := registryReading(t, fakeReader{
		endpoints:        []string{"public"},
		addressEndpoints: []string{"public", "public"},
	})
	_, err := Module().Prepare(context.Background(), reg)
	if !errors.Is(err, ErrDuplicateEndpoint) {
		t.Fatalf("err = %v, want ErrDuplicateEndpoint", err)
	}
	if !strings.Contains(err.Error(), addressEndpointsKey) {
		t.Fatalf("the refusal %q does not name the item %s", err, addressEndpointsKey)
	}
}

// The two endpoint lists are separate, and both are read from a real document
// through the real loader: this holds the item key the schema declares and the
// field's tag to each other, so a tag that named another key would leave the
// field empty and be caught here rather than at a host's first run.
func TestReadConfigDecodesBothEndpointLists(t *testing.T) {
	reader := loadDocument(t, []byte(
		`{"ratelimit": {"endpoints": ["public"], "address-endpoints": ["public", "auth"]}}`))
	cfg, err := readConfig(reader)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if !slices.Equal(cfg.Endpoints, []string{"public"}) {
		t.Errorf("Endpoints = %v, want [public]", cfg.Endpoints)
	}
	if !slices.Equal(cfg.AddressEndpoints, []string{"public", "auth"}) {
		t.Errorf("AddressEndpoints = %v, want [public auth]", cfg.AddressEndpoints)
	}
}

// shieldValues is the shield's three settings items at their declared defaults,
// for a fakeStore a case drives the decision with.
func shieldValues() map[string]any {
	return map[string]any{
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   defaults.AddressBurst,
		addressPenaltyPath: defaults.AddressPenalty,
	}
}
