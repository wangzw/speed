package selfservice_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
)

// emailBudgets are the setting keys the email branch counts against.
const (
	budgetAddressHourly = "authn.login.email-per-address-hourly"
	budgetAddressDaily  = "authn.login.email-per-address-daily"
	budgetAccountHourly = "authn.login.email-per-account-hourly"
	budgetGlobalHourly  = "authn.login.email-global-hourly"
)

// budgetHarness is an email harness whose budget settings a case scripts. Every
// value not named reads as its declared default, because the fake has no entry
// for it.
func budgetHarness(t *testing.T, numbers map[string]int) *harness {
	t.Helper()
	h := emailHarness(t)
	h.settings.numbers = numbers
	return h
}

// reauthenticate refreshes the identity's first-factor time against the harness
// clock, so a case that moves the clock across a bucket boundary still meets the
// five-minute freshness every change of credentials asks for. The options are
// applied before the time, so a case can name another account or session.
func (h *harness) reauthenticate(opts ...authntest.Option) {
	h.as(append(opts, authntest.WithAuthTime(h.clockNow.Add(-time.Minute)))...)
}

// hasKey reports whether the store holds a key with the prefix.
func hasKey(h *harness, prefix string) bool {
	for _, key := range h.kv.keys() {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// TestAnAddressAnotherAccountHoldsIsRejected holds the refusal: an address
// another account carries as a verified identifier is answered 409 email-taken,
// gets no message, spends no resend window or budget, and keeps no pending
// record.
func TestAnAddressAnotherAccountHoldsIsRejected(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = "someone-else"

	w := h.post("/identifiers", addBody("email", "Ada@Example.com"))
	if w.Code != http.StatusConflict || problemType(t, w) != "email-taken" {
		t.Fatalf("the add answered %d %s, want 409 email-taken", w.Code, w.Body)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("a taken address was sent %d message(s), want none", len(h.email.sent))
	}
	if hasKey(h, "authn:email-resend:") {
		t.Error("a taken address spent a resend window")
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Error("a taken address kept a pending record")
	}
	if got := h.accounts.looked; len(got) != 1 || got[0] != "ada@example.com" {
		t.Errorf("Lookup was asked for %v, want the one normalized address", got)
	}
}

// TestAnAddressReservedByAnotherAccountIsRejected holds the other source of a
// taken address: an unproved reservation a self-registration wrote. The asking
// account is told email-taken before a code is drawn.
func TestAnAddressReservedByAnotherAccountIsRejected(t *testing.T) {
	h := emailHarness(t)
	h.signup.pending["reserved@example.com"] = "someone-else"

	w := h.post("/identifiers", addBody("email", "Reserved@Example.com"))
	if w.Code != http.StatusConflict || problemType(t, w) != "email-taken" {
		t.Fatalf("the add answered %d %s, want 409 email-taken", w.Code, w.Body)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("a reserved address was sent %d message(s), want none", len(h.email.sent))
	}
	if got := h.signup.asked; len(got) != 1 || got[0] != "email:reserved@example.com" {
		t.Errorf("the reservation read was asked for %v, want the one normalized address", got)
	}
}

// TestATakenAddressIsAnsweredBeforeTheStoreIsRead holds the order: the taken
// check runs before the resend claim, so a taken address is 409 even when the
// store cannot answer, while a free address still meets the outage as a 503.
func TestATakenAddressIsAnsweredBeforeTheStoreIsRead(t *testing.T) {
	storeDown := errors.New("the store is unavailable")
	taken := emailHarness(t)
	taken.accounts.lookup["taken@example.com"] = "someone-else"
	taken.kv.setErr = storeDown

	w := taken.post("/identifiers", addBody("email", "taken@example.com"))
	if w.Code != http.StatusConflict || problemType(t, w) != "email-taken" {
		t.Errorf("the taken address answered %d %s, want 409 email-taken", w.Code, w.Body)
	}
	if len(taken.email.sent) != 0 {
		t.Errorf("the taken address was sent %d message(s), want none", len(taken.email.sent))
	}

	free := emailHarness(t)
	free.kv.setErr = storeDown
	w = free.post("/identifiers", addBody("email", "taken@example.com"))
	if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
		t.Errorf("the free address answered %d %s, want 503 unavailable", w.Code, w.Body)
	}
}

// TestAVerifyAfterATakenAddHasNoRecord holds that a taken address receives no
// code, so a verify has no record and answers the same login-failed a wrong code
// gets.
func TestAVerifyAfterATakenAddHasNoRecord(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["taken@example.com"] = "someone-else"
	if w := h.post("/identifiers", addBody("email", "taken@example.com")); w.Code != http.StatusConflict {
		t.Fatalf("the add answered %d %s, want 409", w.Code, w.Body)
	}
	w := h.post("/identifiers/verify", verifyBody("000000"))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "login-failed" {
		t.Fatalf("the verify answered %d %s, want 401 login-failed", w.Code, w.Body)
	}
}

// TestAnAddressThisAccountHoldsIsSentTo holds the other side of the lookup: an
// address the asking account itself carries is a re-add and sends normally.
func TestAnAddressThisAccountHoldsIsSentTo(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = account

	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusOK || len(h.email.sent) != 1 {
		t.Fatalf("the add answered %d with %d send(s), want 200 and one send",
			w.Code, len(h.email.sent))
	}
}

// TestAnAddressLookupThatFailsIsAnOutage holds the failure direction: a table
// that cannot be read is a 503 and no send, never "nobody holds it".
func TestAnAddressLookupThatFailsIsAnOutage(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookupErr = errors.New("the identifier table is unavailable")

	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
		t.Fatalf("the add answered %d %s, want 503 unavailable", w.Code, w.Body)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("the address was sent %d message(s) over an unreadable table", len(h.email.sent))
	}
}

