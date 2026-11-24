package selfservice_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
)

type plainEmail struct{ err error }

func (p *plainEmail) SendCode(context.Context, string, string, time.Duration, string) error {
	return p.err
}

type steppingEmail struct {
	send func() error
}

func (s *steppingEmail) SendCode(context.Context, string, string, time.Duration, string) error {
	return s.send()
}

func (s *steppingEmail) CertifiesNotSent(error) bool { return true }

func budgetTotal(h *harness, prefix string) float64 {
	h.kv.mu.Lock()
	defer h.kv.mu.Unlock()
	var total float64
	for key, raw := range h.kv.values {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		v, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			continue
		}
		total += v
	}
	return total
}

// TestTheAccountDailyBudgetStopsAnotherAddress holds that one account may ask for a daily number of codes whatever the address.
func TestTheAccountDailyBudgetStopsAnotherAddress(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first address answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	w := h.post("/identifiers", addBody("email", "two@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second address answered %d %s, want the account-day 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the account sent %d message(s), want its daily allowance of 1", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-account:") {
		t.Error("no account budget key was written")
	}
}

// TestTheSourceHourlyBudgetStopsADifferentAddress holds that one source may ask for an hourly number of codes even when rotating the destination.
func TestTheSourceHourlyBudgetStopsADifferentAddress(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 1, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	const source = "203.0.113.7:4567"
	if w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "one@example.com"), remote: source,
	}); w.Code != http.StatusOK {
		t.Fatalf("the first address answered %d %s, want 200", w.Code, w.Body)
	}
	w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "two@example.com"), remote: source,
	})
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second address answered %d %s, want the source-hour 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the source sent %d message(s), want its hourly allowance of 1", len(h.email.sent))
	}
	if !hasKey(h, "authn:email-source:") {
		t.Error("no source budget key was written")
	}
}

// TestTheSourceDailyBudgetStopsADifferentAddress holds that the refusal inside a UTC day is the day's budget.
func TestTheSourceDailyBudgetStopsADifferentAddress(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 10000, budgetSourceDaily: 1,
		budgetGlobalHourly: 1000,
	})
	const source = "203.0.113.7:4567"
	if w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "one@example.com"), remote: source,
	}); w.Code != http.StatusOK {
		t.Fatalf("the first address answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "two@example.com"), remote: source,
	})
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second address answered %d %s, want the source-day 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the source sent %d message(s), want its daily allowance of 1", len(h.email.sent))
	}
}

// TestDifferentSourcesDoNotShareTheirBudget holds that one spent source does not refuse another.
func TestDifferentSourcesDoNotShareTheirBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 1, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "one@example.com"), remote: "203.0.113.7:4567",
	}); w.Code != http.StatusOK {
		t.Fatalf("the first source answered %d %s, want 200", w.Code, w.Body)
	}
	if w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "two@example.com"), remote: "203.0.113.8:4567",
	}); w.Code != http.StatusOK {
		t.Fatalf("the second source answered %d %s, want its own 200", w.Code, w.Body)
	}
	if len(h.email.sent) != 2 {
		t.Errorf("the deployment sent %d message(s), want one from each source", len(h.email.sent))
	}
}

// TestTheEmailRouteKeepsTheClientAddress holds that the request's address
// reaches the source budget.
func TestTheEmailRouteKeepsTheClientAddress(t *testing.T) {
	h := emailHarness(t)
	const remote = "203.0.113.7"
	if w := h.do(request{
		method: http.MethodPost, path: "/identifiers",
		body: addBody("email", "ada@example.com"), remote: remote + ":4567",
	}); w.Code != http.StatusOK {
		t.Fatalf("the add answered %d %s, want 200", w.Code, w.Body)
	}
	if !hasKey(h, "authn:email-source:") {
		t.Error("the send wrote no source budget key")
	}
	for _, key := range h.kv.keys() {
		if strings.Contains(key, remote) {
			t.Errorf("the store carries the source address in the clear: %s", key)
		}
	}
}

