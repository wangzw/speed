package login_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const (
	testPasswordSignupKey = "authn.account.password-signup"
	testSignupIPLimitKey  = "authn.account.signup-ip-limit"
	testSignupGlobalKey   = "authn.account.signup-global-hourly"
)

func openPasswordSignup(t *testing.T, h *harness) {
	t.Helper()
	writeASetting(t, h.ctx, h.store, testPasswordSignupKey, "open")
}

func (h *harness) registerOne(username, email, password string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/register", map[string]string{
		"username": username, "email": email, "password": password,
	})
}

// TestARegistrationThenSignsInWithTheSameCredentials holds that registration opens the account, makes no session, and the password flow then signs in.
func TestARegistrationThenSignsInWithTheSameCredentials(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)

		rec := h.registerOne("alice", "alice@example.com", "alice-password")
		if rec.status != http.StatusCreated {
			t.Fatalf("a registration answered %d: %s", rec.status, rec.body)
		}
		if got := rec.header.Get("Set-Cookie"); got != "" {
			t.Errorf("a registration set the cookie %q, want no session", got)
		}
		body := rec.json(t)
		if body["state"] != "registered" {
			t.Errorf("state = %v, want registered", body["state"])
		}
		accountID, ok := body["account_id"].(string)
		if !ok || accountID == "" {
			t.Fatalf("account_id = %v, want the opened account", body["account_id"])
		}
		verification, ok := body["verification"].(map[string]any)
		if !ok || verification["state"] != "pending" || verification["email"] != "alice@example.com" {
			t.Errorf("verification = %v, want pending at the address submitted", body["verification"])
		}
		if h.selfStatus() {
			t.Error("a registration answered an authenticated session, want none")
		}

		done := h.loginFlow("alice", "alice-password")
		if done.status != http.StatusOK {
			t.Fatalf("the password login after a registration answered %d: %s", done.status, done.body)
		}
		if got := done.json(t)["state"]; got != "authenticated" {
			t.Errorf("the login after a registration answered state %v, want authenticated", got)
		}
		if !h.selfStatus() {
			t.Error("the session a login after a registration delivered does not authenticate")
		}

		identifiers, err := h.accounts.Identifiers(h.ctx, authn.AccountID(accountID))
		if err != nil {
			t.Fatalf("reading the identifiers of the new account: %v", err)
		}
		var kinds []authn.IdentifierKind
		for _, id := range identifiers {
			kinds = append(kinds, id.Kind)
		}
		if !slices.Equal(kinds, []authn.IdentifierKind{authn.IdentifierUsername}) {
			t.Errorf("the new account holds the identifiers %v, want only the username", kinds)
		}
	})
}

// TestARegistrationRefusesAClosedSwitch holds that with no settings row the route answers 403 signup-closed and opens nothing.
func TestARegistrationRefusesAClosedSwitch(t *testing.T) {
	assemble(t, func(h *harness) {
		closed := h.registerOne("alice", "alice@example.com", "alice-password")
		if closed.status != http.StatusForbidden {
			t.Fatalf("a registration with the switch at its default answered %d: %s", closed.status, closed.body)
		}
		if got := closed.json(t)["type"]; got != "urn:speed:authn:signup-closed" {
			t.Errorf("the refusal's type = %v, want urn:speed:authn:signup-closed", got)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "alice"); err == nil {
			t.Error("a closed registration opened the username")
		}

		openPasswordSignup(t, h)
		open := h.registerOne("alice", "alice@example.com", "alice-password")
		if open.status != http.StatusCreated {
			t.Fatalf("a registration after the switch was written open answered %d: %s", open.status, open.body)
		}
	})
}

// TestARegistrationRefusesABadRequest holds the 400s for an unknown member, a missing member and a policy-refused password.
func TestARegistrationRefusesABadRequest(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)

		unknown := h.post("/api/v1/auth/register", map[string]any{
			"username": "alice", "email": "alice@example.com", "password": "alice-password", "admin": true,
		})
		if unknown.status != http.StatusBadRequest {
			t.Errorf("an unknown member answered %d, want 400: %s", unknown.status, unknown.body)
		}
		missing := h.post("/api/v1/auth/register", map[string]any{
			"username": "alice", "password": "alice-password",
		})
		if missing.status != http.StatusBadRequest {
			t.Errorf("a missing email answered %d, want 400: %s", missing.status, missing.body)
		}
		policy := h.registerOne("alice", "alice@example.com", "short")
		if policy.status != http.StatusBadRequest {
			t.Errorf("a password under the policy answered %d, want 400: %s", policy.status, policy.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "alice"); err == nil {
			t.Error("a refused registration opened the username")
		}
	})
}

