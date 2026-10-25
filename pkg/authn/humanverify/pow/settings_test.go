package pow

import (
	"errors"
	"testing"
	"time"
)

// TestTheDeclaredDefaultsAreTheProductDefaults holds the values a deployment
// that configures nothing runs on.
func TestTheDeclaredDefaultsAreTheProductDefaults(t *testing.T) {
	if DefaultDifficulty != 18 {
		t.Errorf("the default difficulty is %d, want 18", DefaultDifficulty)
	}
	if DefaultChallengeTTL != 5*time.Minute {
		t.Errorf("the default challenge lifetime is %v, want 5m", DefaultChallengeTTL)
	}
}

// TestTheDifficultyDomain holds the declared range: sixteen to twenty-two
// leading zero bits, inclusive.
func TestTheDifficultyDomain(t *testing.T) {
	for _, value := range []int{DifficultyMin, 18, DifficultyMax} {
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
}

// TestTheSchemaDeclaresTheTwoItems holds the declaration's shape: the namespace
// the keys live under, the two items, and the mounted defaults.
func TestTheSchemaDeclaresTheTwoItems(t *testing.T) {
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
	if len(s.Mounts) != 1 {
		t.Fatalf("the schema mounts %+v, want the one defaults struct", s.Mounts)
	}
	mounted, ok := s.Mounts[0].Value.(*settingsOptions)
	if !ok {
		t.Fatalf("the schema mounts %T, want *settingsOptions", s.Mounts[0].Value)
	}
	if mounted.Difficulty != DefaultDifficulty || mounted.ChallengeTTL != DefaultChallengeTTL {
		t.Errorf("the mounted defaults are %+v, want %d and %v",
			*mounted, DefaultDifficulty, DefaultChallengeTTL)
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
			settingsStub{difficulty: 16, ttl: time.Hour},
			16, DefaultChallengeTTL,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, _ := newTestProvider(t, c.cfg)
			challenge, err := p.Issue(t.Context(), "203.0.113.7")
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
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 17, ttl: 3 * time.Minute})
	c, err := p.Issue(t.Context(), "203.0.113.7")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if c.Difficulty != 17 {
		t.Errorf("the challenge carries difficulty %d, want 17", c.Difficulty)
	}
	if c.ExpiresIn != 3*time.Minute {
		t.Errorf("the challenge expires in %v, want 3m", c.ExpiresIn)
	}
}
