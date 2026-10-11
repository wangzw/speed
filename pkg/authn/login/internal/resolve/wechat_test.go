package resolve

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A8: a WeChat identity that was bound under the application's open identifier
// and now comes with a union identifier.

func wechat(union, open string) authn.Proof {
	p := social("wechat", union, "", false)
	p.Aliases = []authn.ExternalLink{{Provider: "wechat-openid:app1", Subject: open}}
	return p
}

func TestWeChatUnionKeyIsAddedToTheAccountOfTheOpenKey(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	res, err := e.resolve(wechat("u1", "o1"))
	if err != nil || res.Account != id || res.Outcome != OutcomeUpgraded {
		t.Fatalf("Resolve = %+v, %v, want a1 upgraded", res, err)
	}
	if got, ok := e.s.boundTo("wechat", "u1"); !ok || got != id {
		t.Errorf("union binding = %v, %v, want it on a1", got, ok)
	}
	if got, ok := e.s.boundTo("wechat-openid:app1", "o1"); !ok || got != id {
		t.Errorf("open binding = %v, %v, want it kept", got, ok)
	}
	if len(e.s.created) != 0 || len(e.s.membershipPuts) != 0 || e.s.membershipRead != 0 || len(res.Cleanup) != 0 {
		t.Errorf("an upgrade created %d accounts, touched memberships, or owes cleanup %v", len(e.s.created), res.Cleanup)
	}
}

func TestWeChatUnionKeyHeldByAnotherAccountIsAConflictAndMergesNothing(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	e.s.seed(t, seed{id: "b1", status: authn.StatusActive, bindings: [][2]string{{"wechat", "u1"}}})
	_, err := e.resolve(wechat("u1", "o1"))
	if !errors.Is(err, authn.ErrBindingConflict) {
		t.Fatalf("err = %v, want ErrBindingConflict", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none: the two accounts are not merged", e.s.writes())
	}
	for id, key := range map[authn.AccountID][2]string{"a1": {"wechat-openid:app1", "o1"}, "b1": {"wechat", "u1"}} {
		if got, _ := e.s.boundTo(key[0], key[1]); got != id {
			t.Errorf("%s moved to %s", key[1], got)
		}
	}
}

func TestWeChatUnionKeyTakenWhileUpgradingIsAConflict(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	e.s.seed(t, seed{id: "b1", status: authn.StatusActive})
	e.s.beforeLink = func(_ authn.AccountID, l authn.ExternalLink) {
		e.s.mu.Lock()
		defer e.s.mu.Unlock()
		e.s.binding[bkey(l.Provider, l.Subject)] = "b1"
	}
	_, err := e.resolve(wechat("u1", "o1"))
	if !errors.Is(err, authn.ErrBindingConflict) {
		t.Errorf("err = %v, want ErrBindingConflict", err)
	}
}

func TestWeChatBothKeysOnTheSameAccountIsAnOrdinaryLogin(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat", "u1"}, {"wechat-openid:app1", "o1"}}})
	res, err := e.resolve(wechat("u1", "o1"))
	if err != nil || res.Account != "a1" || res.Outcome != OutcomeBound {
		t.Errorf("Resolve = %+v, %v", res, err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestWeChatUpgradeOfADisabledAccountDoesNotHappen(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusDisabled, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	_, err := e.resolve(wechat("u1", "o1"))
	if !errors.Is(err, authn.ErrAccountUnavailable) {
		t.Fatalf("err = %v, want ErrAccountUnavailable", err)
	}
	if _, ok := e.s.boundTo("wechat", "u1"); ok {
		t.Error("the union key was bound to a disabled account")
	}
}

func TestWeChatTwoEarlierKeysOfTwoAccountsAreAConflict(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	e.s.seed(t, seed{id: "b1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app2", "o2"}}})
	p := wechat("u1", "o1")
	p.Aliases = append(p.Aliases, authn.ExternalLink{Provider: "wechat-openid:app2", Subject: "o2"})
	if _, err := e.resolve(p); !errors.Is(err, authn.ErrBindingConflict) {
		t.Errorf("err = %v, want ErrBindingConflict", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestWeChatNewIdentityWithNoEarlierBindingIsAFirstLogin(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(wechat("u1", "o1"))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if got, ok := e.s.boundTo("wechat", "u1"); !ok || got != res.Account {
		t.Errorf("binding = %v, %v, want the union key on the new account", got, ok)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none: WeChat has no address", ids)
	}
}

func TestEarlierKeysAreForWeChatOpenIdentifiersOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    authn.Proof
	}{
		{"another provider's alias", func() authn.Proof {
			p := social("wechat", "u1", "", false)
			p.Aliases = []authn.ExternalLink{{Provider: "google", Subject: "g1"}}
			return p
		}()},
		{"an alias on a provider that has none", func() authn.Proof {
			p := social("google", "g1", "", false)
			p.Aliases = []authn.ExternalLink{{Provider: "wechat-openid:app1", Subject: "o1"}}
			return p
		}()},
		{"an alias with no subject", func() authn.Proof {
			p := social("wechat", "u1", "", false)
			p.Aliases = []authn.ExternalLink{{Provider: "wechat-openid:app1"}}
			return p
		}()},
		{"an alias on an enterprise proof", func() authn.Proof {
			p := enterprise("u1", "ann@acme.com")
			p.Aliases = []authn.ExternalLink{{Provider: "wechat-openid:app1", Subject: "o1"}}
			return p
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A victim's binding is on the key the alias names: honoring the alias
			// would bind an attacker's identity to it.
			e := newEnv(t, withSignup(SignupOpen), withProvider(authn.ProvisioningJIT, true))
			e.s.seed(t, seed{id: "victim", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}, {"wechat-openid:app1", "o1"}}})
			e.s.mu.Lock()
			delete(e.s.binding, bkey("google", "g1")) // the primary is unbound, so the alias would be consulted
			e.s.mu.Unlock()
			_, err := e.resolve(tc.p)
			if !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("err = %v, want ErrInvalidArgument", err)
			}
			if len(e.s.linked) != 0 {
				t.Errorf("linked %v, want nothing", e.s.linked)
			}
		})
	}
}
