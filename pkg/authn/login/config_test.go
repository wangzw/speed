package login

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestTheSettingDomainsAdmitTheirBounds holds that a value exactly at a bound is inside its domain and one past it is out.
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
	if maxIPLimit != 10000 {
		t.Errorf("maxIPLimit = %d, want 10000", maxIPLimit)
	}
}

// TestTheEmailBudgetDomainsAdmitTheirBounds holds the four email budgets at their endpoints, with the declared defaults inside.
func TestTheEmailBudgetDomainsAdmitTheirBounds(t *testing.T) {
	for _, c := range []struct {
		name     string
		inDomain func(int) bool
		lo, hi   int
		def      int
	}{
		{
			"email-per-address-hourly", emailPerAddressHourlyInDomain,
			minEmailPerAddressHourly, maxEmailPerAddressHourly, defaults.EmailPerAddressHourly,
		},
		{
			"email-per-address-daily", emailPerAddressDailyInDomain,
			minEmailPerAddressDaily, maxEmailPerAddressDaily, defaults.EmailPerAddressDaily,
		},
		{
			"email-per-account-hourly", emailPerAccountHourlyInDomain,
			minEmailPerAccountHourly, maxEmailPerAccountHourly, defaults.EmailPerAccountHourly,
		},
		{
			"email-per-account-daily", emailPerAccountDailyInDomain,
			minEmailPerAccountDaily, maxEmailPerAccountDaily, defaults.EmailPerAccountDaily,
		},
		{
			"email-per-source-hourly", emailPerSourceHourlyInDomain,
			minEmailPerSourceHourly, maxEmailPerSourceHourly, defaults.EmailPerSourceHourly,
		},
		{
			"email-per-source-daily", emailPerSourceDailyInDomain,
			minEmailPerSourceDaily, maxEmailPerSourceDaily, defaults.EmailPerSourceDaily,
		},
		{
			"email-global-hourly", emailGlobalHourlyInDomain,
			minEmailGlobalHourly, maxEmailGlobalHourly, defaults.EmailGlobalHourly,
		},
	} {
		if !c.inDomain(c.lo) || !c.inDomain(c.hi) {
			t.Errorf("%s: a value at its own bound is outside the domain", c.name)
		}
		if c.inDomain(c.lo-1) || c.inDomain(c.hi+1) {
			t.Errorf("%s: a value past its bound is inside the domain", c.name)
		}
		if !c.inDomain(c.def) {
			t.Errorf("%s: the declared default %d is outside the domain", c.name, c.def)
		}
	}
	if defaults.EmailPerAddressHourly != 5 || defaults.EmailPerAddressDaily != 10 ||
		defaults.EmailPerAccountHourly != 10 || defaults.EmailPerAccountDaily != 100 ||
		defaults.EmailPerSourceHourly != 30 || defaults.EmailPerSourceDaily != 100 ||
		defaults.EmailGlobalHourly != 300 {
		t.Errorf("the email budgets default to %d/%d/%d/%d/%d/%d/%d, want 5/10/10/100/30/100/300",
			defaults.EmailPerAddressHourly, defaults.EmailPerAddressDaily,
			defaults.EmailPerAccountHourly, defaults.EmailPerAccountDaily,
			defaults.EmailPerSourceHourly, defaults.EmailPerSourceDaily, defaults.EmailGlobalHourly)
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

// TestTheRegistrationReadsFallBackToTheirStrictSide holds the three reads
// of the account module's registration settings at their two fallbacks.
func TestTheRegistrationReadsFallBackToTheirStrictSide(t *testing.T) {
	failing := readerWith(nil) // every key the map does not carry fails to read

	if got := failing.passwordSignup(); got != signupClosed {
		t.Errorf("a failed read of the password switch = %q, want %q", got, signupClosed)
	}
	if got := failing.signupIPLimit(); got != signupIPLimitDefault {
		t.Errorf("a failed read of the per-source budget = %d, want %d off", got, signupIPLimitDefault)
	}
	if got := failing.signupGlobalHourly(); got != signupGlobalHourlyDefault {
		t.Errorf("a failed read of the deployment-wide budget = %d, want the declared default %d",
			got, signupGlobalHourlyDefault)
	}

	cases := []struct {
		name     string
		values   map[string]string
		password string
		ipLimit  int
		global   int
	}{
		{"the password switch outside its set", map[string]string{passwordSignupPath: "on"}, signupClosed, signupIPLimitDefault, signupGlobalHourlyDefault},
		{"the per-source budget below zero", map[string]string{signupIPLimitPath: "-1"}, signupClosed, signupIPLimitDefault, signupGlobalHourlyDefault},
		{"the per-source budget above its ceiling", map[string]string{signupIPLimitPath: "10001"}, signupClosed, signupIPLimitDefault, signupGlobalHourlyDefault},
		{"the deployment-wide budget below its floor", map[string]string{signupGlobalHourlyPath: "0"}, signupClosed, signupIPLimitDefault, signupGlobalHourlyDefault},
		{"the deployment-wide budget above its ceiling", map[string]string{signupGlobalHourlyPath: "1000001"}, signupClosed, signupIPLimitDefault, signupGlobalHourlyDefault},
		{"the in-domain values", map[string]string{
			passwordSignupPath: signupOpen, signupIPLimitPath: "10000", signupGlobalHourlyPath: "1",
		}, signupOpen, 10000, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := readerWith(c.values)
			if got := r.passwordSignup(); got != c.password {
				t.Errorf("passwordSignup = %q, want %q", got, c.password)
			}
			if got := r.signupIPLimit(); got != c.ipLimit {
				t.Errorf("signupIPLimit = %d, want %d", got, c.ipLimit)
			}
			if got := r.signupGlobalHourly(); got != c.global {
				t.Errorf("signupGlobalHourly = %d, want %d", got, c.global)
			}
		})
	}
}

// TestTheDisabledMethodsDomainNeverAdmitsPassword holds that the domain parses a JSON array and refuses a value naming password.
func TestTheDisabledMethodsDomainNeverAdmitsPassword(t *testing.T) {
	item, ok := settingsSchema().Items[disabledMethodsKey]
	if !ok || item.Domain.Rule() == "" {
		t.Fatal("the disabled-methods item declares no domain, so a write naming password would be stored")
	}
	for _, c := range []struct {
		text string
		ok   bool
	}{
		{"", true},
		{"[]", true},
		{`["sms"]`, true},
		{`["sms","password"]`, false},
		{`["password"]`, false},
		{"not json", false},
	} {
		if got := disabledMethodsTextInDomain(c.text); got != c.ok {
			t.Errorf("disabledMethodsTextInDomain(%q) = %v, want %v", c.text, got, c.ok)
		}
	}
}

// TestTheDisabledMethodsReadIgnoresPasswordAndReportsItOnce holds that password is never turned off and its out-of-domain value is reported once.
func TestTheDisabledMethodsReadIgnoresPasswordAndReportsItOnce(t *testing.T) {
	var buf bytes.Buffer
	r := newReader(&fakeStore{values: map[string]string{disabledMethodsPath: `["sms","password"]`}},
		slog.New(slog.NewTextHandler(&buf, nil)))
	for i := range 2 {
		got, err := r.disabledMethods()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if len(got) != 1 || got[0] != "sms" {
			t.Fatalf("read %d = %v, want only sms", i, got)
		}
	}
	if n := strings.Count(buf.String(), "level=ERROR"); n != 1 {
		t.Errorf("one raw value wrote %d Error lines, want 1: %s", n, buf.String())
	}

	var quiet bytes.Buffer
	only := newReader(&fakeStore{values: map[string]string{disabledMethodsPath: `["password"]`}},
		slog.New(slog.NewTextHandler(&quiet, nil)))
	if got, err := only.disabledMethods(); err != nil || len(got) != 0 {
		t.Errorf("reading a value naming only password = %v, %v; want the empty list", got, err)
	}
}

// TestTheDeliveredSwitchMatchesTheSettingsRead holds that the delivered switch matches the settings read and an error rather than an empty list.
func TestTheDeliveredSwitchMatchesTheSettingsRead(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	reader := newReader(&fakeStore{values: map[string]string{disabledMethodsPath: `["sms","password"]`}}, logger)

	got, err := (&product{settings: reader}).DisabledMethods()
	want, wantErr := reader.disabledMethods()
	if (err != nil) != (wantErr != nil) {
		t.Fatalf("the delivered switch answered %v, the reader %v", err, wantErr)
	}
	if err != nil {
		t.Fatalf("the delivered switch failed: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the delivered switch = %v, the reader = %v", got, want)
	}
	if len(got) != 1 || got[0] != "sms" {
		t.Errorf("the delivered switch = %v, want only sms: password is never turned off", got)
	}

	failing := &product{settings: newReader(&fakeStore{values: map[string]string{}}, logger)}
	if got, err := failing.DisabledMethods(); err == nil {
		t.Errorf("a failed read answered %v, nil; want an error", got)
	} else if got != nil {
		t.Errorf("a failed read answered %v with %v, want no names", got, err)
	}
}

// TestThePerFlowSendLimitHoldsItsDomain holds that an out-of-domain read falls back to the declared default.
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

// TestThePerFlowSendLimitTreatsAnAbsentDeclaringModuleAsTheDefault holds that an undeclared key uses the default without an Error.
func TestThePerFlowSendLimitTreatsAnAbsentDeclaringModuleAsTheDefault(t *testing.T) {
	var absent bytes.Buffer
	r := newReader(&fakeStore{values: map[string]string{}},
		slog.New(slog.NewTextHandler(&absent, nil)))
	if got := r.perFlowSends(); got != defaultPerFlowSends {
		t.Fatalf("an undeclared per-flow-sends read %d, want the default %d", got, defaultPerFlowSends)
	}
	if strings.Contains(absent.String(), "per-flow") {
		t.Errorf("an absent declaring module was diagnosed: %s", absent.String())
	}

	var failed bytes.Buffer
	r = newReader(&fakeStore{values: map[string]string{"authn.sms.per-flow-sends": "not-an-int"}},
		slog.New(slog.NewTextHandler(&failed, nil)))
	if got := r.perFlowSends(); got != defaultPerFlowSends {
		t.Fatalf("an unreadable per-flow-sends read %d, want the default %d", got, defaultPerFlowSends)
	}
	if !strings.Contains(failed.String(), "per-flow") {
		t.Errorf("a failed per-flow-sends read was not diagnosed: %s", failed.String())
	}
}