// TestTheAddressHourlyBudgetStopsASendInTheHour holds the per-address hourly
// bound on its own: with every other budget out of the way, the address admits
// its limit and the next ask in the same hour is a 429 carrying Retry-After.
func TestTheAddressHourlyBudgetStopsASendInTheHour(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 2, budgetAddressDaily: 200, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
	})
	for i := 0; i < 2; i++ {
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("send %d answered %d %s, want 200", i+1, w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
	}
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the third send answered %d %s, want 429 throttled", w.Code, w.Body)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("the 429 carries no Retry-After")
	}
	if len(h.email.sent) != 2 {
		t.Errorf("the address was sent %d message(s), want its limit of 2", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-address:") {
		t.Error("no address budget key was written")
	}
}

// TestTheAddressDailyBudgetStopsASendInTheDay holds the daily bound: the hourly
// one is wide open, so the refusal is the day's.
func TestTheAddressDailyBudgetStopsASendInTheDay(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 2, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
	})
	for i := 0; i < 2; i++ {
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("send %d answered %d %s, want 200", i+1, w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
	}
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the third send answered %d %s, want 429 throttled", w.Code, w.Body)
	}
	if len(h.email.sent) != 2 {
		t.Errorf("the address was sent %d message(s), want its daily limit of 2", len(h.email.sent))
	}
}

// TestTheAccountBudgetStopsAnotherAddress holds the account bound: one account
// may ask for a limited number of codes an hour whatever addresses it names, so
// a second address is refused once the account has spent its allowance.
func TestTheAccountBudgetStopsAnotherAddress(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200, budgetAccountHourly: 1, budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first address answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	w := h.post("/identifiers", addBody("email", "two@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second address answered %d %s, want the account's 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the account sent %d message(s), want its allowance of 1", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-account:") {
		t.Error("no account budget key was written")
	}
}

// TestTheGlobalBudgetStopsAnotherAccount holds the deployment-wide bound: it is
// shared across accounts and addresses, so a second account naming a fresh
// address is refused once the deployment has spent its hourly total.
func TestTheGlobalBudgetStopsAnotherAccount(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200, budgetAccountHourly: 60, budgetGlobalHourly: 1,
	})
	if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
	}
	h.as(authntest.WithAccount("acct-2"), authntest.WithSession("sess-2"))
	w := h.post("/identifiers", addBody("email", "two@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("another account answered %d %s, want the deployment's 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the deployment sent %d message(s), want its total of 1", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-global:") {
		t.Error("no deployment-wide budget key was written")
	}
}

