package resolve

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// The display name a first login opens an account with: every path that creates
// an account takes the name its source gave, makes it fit to store and falls back
// to the username when nothing is left. The phone path's name is collected at
// signup, and a social or enterprise provider may give none.

// TestAFirstLoginWithoutADisplayNameIsNamedAfterItsUsername holds the fallback
// on all three paths that open an account: a social provider and an enterprise
// provider that give no name, and a phone signup that collected none.
func TestAFirstLoginWithoutADisplayNameIsNamedAfterItsUsername(t *testing.T) {
	socialProof := func() authn.Proof {
		p := social("github", "gh1", "", false)
		p.External.DisplayName = ""
		return p
	}
	enterpriseProof := func() authn.Proof {
		p := enterprise("u1", "ann@acme.com")
		p.External.DisplayName = ""
		return p
	}
	for name, tc := range map[string]struct {
		proof authn.Proof
		opts  []option
	}{
		"social":     {socialProof(), nil},
		"phone":      {phone("+8613800000001"), nil},
		"enterprise": {enterpriseProof(), []option{withProvider(authn.ProvisioningJIT, false)}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, append([]option{withSignup(SignupOpen)}, tc.opts...)...)
			res, err := e.resolveAs("collected", tc.proof)
			if err != nil || res.Outcome != OutcomeCreated {
				t.Fatalf("Resolve = %+v, %v, want a new account", res, err)
			}
			if got := e.s.account(res.Account).DisplayName; got != "collected" {
				t.Errorf("account display name = %q, want the username %q", got, "collected")
			}
			if got := e.s.created[len(e.s.created)-1].DisplayName; got != "collected" {
				t.Errorf("Create was given display name %q, want the username %q", got, "collected")
			}
		})
	}
}

// TestAPhoneSignupDisplayNameIsMadeFitToStore holds the shape the shared
// provision layer gives a name the signup step collected: invalid text and
// control characters dropped, the ends trimmed, and the result cut to 100
// characters, which is the bound AccountAdmin.SetDisplayName states.
func TestAPhoneSignupDisplayNameIsMadeFitToStore(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"the ends are trimmed", "  Ann  ", "Ann"},
		{"control characters are dropped", "A\x00n\nn", "Ann"},
		{"invalid text is dropped", "\xffAnn", "Ann"},
		{"a longer name is cut to 100 characters", strings.Repeat("名", 150), strings.Repeat("名", 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, withSignup(SignupOpen))
			pending, err := e.pending(phone("+8613800000001"))
			if err != nil || pending.Pending == nil {
				t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
			}
			res, err := e.r.Create(context.Background(), *pending.Pending, "ann", testPassword, tc.in)
			if err != nil || res.Outcome != OutcomeCreated {
				t.Fatalf("Create = %+v, %v, want a new account", res, err)
			}
			if got := e.s.account(res.Account).DisplayName; got != tc.want {
				t.Errorf("display name = %q, want %q", got, tc.want)
			}
		})
	}
}