// TestAPlusAliasSharesTheAddressBudget holds that the +suffix is stripped for every domain, so one mailbox has one budget.
func TestAPlusAliasSharesTheAddressBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "ada+one@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first writing answered %d %s, want 200", w.Code, w.Body)
	}
	if got := h.email.sent[0].to; got != "ada+one@example.com" {
		t.Errorf("the message went to %q, want the address as written", got)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	w := h.post("/identifiers", addBody("email", "ada+two@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second writing answered %d %s, want the shared address 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the mailbox was sent %d message(s), want one budget's worth", len(h.email.sent))
	}
}

// TestGmailDotAliasesShareTheAddressBudget holds the Gmail dot rule: dots in the
// local part of gmail.com fold away, so two dotted writings are one mailbox.
func TestGmailDotAliasesShareTheAddressBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "ada.one@gmail.com")); w.Code != http.StatusOK {
		t.Fatalf("the dotted writing answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	w := h.post("/identifiers", addBody("email", "adaone@gmail.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the dotless writing answered %d %s, want the shared address 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the mailbox was sent %d message(s), want one budget's worth", len(h.email.sent))
	}
}

// TestGooglemailAndGmailShareTheAddressBudget holds that the two Gmail domains
// name one mailbox for the budget.
func TestGooglemailAndGmailShareTheAddressBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "ada@gmail.com")); w.Code != http.StatusOK {
		t.Fatalf("the gmail.com writing answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	w := h.post("/identifiers", addBody("email", "ada@googlemail.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the googlemail.com writing answered %d %s, want the shared address 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the mailbox was sent %d message(s), want one budget's worth", len(h.email.sent))
	}
}

// TestDotsAreFoldedOnlyForGmail holds that at another provider a dot makes a different budget.
func TestDotsAreFoldedOnlyForGmail(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	if w := h.post("/identifiers", addBody("email", "a.b@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the dotted writing answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "ab@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the dotless writing answered %d %s, want its own 200", w.Code, w.Body)
	}
	if len(h.email.sent) != 2 {
		t.Errorf("the provider was sent %d message(s), want one per address", len(h.email.sent))
	}
}

// TestAFailedSendRefundsItsBudget holds that a certified non-send gives back every count the request took.
func TestAFailedSendRefundsItsBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	h.email.err = errors.New("the carrier refused the message")
	h.email.certain = true
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the failed send answered %d %s, want 503", w.Code, w.Body)
	}
	if got := budgetTotal(h, "authn:email-address:"); got != 0 {
		t.Errorf("the failed send left %v count(s) on the address, want none", got)
	}

	h.email.err = nil
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the retry answered %d %s, want the refunded budget's 200", w.Code, w.Body)
	}
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the send past the limit answered %d %s, want the address 429", w.Code, w.Body)
	}
}

// TestAnUncertainSendKeepsItsBudget holds that an uncertain failure keeps the budget.
func TestAnUncertainSendKeepsItsBudget(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	h.email.err = errors.New("the carrier call timed out")
	h.email.certain = false
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the uncertain send answered %d %s, want 503", w.Code, w.Body)
	}
	if got := budgetTotal(h, "authn:email-address:"); got != 2 {
		t.Errorf("the uncertain send left %v count(s), want its hourly and daily count kept", got)
	}

	h.email.err = nil
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the retry after an uncertain send answered %d %s, want the kept budget's 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the channel was handed %d message(s), want only the uncertain attempt", len(h.email.sent))
	}
}