// TestARegistrationAnswersATakenUsernameWithAConflict holds that a taken username answers 409 username-taken, not the generic conflict.
func TestARegistrationAnswersATakenUsernameWithAConflict(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)

		first := h.registerOne("alice", "alice@example.com", "alice-password")
		if first.status != http.StatusCreated {
			t.Fatalf("the first registration answered %d: %s", first.status, first.body)
		}
		second := h.registerOne("alice", "other@example.com", "alice-password")
		if second.status != http.StatusConflict {
			t.Fatalf("a taken username answered %d, want 409: %s", second.status, second.body)
		}
		if got := second.json(t)["type"]; got != "urn:speed:authn:username-taken" {
			t.Errorf("the conflict's type = %v, want urn:speed:authn:username-taken", got)
		}
	})
}

// TestARegistrationAnswersAReservedEmailWithEmailTaken holds that a second registration naming a reserved address, in any case, answers 409 email-taken.
func TestARegistrationAnswersAReservedEmailWithEmailTaken(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)

		first := h.registerOne("alice", "shared@example.com", "alice-password")
		if first.status != http.StatusCreated {
			t.Fatalf("the first registration answered %d: %s", first.status, first.body)
		}
		second := h.registerOne("bob", "Shared@Example.com", "bob-password")
		if second.status != http.StatusConflict {
			t.Fatalf("a reserved email answered %d, want 409: %s", second.status, second.body)
		}
		if got := second.json(t)["type"]; got != "urn:speed:authn:email-taken" {
			t.Errorf("the refusal's type = %v, want urn:speed:authn:email-taken", got)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "bob"); err == nil {
			t.Error("a refused registration opened the username")
		}
	})
}

// TestThePerSourceBudgetStopsARegistration holds each source's minute bucket, the attempt past it answering 429 and opening no account.
func TestThePerSourceBudgetStopsARegistration(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)
		writeASetting(t, h.ctx, h.store, testSignupIPLimitKey, 1)

		aliceMinute := time.Now().Truncate(time.Minute)
		first := h.registerOne("alice", "alice@example.com", "alice-password")
		if first.status != http.StatusCreated {
			t.Fatalf("the first registration answered %d: %s", first.status, first.body)
		}
		refusedName := "bob"
		bobMinute := time.Now().Truncate(time.Minute)
		second := h.registerOne(refusedName, "bob@example.com", "bob-password")
		if second.status == http.StatusCreated {
			if aliceMinute.Equal(bobMinute) {
				t.Fatalf("a second registration from one source in the same wall-clock minute answered %d: "+
					"the per-source budget of one admitted it", second.status)
			}
			// The boundary gave bob a fresh budget that his own request then
			// spent, so a retry in the same new minute must be refused.
			refusedName = "carol"
			second = h.registerOne(refusedName, "carol@example.com", "carol-password")
		}
		if second.status != http.StatusTooManyRequests {
			t.Fatalf("a registration past the per-source budget answered %d, want 429: %s",
				second.status, second.body)
		}
		if wait := second.header.Get("Retry-After"); wait == "" {
			t.Error("the 429 past the per-source budget carries no Retry-After")
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, refusedName); err == nil {
			t.Error("a registration past the budget opened the username")
		}
	})
}

// TestTheGlobalBudgetStopsARegistration holds one hourly bucket for every source, the attempt past it answering 429.
func TestTheGlobalBudgetStopsARegistration(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)
		writeASetting(t, h.ctx, h.store, testSignupGlobalKey, 1)

		first := h.registerOne("alice", "alice@example.com", "alice-password")
		if first.status != http.StatusCreated {
			t.Fatalf("the first registration answered %d: %s", first.status, first.body)
		}
		second := h.registerOne("bob", "bob@example.com", "bob-password")
		if second.status != http.StatusTooManyRequests {
			t.Fatalf("a registration past the hourly budget answered %d, want 429: %s",
				second.status, second.body)
		}
		if wait := second.header.Get("Retry-After"); wait == "" {
			t.Error("the 429 past the hourly budget carries no Retry-After")
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "bob"); err == nil {
			t.Error("a registration past the budget opened the username")
		}
	})
}
