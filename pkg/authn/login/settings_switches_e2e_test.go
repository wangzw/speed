package login_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

// The account switch this package reads as a setting. It is written out here
// rather than read from the account module so that the cases name the key the
// design lists.
const testSignupKey = "authn.account.signup"

// writeASetting writes one value into the settings store the assembly
// delivered. A failure is fatal: the key being undeclared is the whole point of
// the case that follows it.
func writeASetting(t *testing.T, ctx context.Context, store settings.Store, key string, value any) {
	t.Helper()
	if store == nil {
		t.Fatal("the assembly delivered no settings store")
	}
	write := settings.Write{Layer: settings.Global(), Key: key, Value: value, Actor: "test"}
	if err := store.Set(ctx, write); err != nil {
		t.Fatalf("writing %s: %v, want it declared as a setting", key, err)
	}
}

// signupEnabled reads the registration switch from the methods listing.
func (h *harness) signupEnabled() bool {
	h.t.Helper()
	r := h.get("/api/v1/auth/methods")
	if r.status != http.StatusOK {
		h.t.Fatalf("GET methods answered %d: %s", r.status, r.body)
	}
	signup, ok := r.json(h.t)["signup"].(map[string]any)
	if !ok {
		h.t.Fatalf("the methods listing carries no signup member: %s", r.body)
	}
	enabled, ok := signup["enabled"].(bool)
	if !ok {
		h.t.Fatalf("signup.enabled is %v, want a boolean", signup["enabled"])
	}
	return enabled
}

// signupFlags reads both registration switches from the methods listing: the
// union the two report and the register route's own.
func (h *harness) signupFlags() (enabled, password bool) {
	h.t.Helper()
	r := h.get("/api/v1/auth/methods")
	if r.status != http.StatusOK {
		h.t.Fatalf("GET methods answered %d: %s", r.status, r.body)
	}
	signup, ok := r.json(h.t)["signup"].(map[string]any)
	if !ok {
		h.t.Fatalf("the methods listing carries no signup member: %s", r.body)
	}
	enabled, _ = signup["enabled"].(bool)
	own, ok := signup["password"].(map[string]any)
	if !ok {
		h.t.Fatalf("signup.password is %v, want an object", signup["password"])
	}
	password, _ = own["enabled"].(bool)
	return enabled, password
}

// TestASignupSettingThatIsOpenTurnsTheMethodsListingOn holds that the
// registration switch is read at run time from the settings store the
// deployment holds: the listing is closed with no row and offers signup once
// authn.account.signup is written open, without a restart.
func TestASignupSettingThatIsOpenTurnsTheMethodsListingOn(t *testing.T) {
	assemble(t, func(h *harness) {
		if got := h.signupEnabled(); got {
			t.Fatalf("signup.enabled with no settings row = %v, want false", got)
		}

		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		if got := h.signupEnabled(); !got {
			t.Errorf("signup.enabled after authn.account.signup is open = %v, want true", got)
		}
	})
}

// TestTheTwoSignupSwitchesAreReportedOnTheirOwn holds the discovery document and
// the register route against the real settings store in all four combinations of
// the two switches: signup.enabled is the union, signup.password.enabled reports
// the register route's own switch, and the route is open only when that switch
// is.
func TestTheTwoSignupSwitchesAreReportedOnTheirOwn(t *testing.T) {
	for _, c := range []struct {
		name       string
		signup     string
		password   string
		enabled    bool
		passwordOK bool
		route      int
	}{
		{"both closed", "closed", "closed", false, false, http.StatusForbidden},
		{"the first-login switch alone", "open", "closed", true, false, http.StatusForbidden},
		{"the register switch alone", "closed", "open", true, true, http.StatusCreated},
		{"both open", "open", "open", true, true, http.StatusCreated},
	} {
		t.Run(c.name, func(t *testing.T) {
			assemble(t, func(h *harness) {
				writeASetting(t, h.ctx, h.store, testSignupKey, c.signup)
				writeASetting(t, h.ctx, h.store, testPasswordSignupKey, c.password)

				enabled, password := h.signupFlags()
				if enabled != c.enabled {
					t.Errorf("signup.enabled = %v, want %v", enabled, c.enabled)
				}
				if password != c.passwordOK {
					t.Errorf("signup.password.enabled = %v, want %v", password, c.passwordOK)
				}

				rec := h.registerOne("alice", "alice@example.com", "alice-password")
				if rec.status != c.route {
					t.Fatalf("POST register answered %d, want %d: %s", rec.status, c.route, rec.body)
				}
				if c.route == http.StatusForbidden {
					if got := rec.json(t)["type"]; got != "urn:speed:authn:signup-closed" {
						t.Errorf("the refusal's type = %v, want urn:speed:authn:signup-closed", got)
					}
				}
			})
		})
	}
}