// TestACustomSendersFailureKeepsItsBudget holds that an unclassified failure is treated as uncertain and keeps the budget.
func TestACustomSendersFailureKeepsItsBudget(t *testing.T) {
	h := newHarness(t, func(c *config) {
		c.email = true
		c.mutateDeps = func(d *selfservice.Deps) {
			d.EmailSender = &plainEmail{err: errors.New("the call did not complete")}
		}
	})
	h.settings.numbers = map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	}
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the custom channel's failure answered %d %s, want 503", w.Code, w.Body)
	}
	if got := budgetTotal(h, "authn:email-address:"); got != 2 {
		t.Errorf("the custom channel's failure left %v count(s), want its hourly and daily count kept", got)
	}
}

// TestARefusalReleasesWhatItAlreadyReserved holds that every count including the refusing one is given back.
func TestARefusalReleasesWhatItAlreadyReserved(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1,
	})
	if w := h.post("/identifiers", addBody("email", "one@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first address answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "two@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the refused address answered %d %s, want the deployment's 429", w.Code, w.Body)
	}

	if got := budgetTotal(h, "authn:email-address:"); got != 2 {
		t.Errorf("the address budgets hold %v count(s), want the accepted send's 2", got)
	}
	if got := budgetTotal(h, "authn:email-account:"); got != 2 {
		t.Errorf("the account budgets hold %v count(s), want the accepted send's 2", got)
	}
	if got := budgetTotal(h, "authn:email-global:"); got != 1 {
		t.Errorf("the deployment budget holds %v count(s), want the accepted send's 1", got)
	}
}

// TestARefundDoesNotRecreateAnExpiredWindow holds that a refund reads and swaps, never increments negatively.
func TestARefundDoesNotRecreateAnExpiredWindow(t *testing.T) {
	var h *harness
	sender := &steppingEmail{}
	sender.send = func() error {
		h.clockNow = h.clockNow.Add(25 * time.Hour)
		return errors.New("the carrier refused the message")
	}
	h = newHarness(t, func(c *config) {
		c.email = true
		c.mutateDeps = func(d *selfservice.Deps) { d.EmailSender = sender }
	})
	h.settings.numbers = map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 1,
		budgetAccountHourly: 1000, budgetAccountDaily: 1000,
		budgetSourceHourly: 1000, budgetSourceDaily: 1000,
		budgetGlobalHourly: 1000,
	}

	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the send past the window answered %d %s, want 503", w.Code, w.Body)
	}
	for _, prefix := range []string{"authn:email-address:", "authn:email-account:", "authn:email-global:"} {
		if hasKey(h, prefix) {
			t.Errorf("the refund recreated an expired %s key", strings.TrimSuffix(prefix, ":"))
		}
		if got := budgetTotal(h, prefix); got != 0 {
			t.Errorf("the expired %s window holds %v count(s), want none", strings.TrimSuffix(prefix, ":"), got)
		}
	}

	sender.send = func() error { return nil }
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first send of the new window answered %d %s, want 200", w.Code, w.Body)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the second send of the new window answered %d %s, want the address 429", w.Code, w.Body)
	}
}

// TestARefundThatCannotBeWrittenStaysSpent holds that a failed refund leaves the count spent.
func TestARefundThatCannotBeWrittenStaysSpent(t *testing.T) {
	h := budgetHarness(t, map[string]int{
		budgetAddressHourly: 1, budgetAddressDaily: 200,
		budgetAccountHourly: 60, budgetAccountDaily: 1000,
		budgetSourceHourly: 30, budgetSourceDaily: 100,
		budgetGlobalHourly: 1000,
	})
	h.email.err = errors.New("the carrier refused the message")
	h.email.certain = true
	h.kv.casErr = errors.New("the store is unavailable")

	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the failed send answered %d %s, want 503", w.Code, w.Body)
	}
	if got := budgetTotal(h, "authn:email-address:"); got != 2 {
		t.Errorf("the address budgets hold %v count(s), want the failed send's hourly and daily count kept", got)
	}

	h.kv.casErr = nil
	h.email.err = nil
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the retry answered %d %s, want the kept budget's 429", w.Code, w.Body)
	}
}
