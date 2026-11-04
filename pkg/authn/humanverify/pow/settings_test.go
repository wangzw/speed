package pow

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheDeclaredDefaultsAreTheProductDefaults holds the values a deployment
// that configures nothing runs on.
func TestTheDeclaredDefaultsAreTheProductDefaults(t *testing.T) {
	if DefaultDifficulty != 20 {
		t.Errorf("the default difficulty is %d, want 20", DefaultDifficulty)
	}
	if DifficultyMin != 18 {
		t.Errorf("the difficulty minimum is %d, want 18", DifficultyMin)
	}
	if DifficultyMax != 22 {
		t.Errorf("the difficulty maximum is %d, want 22", DifficultyMax)
	}
	if DefaultChallengeTTL != 5*time.Minute {
		t.Errorf("the default challenge lifetime is %v, want 5m", DefaultChallengeTTL)
	}
	if DefaultGlobalIssueLimit != 600 {
		t.Errorf("the default global issuance limit is %d, want 600", DefaultGlobalIssueLimit)
	}
}

// TestTheGlobalIssueLimitDomain holds the declared range: one to a million
// challenges a window, inclusive.
func TestTheGlobalIssueLimitDomain(t *testing.T) {
	for _, value := range []int{GlobalIssueLimitMin, 600, GlobalIssueLimitMax} {
		if !globalIssueLimitInDomain(value) {
			t.Errorf("the domain refuses %d", value)
		}
	}
	for _, value := range []int{GlobalIssueLimitMin - 1, GlobalIssueLimitMax + 1, 0, -1} {
		if globalIssueLimitInDomain(value) {
			t.Errorf("the domain admits %d", value)
		}
	}
}

// TestTheDifficultyDomain holds the declared range: eighteen to twenty-two
// leading zero bits, inclusive.
func TestTheDifficultyDomain(t *testing.T) {
	for _, value := range []int{DifficultyMin, 20, DifficultyMax} {
		if !difficultyInDomain(value) {
			t.Errorf("the domain refuses %d", value)
		}
	}
	for _, value := range []int{DifficultyMin - 1, DifficultyMax + 1, 0, -1} {
		if difficultyInDomain(value) {
			t.Errorf("the domain admits %d", value)
		}
	}
}

// TestTheChallengeTTLDomain holds the declared range: one minute to fifteen
// minutes, inclusive.
func TestTheChallengeTTLDomain(t *testing.T) {
	for _, value := range []time.Duration{ChallengeTTLMin, 5 * time.Minute, ChallengeTTLMax} {
		if !challengeTTLInDomain(value) {
			t.Errorf("the domain refuses %v", value)
		}
	}
	for _, value := range []time.Duration{ChallengeTTLMin - time.Second, ChallengeTTLMax + time.Second, 0} {
		if challengeTTLInDomain(value) {
			t.Errorf("the domain admits %v", value)
		}
	}
}

// TestTheFullKeyPathsAreTheNamespacedOnes holds the read paths to the keys the
// schema declares, since a store names an item by its full path and a relative
// key would read as an unknown item and silently fall back to the default.
func TestTheFullKeyPathsAreTheNamespacedOnes(t *testing.T) {
	if difficultyPath != "authn.humanverify.pow.difficulty" {
		t.Errorf("difficultyPath = %q", difficultyPath)
	}
	if challengeTTLPath != "authn.humanverify.pow.challenge-ttl" {
		t.Errorf("challengeTTLPath = %q", challengeTTLPath)
	}
	if globalIssueLimitPath != "authn.humanverify.pow.global-issue-limit" {
		t.Errorf("globalIssueLimitPath = %q", globalIssueLimitPath)
	}
}

// TestTheSchemaDeclaresTheThreeItems holds the declaration's shape: the
// namespace the keys live under, the three items, and the mounted defaults.
func TestTheSchemaDeclaresTheThreeItems(t *testing.T) {
	s := Schema()
	if s.Namespace != Namespace {
		t.Errorf("the schema namespace is %q, want %q", s.Namespace, Namespace)
	}
	if _, ok := s.Items[DifficultyKey]; !ok {
		t.Errorf("the schema declares no %q item", DifficultyKey)
	}
	if _, ok := s.Items[ChallengeTTLKey]; !ok {
		t.Errorf("the schema declares no %q item", ChallengeTTLKey)
	}
	if _, ok := s.Items[GlobalIssueLimitKey]; !ok {
		t.Errorf("the schema declares no %q item", GlobalIssueLimitKey)
	}
	if len(s.Mounts) != 1 {
		t.Fatalf("the schema mounts %+v, want the one defaults struct", s.Mounts)
	}
	mounted, ok := s.Mounts[0].Value.(*settingsOptions)
	if !ok {
		t.Fatalf("the schema mounts %T, want *settingsOptions", s.Mounts[0].Value)
	}
	if mounted.Difficulty != DefaultDifficulty || mounted.ChallengeTTL != DefaultChallengeTTL ||
		mounted.GlobalIssueLimit != DefaultGlobalIssueLimit {
		t.Errorf("the mounted defaults are %+v, want %d, %v and %d",
			*mounted, DefaultDifficulty, DefaultChallengeTTL, DefaultGlobalIssueLimit)
	}
}

// TestAnUnreadableOrOutOfDomainSettingFallsBackToTheDefault holds the read
// direction: a store that cannot answer, and a value outside the domain, both
// read as the declared default rather than blocking the gate. The other item
// is still read from the store, so one defective value does not discard the
// pair.
func TestAnUnreadableOrOutOfDomainSettingFallsBackToTheDefault(t *testing.T) {
	for _, c := range []struct {
		name       string
		cfg        settingsStub
		difficulty int
		ttl        time.Duration
	}{
		{
			"unreadable",
			settingsStub{err: errors.New("the snapshot is not loaded")},
			DefaultDifficulty, DefaultChallengeTTL,
		},
		{
			"difficulty outside the domain",
			settingsStub{difficulty: 99, ttl: time.Minute},
			DefaultDifficulty, time.Minute,
		},
		{
			"ttl outside the domain",
			settingsStub{difficulty: 18, ttl: time.Hour},
			18, DefaultChallengeTTL,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, _ := newTestProvider(t, c.cfg)
			challenge, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"})
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if challenge.Difficulty != c.difficulty {
				t.Errorf("the challenge carries difficulty %d, want %d", challenge.Difficulty, c.difficulty)
			}
			if challenge.ExpiresIn != c.ttl {
				t.Errorf("the challenge expires in %v, want %v", challenge.ExpiresIn, c.ttl)
			}
		})
	}
}

// TestAConfiguredSettingIsUsed holds the read direction the other way: what the
// store answers is what the challenge carries.
func TestAConfiguredSettingIsUsed(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 19, ttl: 3 * time.Minute})
	c, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if c.Difficulty != 19 {
		t.Errorf("the challenge carries difficulty %d, want 19", c.Difficulty)
	}
	if c.ExpiresIn != 3*time.Minute {
		t.Errorf("the challenge expires in %v, want 3m", c.ExpiresIn)
	}
}
