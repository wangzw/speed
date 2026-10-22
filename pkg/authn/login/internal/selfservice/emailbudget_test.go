package selfservice_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// pendingAnswer is the wire shape of the email branch's answer, read back so two
// answers can be compared field by field.
type pendingAnswer struct {
	State       string `json:"state"`
	ExpiresIn   int    `json:"expires_in"`
	ResendAfter int    `json:"resend_after"`
}

// pendingOf reads the answer of an add as the wire shape.
func pendingOf(t *testing.T, body []byte) pendingAnswer {
	t.Helper()
	var answer pendingAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, body)
	}
	return answer
}

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

// TestAnAddressAnotherAccountHoldsIsNotSentTo holds the suppression: an address
// another account already carries gets no message, no budget charge and no
// pending record, and the answer is the pending answer a real send gives. It
// does hold the address's resend window, which is what keeps a repeat
// indistinguishable from a repeat against a free address.
func TestAnAddressAnotherAccountHoldsIsNotSentTo(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = "someone-else"

	w := h.post("/identifiers", addBody("email", "Ada@Example.com"))
	if w.Code != http.StatusOK {
		t.Fatalf("the add answered %d %s, want 200", w.Code, w.Body)
	}
	answer := pendingOf(t, w.Body.Bytes())
	if answer.State != "pending" || answer.ExpiresIn <= 0 || answer.ResendAfter <= 0 {
		t.Errorf("the answer is %+v, want a pending answer with both durations", answer)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("a held address was sent %d message(s), want none", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-resend:") {
		t.Error("a held address did not hold its resend window")
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Error("a held address kept a pending record")
	}
	if got := h.accounts.looked; len(got) != 1 || got[0] != "ada@example.com" {
		t.Errorf("Lookup was asked for %v, want the one normalized address", got)
	}
}

// TestAPairOfAsksToAHeldAddressAndAFreeOneAnswerAlike holds the
// indistinguishability across a repeat: the held and the free address answer the
// same pending shape on the first ask and the same 429 throttled on the second
// inside the window. Only the free one sends.
func TestAPairOfAsksToAHeldAddressAndAFreeOneAnswerAlike(t *testing.T) {
	held := emailHarness(t)
	held.accounts.lookup["taken@example.com"] = "someone-else"
	heldFirst := held.post("/identifiers", addBody("email", "Taken@Example.com"))
	heldSecond := held.post("/identifiers", addBody("email", "Taken@Example.com"))

	free := emailHarness(t)
	freeFirst := free.post("/identifiers", addBody("email", "Taken@Example.com"))
	freeSecond := free.post("/identifiers", addBody("email", "Taken@Example.com"))

	if heldFirst.Code != http.StatusOK || freeFirst.Code != http.StatusOK {
		t.Fatalf("the first asks answered %d and %d, want 200 and 200", heldFirst.Code, freeFirst.Code)
	}
	if got, want := pendingOf(t, heldFirst.Body.Bytes()), pendingOf(t, freeFirst.Body.Bytes()); got != want {
		t.Errorf("the first answers are %+v and %+v, want the same shape", got, want)
	}
	for _, second := range []*httptest.ResponseRecorder{heldSecond, freeSecond} {
		if second.Code != http.StatusTooManyRequests || problemType(t, second) != "throttled" {
			t.Fatalf("a repeat answered %d %s, want 429 throttled", second.Code, second.Body)
		}
	}
	if got, want := heldSecond.Header().Get("Retry-After"), freeSecond.Header().Get("Retry-After"); got != want {
		t.Errorf("the repeats state %q and %q to retry, want the same", got, want)
	}
	if len(held.email.sent) != 0 || len(free.email.sent) != 1 {
		t.Errorf("the held address was sent %d message(s) and the free one %d, want 0 and 1",
			len(held.email.sent), len(free.email.sent))
	}
}

// TestAHeldAddressAndAFreeOneFailAlikeWhenTheStoreCannotBeClaimed holds the other
// failure direction: a store that cannot take the resend claim answers both
// addresses 503 unavailable, so the store's state tells a client nothing about
// which address is taken.
func TestAHeldAddressAndAFreeOneFailAlikeWhenTheStoreCannotBeClaimed(t *testing.T) {
	storeDown := errors.New("the store is unavailable")
	held := emailHarness(t)
	held.accounts.lookup["taken@example.com"] = "someone-else"
	held.kv.setErr = storeDown
	free := emailHarness(t)
	free.kv.setErr = storeDown

	for name, h := range map[string]*harness{"held": held, "free": free} {
		w := h.post("/identifiers", addBody("email", "taken@example.com"))
		if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
			t.Errorf("%s: the add answered %d %s, want 503 unavailable", name, w.Code, w.Body)
		}
		if len(h.email.sent) != 0 {
			t.Errorf("%s: the address was sent %d message(s), want none", name, len(h.email.sent))
		}
	}
}

