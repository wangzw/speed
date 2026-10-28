package account

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
)

// The descriptor states what the design states: the database is required and is
// the one thing the module stands on, the eight capabilities are each delivered
// exclusively and by nothing else, and the module declares its own section, its
// settings and its migrations as resources.
func TestTheDescriptorStatesWhatTheDesignStates(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.account" {
		t.Errorf("the module is named %q (constant %q), want authn.account", m.Name, ModuleName)
	}
	if len(m.Requires) != 2 || m.Requires[0].Token != core.Token((*db.Database)(nil)) || m.Requires[0].Optional {
		t.Errorf("Requires is %+v, want the database required and the logging capability optional", m.Requires)
	}
	if last := m.Requires[len(m.Requires)-1]; last.Token != core.Token((*log.Logger)(nil)) || !last.Optional {
		t.Errorf("Requires = %+v, want the logging capability as the optional dependency", m.Requires)
	}
	want := []core.Token{
		core.Token((*authn.Accounts)(nil)),
		core.Token((*authn.AccountAdmin)(nil)),
		core.Token((*authn.Signups)(nil)),
		core.Token((*authn.SignupVerification)(nil)),
		core.Token((*authn.Passwords)(nil)),
		core.Token((*authn.Memberships)(nil)),
		core.Token((*authn.MembershipCleanup)(nil)),
		core.Token((*authn.Throttle)(nil)),
	}
	if len(m.Provides) != len(want) {
		t.Fatalf("Provides has %d entries, want the eight capabilities", len(m.Provides))
	}
	for _, provision := range m.Provides {
		if !provision.Exclusive {
			t.Errorf("the capability %v is not delivered exclusively", provision.Token)
		}
		if !containsToken(want, provision.Token) {
			t.Errorf("the capability %v is not one of the eight", provision.Token)
		}
	}
	if len(m.Resources) != 3 {
		t.Errorf("Resources has %d entries, want the section, the settings and the migrations", len(m.Resources))
	}
}

func containsToken(tokens []core.Token, want core.Token) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

// Every product satisfies all eight capabilities, which the package asserts at
// compile time; a case holds that the instance the descriptor builds is the one
// product they are asserted for.
func TestTheProductSatisfiesEachCapability(t *testing.T) {
	var p any = newProduct(stubDatabase{dialect: db.SQLite}, mustResolve(t, configDefaults))
	if _, ok := p.(authn.Accounts); !ok {
		t.Error("the product is not an Accounts")
	}
	if _, ok := p.(authn.AccountAdmin); !ok {
		t.Error("the product is not an AccountAdmin")
	}
	if _, ok := p.(authn.Signups); !ok {
		t.Error("the product is not a Signups")
	}
	if _, ok := p.(authn.SignupVerification); !ok {
		t.Error("the product is not a SignupVerification")
	}
	if _, ok := p.(authn.Passwords); !ok {
		t.Error("the product is not a Passwords")
	}
	if _, ok := p.(authn.Memberships); !ok {
		t.Error("the product is not a Memberships")
	}
	if _, ok := p.(authn.MembershipCleanup); !ok {
		t.Error("the product is not a MembershipCleanup")
	}
	if _, ok := p.(authn.Throttle); !ok {
		t.Error("the product is not a Throttle")
	}
}

func mustResolve(t *testing.T, c moduleConfig) resolved {
	t.Helper()
	r, err := c.resolve()
	if err != nil {
		t.Fatalf("resolving the default configuration: %v", err)
	}
	return r
}

// Prepare states the module runs whatever the section says, and a section it
// cannot run on is a startup failure and not a stand-down.
func TestPrepareStatesTheModuleRunsOrRefusesTheConfiguration(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		ok   bool
	}{
		{"nothing configured", `{}`, true},
		{"a concurrency of zero", `{"authn":{"account":{"password-concurrency":0}}}`, false},
		{"a budget below the floor", `{"authn":{"account":{"password-memory-budget-mib":1}}}`, false},
	} {
		reader := authntest.ConfigReader(t, Module(), c.body)
		reg := core.New()
		reg.Register(config.Module())
		reg.Supply(reader, config.ModuleName)
		got, err := Module().Prepare(t.Context(), reg)
		switch {
		case c.ok && (err != nil || got.State != core.StateEnabled):
			t.Errorf("%s: Prepare gave (%v, %v), want the enabled stance", c.name, got.State, err)
		case !c.ok && !errors.Is(err, authn.ErrInvalidConfig):
			t.Errorf("%s: Prepare gave %v, want authn.ErrInvalidConfig", c.name, err)
		}
	}
}