// TestABudgetThatCannotBeCountedIsAnOutage holds the failure direction: the
// budgets are the only bound on messages to an address, so an increment that
// fails answers 503 and sends nothing, and the resend claim it took is given
// back.
func TestABudgetThatCannotBeCountedIsAnOutage(t *testing.T) {
	h := emailHarness(t)
	h.kv.incrErr = errors.New("the counter store is unavailable")

	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
		t.Fatalf("the add answered %d %s, want 503 unavailable", w.Code, w.Body)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("the address was sent %d message(s) over an uncountable budget", len(h.email.sent))
	}
	if hasKey(h, "authn:email-resend:") {
		t.Error("the refused add kept the resend claim it took")
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Error("the refused add kept a pending record")
	}
}

// TestATakenAddressSpendsNoBudget holds that the taken check happens before any
// counter is touched: an address another account holds is not charged to the
// address, the account or the deployment.
func TestATakenAddressSpendsNoBudget(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = "someone-else"
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusConflict {
		t.Fatalf("the add answered %d %s, want 409", w.Code, w.Body)
	}

	for _, prefix := range []string{
		"authn:email-address:", "authn:email-account:", "authn:email-global:",
	} {
		if hasKey(h, prefix) {
			t.Errorf("a taken address spent a %s budget", prefix)
		}
	}
}

// TestEachBudgetStartsFromZeroInANewWindow holds the bucket boundary of each of
// the four budgets: a count past its limit is refused inside the window, and the
// first send of the next window is admitted because the key is a new one and the
// count starts at zero.
func TestEachBudgetStartsFromZeroInANewWindow(t *testing.T) {
	t.Run("per-address hourly", func(t *testing.T) {
		h := budgetHarness(t, map[string]int{
			budgetAddressHourly: 1, budgetAddressDaily: 200, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
		})
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
		}
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
			t.Fatalf("the second send answered %d %s, want the hour's 429", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(time.Hour)
		h.reauthenticate()
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the send in the next hour answered %d %s, want 200", w.Code, w.Body)
		}
	})

	t.Run("per-address daily", func(t *testing.T) {
		h := budgetHarness(t, map[string]int{
			budgetAddressHourly: 60, budgetAddressDaily: 1, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
		})
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
			t.Fatalf("the second send answered %d %s, want the day's 429", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(24 * time.Hour)
		h.reauthenticate()
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the send on the next day answered %d %s, want 200", w.Code, w.Body)
		}
	})

	t.Run("per-account hourly", func(t *testing.T) {
		h := budgetHarness(t, map[string]int{
			budgetAddressHourly: 60, budgetAddressDaily: 200, budgetAccountHourly: 1, budgetGlobalHourly: 1000,
		})
		if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
		if w := h.post("/identifiers", addBody("email", "two@example.com")); w.Code != http.StatusTooManyRequests {
			t.Fatalf("the second address answered %d %s, want the account's 429", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(time.Hour)
		h.reauthenticate()
		if w := h.post("/identifiers", addBody("email", "three@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the send in the next hour answered %d %s, want 200", w.Code, w.Body)
		}
	})

	t.Run("global hourly", func(t *testing.T) {
		h := budgetHarness(t, map[string]int{
			budgetAddressHourly: 60, budgetAddressDaily: 200, budgetAccountHourly: 60, budgetGlobalHourly: 1,
		})
		if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
		h.as(authntest.WithAccount("acct-2"), authntest.WithSession("sess-2"))
		if w := h.post("/identifiers", addBody("email", "two@example.com")); w.Code != http.StatusTooManyRequests {
			t.Fatalf("the second account answered %d %s, want the deployment's 429", w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(time.Hour)
		h.reauthenticate(authntest.WithAccount("acct-3"), authntest.WithSession("sess-3"))
		if w := h.post("/identifiers", addBody("email", "three@example.com")); w.Code != http.StatusOK {
			t.Fatalf("the send in the next hour answered %d %s, want 200", w.Code, w.Body)
		}
	})
}

// TestTheDailyBudgetStatesTheWaitToTheNextDay holds the 24-hour bucket's landing
// point: the 429 of the daily budget states the seconds left before midnight,
// not the hour the hourly budget would state.
func TestTheDailyBudgetStatesTheWaitToTheNextDay(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 1, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first send answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the second send answered %d %s, want the day's 429", w.Code, w.Body)
	}
	// The clock reads 12:02 UTC, so the day's window has 11h58m left.
	if got, want := w.Header().Get("Retry-After"), "43080"; got != want {
		t.Errorf("Retry-After is %q, want %q: the wait to the next day", got, want)
	}
}
