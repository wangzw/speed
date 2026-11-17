package login_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The account module's settings this package reads by path. They are written out
// here so that the cases name the keys the design lists.
const (
	testPasswordSignupKey = "authn.account.password-signup"
	testSignupIPLimitKey  = "authn.account.signup-ip-limit"
	testSignupGlobalKey   = "authn.account.signup-global-hourly"
)

// openPasswordSignup turns the anonymous register route on for a case.
func openPasswordSignup(t *testing.T, h *harness) {
	t.Helper()
	writeASetting(t, h.ctx, h.store, testPasswordSignupKey, "open")
}

// registerOne posts one registration and returns the reply.
func (h *harness) registerOne(username, email, password string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/register", map[string]string{
		"username": username, "email": email, "password": password,
	})
}

// TestARegistrationThenSignsInWithTheSameCredentials holds the whole anonymous
// registration against the real modules: the route opens an account in one
// transaction, answers the pending state of the address, makes no session and
// sets no cookie, and the existing password flow then signs the new account in.
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

		// The email address is only a contact awaiting verification: it is in no
		// identifier row, so the username is the only login name.
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

// TestARegistrationRefusesAClosedSwitch holds the declared default: with no
// settings row the register route is closed, answers 403 signup-closed, and opens
// no account. Writing the switch open takes effect on the next request.
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

// TestARegistrationRefusesABadRequest holds the 400s before an account is opened:
// an unknown member, a missing required member, and a password the policy
// refuses.
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

// TestARegistrationAnswersATakenUsernameWithAConflict holds the one explicit
// conflict a registration reports: the username is taken, and the answer says so
// in the register route's own code rather than the generic conflict.
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

// TestARegistrationAnswersAReservedEmailWithEmailTaken holds the contact
// arbitration: the first registration reserves the address, and a second one
// naming it, in any case, is 409 email-taken and opens no account.
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

// TestThePerSourceBudgetStopsARegistration holds the minute bucket against the
// real key-value store: one source address may register up to the budget, and the
// attempt past it is a 429 with a Retry-After, opening no account.
func TestThePerSourceBudgetStopsARegistration(t *testing.T) {
	assemble(t, func(h *harness) {
		openPasswordSignup(t, h)
		writeASetting(t, h.ctx, h.store, testSignupIPLimitKey, 1)

		aliceMinute := time.Now().Truncate(time.Minute)
		first := h.registerOne("alice", "alice@example.com", "alice-password")
		if first.status != http.StatusCreated {
			t.Fatalf("the first registration answered %d: %s", first.status, first.body)
		}
		// The budget counts a source address's registrations in the wall-clock
		// minute the request reads. A pair split by a minute boundary sees two
		// budgets, so a correct implementation may admit the second request at
		// the start of a new minute; that is the only way it may. An admission
		// inside one minute is the budget being wrong (off by one, or a counter
		// that did not move), so the retry is allowed only when the two requests
		// straddle the boundary; otherwise the case fails as the bug it is.
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

// TestTheGlobalBudgetStopsARegistration holds the deployment-wide hourly bucket
// against the real key-value store: the registrations of every source share one
// bucket, and the attempt past it is a 429 with a Retry-After, opening no
// account.
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