// The bounds of the sizes are judged when the module is built.
func TestTheSizesAreHeldToTheirDomains(t *testing.T) {
	base := configDefaults
	cases := []struct {
		name   string
		mutate func(*moduleConfig)
		ok     bool
	}{
		{"the default", func(*moduleConfig) {}, true},
		{"a concurrency of one", func(c *moduleConfig) { c.PasswordConcurrency = 1 }, true},
		{"a concurrency past the ceiling", func(c *moduleConfig) { c.PasswordConcurrency = maxConcurrency + 1 }, false},
		{"a concurrency below one", func(c *moduleConfig) { c.PasswordConcurrency = 0 }, false},
		{"a signup concurrency of one", func(c *moduleConfig) { c.SignupConcurrency = 1 }, true},
		{"a signup concurrency past the ceiling", func(c *moduleConfig) { c.SignupConcurrency = maxConcurrency + 1 }, false},
		{"a signup concurrency below one", func(c *moduleConfig) { c.SignupConcurrency = 0 }, false},
		{"a budget at the floor", func(c *moduleConfig) { c.PasswordMemoryBudgetMiB = memoryBudgetFloorMiB }, true},
		{"a budget past the ceiling", func(c *moduleConfig) { c.PasswordMemoryBudgetMiB = maxMemoryBudgetMiB + 1 }, false},
	}
	for _, item := range cases {
		c := base
		item.mutate(&c)
		_, err := c.resolve()
		if item.ok && err != nil {
			t.Errorf("%s: resolve gave %v, want no error", item.name, err)
		}
		if !item.ok && !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("%s: resolve gave %v, want authn.ErrInvalidConfig", item.name, err)
		}
	}
}

// The sizes are configuration a host states anywhere; the algorithm, the
// relaxation switches and the registration parameters are settings, not
// configuration.
func TestTheConfigurationItemsAreTheSizes(t *testing.T) {
	s := schema()
	for _, key := range []string{concurrencyItem, signupConcurrencyItem, memoryBudgetItem} {
		if got := s.Items[key].Origins; got != 0 {
			t.Errorf("%s takes the origins %v, want every source", key, got)
		}
	}
	for _, key := range []string{algorithmKey, signupKey, passwordSignupKey, autoLinkKey} {
		if _, ok := s.Items[key]; ok {
			t.Errorf("the configuration declares %s, want it declared as a setting alone", key)
		}
	}
}

// The settings are declared with a domain, so that a write is judged when it is
// made and again when it is read.
func TestTheSettingsAreDeclaredWithADomain(t *testing.T) {
	s := settingsSchema()
	want := []string{
		algorithmKey, signupKey, passwordSignupKey, autoLinkKey,
		signupIPLimitKey, signupHourlyKey, unverifiedTTLKey, unverifiedSweepKey,
		minLengthKey, argonMemoryKey, argonIterationKey, argonLanesKey, pbkdf2IterKey,
		freeAttemptsKey, maxDelayKey, decayKey, retentionKey, reservedKey,
	}
	for _, key := range want {
		if _, ok := s.Items[key]; !ok {
			t.Errorf("the setting %s is not declared", key)
		}
	}
	if len(s.Items) != len(want) {
		t.Errorf("the section declares %d settings, want %d", len(s.Items), len(want))
	}
	if !signupInDomain(defaults.Signup) || !autoLinkInDomain(defaults.AutoLink) {
		t.Errorf("a declared default lies outside its domain: %q, %q", defaults.Signup, defaults.AutoLink)
	}
	if defaults.Signup != signupClosed || defaults.AutoLink != autoLinkNever || defaults.PasswordSignup != signupClosed {
		t.Errorf("the declared defaults are %q, %q and %q, want the strict values",
			defaults.Signup, defaults.AutoLink, defaults.PasswordSignup)
	}
}
