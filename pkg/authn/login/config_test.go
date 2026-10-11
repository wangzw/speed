package login

import (
	"testing"
	"time"
)

// TestTheSettingDomainsAdmitTheirBounds holds the endpoints of the domains this
// module declares: a value exactly at a bound is inside it, and one past it is
// out.
func TestTheSettingDomainsAdmitTheirBounds(t *testing.T) {
	if !flowTTLInDomain(minFlowTTL) || !flowTTLInDomain(maxFlowTTL) {
		t.Error("a flow-ttl at its own bound is outside the domain")
	}
	if flowTTLInDomain(minFlowTTL-time.Second) || flowTTLInDomain(maxFlowTTL+time.Second) {
		t.Error("a flow-ttl past its bound is inside the domain")
	}
	if !callBudgetInDomain(minCallBudget) || !callBudgetInDomain(maxCallBudget) {
		t.Error("a call-budget at its own bound is outside the domain")
	}
	if callBudgetInDomain(minCallBudget-time.Second) || callBudgetInDomain(maxCallBudget+time.Second) {
		t.Error("a call-budget past its bound is inside the domain")
	}
	if !ipLimitInDomain(0) || !ipLimitInDomain(maxIPLimit) {
		t.Errorf("an ip-limit at its own bound is outside the domain (max is %d)", maxIPLimit)
	}
	if ipLimitInDomain(-1) || ipLimitInDomain(maxIPLimit+1) {
		t.Error("an ip-limit past its bound is inside the domain")
	}
	// The ip-limit setting is per minute, so its ceiling is stated in per-minute
	// terms and matches the design's 0..10000.
	if maxIPLimit != 10000 {
		t.Errorf("maxIPLimit = %d, want 10000", maxIPLimit)
	}
}

// TestDeliveryModesRefuseARepeat holds that a list naming the same mode twice is
// outside the domain.
func TestDeliveryModesRefuseARepeat(t *testing.T) {
	if deliveryModesInDomain([]string{deliverySession, deliverySession}) {
		t.Error("a repeated delivery mode is inside the domain")
	}
	if !deliveryModesInDomain([]string{deliverySession}) || !deliveryModesInDomain([]string{deliverySession, deliveryToken}) {
		t.Error("a legal set of delivery modes is outside the domain")
	}
}

// TestATuningReadFallsBackOutOfDomain holds that a tuning setting outside its
// domain reads as its declared default.
func TestATuningReadFallsBackOutOfDomain(t *testing.T) {
	r := readerWith(map[string]string{ModuleName + "." + flowTTLKey: "40m", ModuleName + "." + ipLimitKey: "10001"})
	if got := r.flowTTL(); got != defaults.FlowTTL {
		t.Errorf("an out-of-domain flow-ttl read %v, want the declared default %v", got, defaults.FlowTTL)
	}
	if got := r.ipLimit(); got != defaults.IPLimit {
		t.Errorf("an out-of-domain ip-limit read %v, want the declared default %v", got, defaults.IPLimit)
	}
}

// TestAnEmptyReturnTargetFallsBack holds that the default-return-to setting read
// as empty falls back to the declared default.
func TestAnEmptyReturnTargetFallsBack(t *testing.T) {
	r := readerWith(map[string]string{ModuleName + "." + defaultReturnToKey: ""})
	if got := r.defaultReturnTo(); got != defaults.DefaultReturnTo {
		t.Errorf("an empty default-return-to read %q, want %q", got, defaults.DefaultReturnTo)
	}
}

// TestThePerFlowSendLimitHoldsItsDomain holds the domain of the per-flow send
// limit this module only reads: a value at a bound is used, and one outside reads
// as the declared default.
func TestThePerFlowSendLimitHoldsItsDomain(t *testing.T) {
	for _, c := range []struct {
		name string
		val  string
		want int
	}{
		{"at the low bound", "1", minPerFlowSends},
		{"at the high bound", "10", maxPerFlowSends},
		{"below the low bound", "0", defaultPerFlowSends},
		{"above the high bound", "11", defaultPerFlowSends},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := readerWith(map[string]string{"authn.sms.per-flow-sends": c.val})
			if got := r.perFlowSends(); got != c.want {
				t.Errorf("per-flow-sends %q read %d, want %d", c.val, got, c.want)
			}
		})
	}
}