// TestAHeldAddressAndAFreeOneAnswerTheSameShape holds the indistinguishability:
// the answers to a held address and to a free one carry the same state and the
// same two durations, so a client cannot tell whether an address is taken.
func TestAHeldAddressAndAFreeOneAnswerTheSameShape(t *testing.T) {
	held := emailHarness(t)
	held.accounts.lookup["taken@example.com"] = "someone-else"
	heldAnswer := pendingOf(t, held.post("/identifiers", addBody("email", "taken@example.com")).Body.Bytes())

	free := emailHarness(t)
	freeAnswer := pendingOf(t, free.post("/identifiers", addBody("email", "taken@example.com")).Body.Bytes())

	if heldAnswer != freeAnswer {
		t.Errorf("the held address answered %+v, the free one %+v; want the same shape",
			heldAnswer, freeAnswer)
	}
	if len(held.email.sent) != 0 || len(free.email.sent) != 1 {
		t.Errorf("the held address was sent %d message(s) and the free one %d, want 0 and 1",
			len(held.email.sent), len(free.email.sent))
	}
}

// TestAVerifyAfterAHeldSendLooksLikeAWrongCode holds the second half of the
// indistinguishability: a held address never received a code, so its verify
// has no record, and the answer must be the same login-failed a free address
// gives for a wrong code.
func TestAVerifyAfterAHeldSendLooksLikeAWrongCode(t *testing.T) {
	held := emailHarness(t)
	held.accounts.lookup["taken@example.com"] = "someone-else"
	held.post("/identifiers", addBody("email", "taken@example.com"))
	heldVerify := held.post("/identifiers/verify", verifyBody("000000"))

	free := emailHarness(t)
	free.post("/identifiers", addBody("email", "taken@example.com"))
	wrong := "000000"
	if len(free.email.sent) == 1 && free.email.sent[0].code == wrong {
		wrong = "111111"
	}
	freeVerify := free.post("/identifiers/verify", verifyBody(wrong))

	if heldVerify.Code != http.StatusUnauthorized || problemType(t, heldVerify) != "login-failed" {
		t.Fatalf("the held address's verify answered %d %s, want 401 login-failed",
			heldVerify.Code, heldVerify.Body)
	}
	if heldVerify.Code != freeVerify.Code || problemType(t, heldVerify) != problemType(t, freeVerify) {
		t.Errorf("the held address answered %d %s, the free one %d %s; want the same refusal",
			heldVerify.Code, heldVerify.Body, freeVerify.Code, freeVerify.Body)
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

// TestASuppressedSendSpendsNoBudget holds that suppression happens before any
// counter is touched: an address another account holds is not charged to the
// address, the account or the deployment.
func TestASuppressedSendSpendsNoBudget(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = "someone-else"
	h.post("/identifiers", addBody("email", "ada@example.com"))

	for _, prefix := range []string{
		"authn:email-address:", "authn:email-account:", "authn:email-global:",
	} {
		if hasKey(h, prefix) {
			t.Errorf("a suppressed send spent a %s budget", prefix)
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