// TestASocialFirstLoginWithAVerifiedAddressOnAnAccountIsLinkRequired holds the
// retirement of the automatic link at the HTTP layer: a first social login whose
// provider proved the address of an existing account answers 401 link-required
// and binds nothing, whatever the settings hold.
func TestASocialFirstLoginWithAVerifiedAddressOnAnAccountIsLinkRequired(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		acct, err := h.admin.Create(h.ctx, authn.CreateAccount{
			Kind:     authn.KindUser,
			Username: "ann",
			Email:    "ann@x.org",
			EmailVerifiedVia: authn.Verification{
				Via: authn.VerifiedViaIDPPrefix + "google",
			},
			Via: "admin",
		})
		if err != nil {
			t.Fatalf("creating the account holding the address: %v", err)
		}

		refused := h.socialLogin()
		if refused.status != http.StatusUnauthorized || !strings.Contains(refused.body, "link-required") {
			t.Fatalf("a first social login answered %d: %s, want 401 link-required",
				refused.status, refused.body)
		}
		if bound, lookupErr := h.accounts.ByExternal(h.ctx, "google", "g1"); lookupErr == nil {
			t.Fatalf("a first social login bound the identity to %q", bound)
		}
		if identifiers, listErr := h.accounts.Identifiers(h.ctx, acct.ID); listErr != nil {
			t.Fatalf("reading the account's identifiers: %v", listErr)
		} else {
			var holdsEmail bool
			for _, idf := range identifiers {
				holdsEmail = holdsEmail || (idf.Kind == authn.IdentifierEmail && idf.Value == "ann@x.org")
			}
			if !holdsEmail {
				t.Errorf("identifiers = %+v, want the administrator's email still there", identifiers)
			}
		}
	})
}

// TestTheRemovedAutoLinkSettingIsNotDeclared holds that the settings store
// refuses the retired key, so a deployment cannot bring the switch back.
func TestTheRemovedAutoLinkSettingIsNotDeclared(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		write := settings.Write{
			Layer: settings.Global(), Key: "authn.account.auto-link", Value: "verified-email", Actor: "test",
		}
		if err := h.store.Set(h.ctx, write); !errors.Is(err, settings.ErrUnknownKey) {
			t.Fatalf("writing the retired switch returned %v, want settings.ErrUnknownKey", err)
		}
	})
}

// socialLogin runs one first social login through the stand-in method and
// returns the reply that settled it: the start itself when the engine resolved
// the proven step there, the answer otherwise.
func (h *harness) socialLogin() reply {
	h.t.Helper()
	start := h.post("/api/v1/auth/flows", map[string]any{"method": "google"})
	if start.status != http.StatusOK {
		return start
	}
	seq := int(start.json(h.t)["seq"].(float64))
	return h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
}

// socialMethod is a stand-in social provider's login method: its first step is
// already a proof of a verified external identity, so a case reaches the
// resolution such a proof governs without an outside provider.
type socialMethod struct{}

var errNoSocialAnswer = errors.New("the stand-in social method has no answer step")

func (socialMethod) Name() string { return "google" }

func (socialMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             "google",
		Kind:             authn.MethodKindRedirect,
		Protocol:         authn.MethodProtocolOIDC,
		Label:            "Google",
		OpensAccount:     true,
		BindingProviders: []string{"google"},
	}
}

func (socialMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:      "google",
			Subject:       "g1",
			DisplayName:   "Ann",
			Email:         "ann@x.org",
			EmailVerified: true,
		},
		Methods: []authn.Method{authn.MethodOIDC},
	}), nil
}

func (socialMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, errNoSocialAnswer
}

// socialModule registers the stand-in method in an assembly.
func socialModule() core.Module {
	return core.Module{
		Name:     "stand-in.google",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return socialMethod{}, nil
		},
	}
}
