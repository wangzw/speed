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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/guard"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

// TestAddressKeyCountsAnIPv6AddressByItsSlashSixtyFour checks that addressKey
// counts an IPv6 address by its /64 prefix and an IPv4 address as itself.
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

	if addressKey("2001:db8:1:2::1") != addressKey("2001:db8:1:2:abcd::9") {
		t.Error("two addresses in one /64 got different keys, so the prefix is not what is counted")
	}
	if addressKey("2001:db8:1:2::1") == addressKey("2001:db8:1:3::1") {
		t.Error("two addresses in different /64s got one key, so hosts would share a bucket")
	}
}

// TestSpendTakesWhatTheBucketHoldsAndNoMore checks that spend drains a bucket
// rather than being turned away whole when the penalty exceeds what it holds.
func TestSpendTakesWhatTheBucketHoldsAndNoMore(t *testing.T) {
	now := pinnedAt
	lim := rate.NewLimiter(0, 10)

	spend(lim, now, 3)
	if got := lim.TokensAt(now); got != 7 {
		t.Fatalf("tokens = %v, want 7: a penalty smaller than the bucket spends exactly itself", got)
	}

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

// TestRefusalOutcomeChargesOnlyTheAttackerStatuses checks that only 401 and 404
// are charged the penalty.
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

func smallShieldValues() map[string]any {
	return map[string]any{
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   4,
		addressPenaltyPath: defaults.AddressPenalty,
	}
}

// TestAStepUpRefusalDoesNotSpendThePenalty checks that a marked step-up 401 pays
// no penalty and four of them reach the route.
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

// TestACredentialRefusalStillSpendsThePenalty checks that an unmarked 401 spends
// the penalty and shuts the source out.
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

// TestANotFoundStillSpendsThePenalty checks that a 404 spends the penalty.
func TestANotFoundStillSpendsThePenalty(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: smallShieldValues()}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	requireProblem(t, h.get(t, "/nowhere"), nethttp.StatusNotFound)
	requireThrottledProblem(t, h.get(t, "/nowhere"))
}

// TestTheGuardDistinguishesItsAnonymousRefusalFromTheStepUp checks that the real
// guard's untyped 401 is charged and its typed step-up 401 is exempt.
func TestTheGuardDistinguishesItsAnonymousRefusalFromTheStepUp(t *testing.T) {
	l := pinned()
	l.addressEndpoints = []string{guardedEndpoint}
	l.counters = newFakeKV()
	l.store = &fakeStore{values: smallShieldValues()}
	below := authntest.Context(t, context.Background())
	req := authn.Requirement{Assurance: authn.AssuranceMulti}
	h := startHost(t, hostOptions{
		limiter:        l,
		viaModule:      true,
		trustedProxies: []string{"127.0.0.1/32"},
		register: func(ep speedhttp.Endpoint) {
			ep.Route(nethttp.MethodGet, "/guard-anonymous", guard.Require(req))
			ep.Route(nethttp.MethodGet, "/guard-below",
				func(c *gin.Context) { c.Request = c.Request.WithContext(below) },
				guard.Require(req))
		},
	})
	from := func(address string) map[string]string {
		return map[string]string{"X-Forwarded-For": address}
	}
	anonymous := from("10.0.0.7")
	anonymous[anonymousHeader] = "1"
	stepUp := from("10.0.0.8")

	requireProblem(t, h.send(t, nethttp.MethodGet, "/guard-anonymous", anonymous), nethttp.StatusUnauthorized)
	requireThrottledProblem(t, h.send(t, nethttp.MethodGet, "/guard-anonymous", anonymous))

	for i := 0; i < 4; i++ {
		got := h.send(t, nethttp.MethodGet, "/guard-below", stepUp)
		if got.status != nethttp.StatusUnauthorized {
			t.Fatalf("a step-up refusal answered %d, want 401 (body %q)", got.status, got.body)
		}
	}
	requireThrottledProblem(t, h.send(t, nethttp.MethodGet, "/guard-below", stepUp))
}

// TestAddressMiddlewareCarriesItsStageAndNoToken checks the shield's stage, zero
// order, absent constraints and distinct name.
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

// TestTheShieldStopsARefusedSourceAndKeepsAddressesApart checks that a refused
// source is drained while another address keeps its own bucket.
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

	for i := 0; i < 6; i++ {
		serving.get(t, "/things")
	}
	if got := serving.reached.count(); got != 4 {
		t.Fatalf("the route behind the shield served %d requests, want 4 before the source was shut out", got)
	}

	refused := serving.get(t, "/things")
	requireThrottledProblem(t, refused)
	requireRetryAfter(t, refused)
	if got := serving.reached.count(); got != 4 {
		t.Fatalf("the route served %d requests, want the count to stop at 4 while the source is refused", got)
	}

	before := serving.reached.count()
	serving.send(t, nethttp.MethodGet, "/things", map[string]string{"X-Forwarded-For": "10.0.0.9"})
	if got := serving.reached.count() - before; got != 1 {
		t.Fatalf("a second address reached the route %d times, want 1: its budget is not the first address's", got)
	}

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

// TestTheShieldChargesOneTokenForASuccess checks that a success costs one token
// and the burst count passes before the next is refused.
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

// TestTheShieldAndTheQuotaAreDistinctLayers checks that the shield is listed in
// the guard stage ahead of the quota in the throttling stage.
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

// TestTheShieldLetsRequestsThroughWhenItsThresholdsAreUnreadable checks that a
// failed settings read stands the shield aside and is reported.
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

// TestTheShieldStandsAsideOnAThresholdOutsideItsDomain checks that a zero or
// negative threshold stands the shield aside.
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

// TestTheShieldDefaultsAreTheDecidedNumbers checks the shield's declared
// defaults.
func TestTheShieldDefaultsAreTheDecidedNumbers(t *testing.T) {
	if defaults.AddressRate != 4 || defaults.AddressBurst != 80 || defaults.AddressPenalty != 20 {
		t.Fatalf("the shield defaults are rate %d, burst %d, penalty %d; want 4, 80, 20",
			defaults.AddressRate, defaults.AddressBurst, defaults.AddressPenalty)
	}
}

// TestAnAddressThresholdOutsideItsDomainIsRefusedWhereItIsWritten checks that the
// store refuses and the shield stands aside on the same out-of-domain values.
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

// TestPrepareRefusesARepeatedAddressEndpoint checks that a name repeated in the
// address list is refused naming the item.
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

// TestReadConfigDecodesBothEndpointLists checks that both endpoint lists decode
// from a real document.
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

func shieldValues() map[string]any {
	return map[string]any{
		addressRatePath:    defaults.AddressRate,
		addressBurstPath:   defaults.AddressBurst,
		addressPenaltyPath: defaults.AddressPenalty,
	}
}
