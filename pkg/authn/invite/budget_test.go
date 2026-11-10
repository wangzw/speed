package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// throttleOf reads the throttled error an issuance or probe answered with.
func throttleOf(t *testing.T, err error) *authn.ThrottledError {
	t.Helper()
	var throttled *authn.ThrottledError
	if !errors.Is(err, authn.ErrThrottled) || !errors.As(err, &throttled) {
		t.Fatalf("the call answered %v, want a throttled error", err)
	}
	if throttled.RetryAfter <= 0 {
		t.Errorf("the throttled error carries RetryAfter %s, want a positive wait", throttled.RetryAfter)
	}
	return throttled
}

// issueAs issues an invitation over an identity so the issuer budget is spent.
func issueAs(h *harness, account authn.AccountID, email string) (Issued, error) {
	ctx := authntest.Context(h.t, context.Background(), authntest.WithAccount(account))
	return h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: email})
}

// TestThePerAddressIssueBudgetAnswersThrottled holds the first send budget: one
// invited address receives no more than its window allows, a resend counts like
// any other issuance, and the address is folded to its canonical inbox form so a
// plus suffix cannot multiply the allowance.
func TestThePerAddressIssueBudgetAnswersThrottled(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 2)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 100)

	for i, address := range []string{"alice+one@example.com", "Alice+two@example.com"} {
		if _, err := issueAs(h, adminID, address); err != nil {
			t.Fatalf("issuance %d to the same inbox gave %v, want it let through", i+1, err)
		}
	}
	if _, err := issueAs(h, adminID, "alice@example.com"); err == nil {
		t.Fatal("a third issuance to the same canonical inbox was let through, want throttled")
	} else {
		throttleOf(t, err)
	}
	// Another address is a different budget and still issues.
	if _, err := issueAs(h, adminID, "bob@example.com"); err != nil {
		t.Errorf("an issuance to another address gave %v, want it let through", err)
	}
}

// TestThePerIssuerIssueBudgetAnswersThrottled holds the second send budget: one
// issuing account sends no more than its window allows, while another operator
// has its own budget.
func TestThePerIssuerIssueBudgetAnswersThrottled(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 100)
	h.store.set(issueIssuerPath, 2)
	h.store.set(issueGlobalPath, 100)

	for i, address := range []string{"one@example.com", "two@example.com"} {
		if _, err := issueAs(h, adminID, address); err != nil {
			t.Fatalf("issuance %d from the operator gave %v, want it let through", i+1, err)
		}
	}
	if _, err := issueAs(h, adminID, "three@example.com"); err == nil {
		t.Fatal("a third issuance from the same operator was let through, want throttled")
	} else {
		throttleOf(t, err)
	}
	if _, err := issueAs(h, "acct-other-operator", "four@example.com"); err != nil {
		t.Errorf("another operator's issuance gave %v, want its own budget let it through", err)
	}
}

// TestTheGlobalIssueBudgetAnswersThrottled holds the third send budget: the
// deployment's ceiling applies across addresses and operators alike.
func TestTheGlobalIssueBudgetAnswersThrottled(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 100)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 2)

	for i, address := range []string{"one@example.com", "two@example.com"} {
		if _, err := issueAs(h, adminID, address); err != nil {
			t.Fatalf("issuance %d gave %v, want it let through", i+1, err)
		}
	}
	if _, err := issueAs(h, "acct-other-operator", "three@example.com"); err == nil {
		t.Fatal("an issuance past the deployment budget was let through, want throttled")
	} else {
		throttleOf(t, err)
	}
}

// TestTheIssueRouteAnswers429WithRetryAfter holds the platform-standard refusal
// on the HTTP face: a spent send budget is a 429 problem whose type is the
// capability's throttled code and whose Retry-After names the wait.
func TestTheIssueRouteAnswers429WithRetryAfter(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 1)
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("the first issuance answered %d, want 201: %s", w.Code, w.Body.String())
	}
	w = h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the second issuance answered %d, want 429: %s", w.Code, w.Body.String())
	}
	if retry := w.Header().Get("Retry-After"); retry == "" || retry == "0" {
		t.Errorf("Retry-After = %q, want the seconds until the window opens", retry)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the 429 body %q is not JSON: %v", w.Body.String(), err)
	}
	if got, _ := body["type"].(string); got != "urn:speed:authn:throttled" {
		t.Errorf("problem type = %q, want urn:speed:authn:throttled", got)
	}
}

// TestTheAcceptanceProbeBudgetAnswersThrottledWithoutRevealingTheToken holds the
// probe budget on the public acceptance read: one token may be presented only so
// many times, and the refusal is the same 429 whether the token exists or not,
// because the budget is charged before the token is looked up.
func TestTheAcceptanceProbeBudgetAnswersThrottledWithoutRevealingTheToken(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 1)
	h.store.set(probeClientPath, 100)
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	path := "/api/v1/auth/invitations/"

	// A real token, presented twice: the first is a view, the second is the
	// probe budget.
	if w := h.request(http.MethodGet, path+issued.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("the first view of a real token answered %d, want 200: %s", w.Code, w.Body.String())
	}
	real := h.request(http.MethodGet, path+issued.Token, nil)
	if real.Code != http.StatusTooManyRequests {
		t.Fatalf("the second view of a real token answered %d, want 429", real.Code)
	}

	// An unknown token, presented twice: the first is a bare 404, the second is
	// the same probe budget.
	const unknown = "spd_inv_thisTokenDoesNotExist"
	if w := h.request(http.MethodGet, path+unknown, nil); w.Code != http.StatusNotFound {
		t.Fatalf("the first view of an unknown token answered %d, want 404", w.Code)
	}
	fake := h.request(http.MethodGet, path+unknown, nil)
	if fake.Code != http.StatusTooManyRequests {
		t.Fatalf("the second view of an unknown token answered %d, want 429: a spent budget must not "+
			"tell a caller whether the token exists", fake.Code)
	}

	// The two refusals are indistinguishable.
	if real.Body.String() != fake.Body.String() {
		t.Errorf("the throttled answer to a real token is %q and to an unknown token %q; they must read alike",
			real.Body.String(), fake.Body.String())
	}
	if real.Header().Get("Retry-After") != fake.Header().Get("Retry-After") {
		t.Errorf("Retry-After differs between a real and an unknown token: %q and %q",
			real.Header().Get("Retry-After"), fake.Header().Get("Retry-After"))
	}
}

// TestThePerClientProbeBudgetAnswersThrottled holds the coarse per-client bound:
// a source that walks distinct tokens is stopped by the address budget even
// though no single token's budget is spent.
func TestThePerClientProbeBudgetAnswersThrottled(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 100)
	h.store.set(probeClientPath, 2)
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	path := "/api/v1/auth/invitations/"
	for i, token := range []string{"spd_inv_firstUnknown", "spd_inv_secondUnknown"} {
		if w := h.request(http.MethodGet, path+token, nil); w.Code != http.StatusNotFound {
			t.Fatalf("probe %d with a distinct unknown token answered %d, want 404", i+1, w.Code)
		}
	}
	w := h.request(http.MethodGet, path+issued.Token, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("a third probe from the same client answered %d, want 429 from the client bound: %s",
			w.Code, w.Body.String())
	}
}

// TestTheBudgetsFallBackToTheirDefaultsWhenTheStoreFails holds the failure
// direction: a settings read that fails leaves the declared defaults in force,
// so a security budget is never silently disabled by a bad row.
func TestTheBudgetsFallBackToTheirDefaultsWhenTheStoreFails(t *testing.T) {
	h := newHarness(t)
	h.store.failOn(issueAddressPath, errors.New("the settings store is down"))

	got := h.p.issueBudget()
	if got.address != DefaultIssueAddressLimit || got.issuer != DefaultIssueIssuerLimit ||
		got.global != DefaultIssueGlobalLimit || got.window != DefaultIssueWindow {
		t.Errorf("a failed read gave %+v, want the declared defaults", got)
	}
}

// TestAThrottledIssuanceWritesNoRow holds that the budget is charged before the
// action: an over-budget issuance leaves no invitation row behind.
func TestAThrottledIssuanceWritesNoRow(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 1)
	if _, err := issueAs(h, adminID, "alice@example.com"); err != nil {
		t.Fatalf("the first issuance gave %v", err)
	}
	before := h.rowCount()
	if _, err := issueAs(h, adminID, "alice@example.com"); err == nil {
		t.Fatal("the second issuance was let through")
	}
	if got := h.rowCount(); got != before {
		t.Errorf("the throttled issuance changed the row count from %d to %d", before, got)
	}
}

// TestTheSettingsSchemaDeclaresTheBudgetKeys holds the declaration the budgets
// are read through. A key the schema does not declare is a schema a host cannot
// tune, and a read of it would cover no declaration.
func TestTheSettingsSchemaDeclaresTheBudgetKeys(t *testing.T) {
	s := settingsSchema()
	for _, item := range []string{
		issueWindowItem, issueAddressItem, issueIssuerItem, issueGlobalItem,
		probeWindowItem, probeTokenItem, probeClientItem,
	} {
		if _, ok := s.Items[item]; !ok {
			t.Errorf("the settings schema does not declare %q", item)
		}
	}
}

// TestTheDeclaredBudgetDefaultsAreInDomain holds that the defaults shipped with
// the module are values the budgets will actually act on.
func TestTheDeclaredBudgetDefaultsAreInDomain(t *testing.T) {
	if !windowInDomain(DefaultIssueWindow) || !windowInDomain(DefaultProbeWindow) {
		t.Errorf("a declared window (%s, %s) is outside its domain",
			DefaultIssueWindow, DefaultProbeWindow)
	}
	for name, limit := range map[string]int{
		"issue address": DefaultIssueAddressLimit,
		"issue issuer":  DefaultIssueIssuerLimit,
		"issue global":  DefaultIssueGlobalLimit,
		"probe token":   DefaultProbeTokenLimit,
		"probe client":  DefaultProbeClientLimit,
	} {
		if !limitInDomain(limit) {
			t.Errorf("the declared %s limit %d is outside its domain", name, limit)
		}
	}
}

// TestTheProbeClientKeyCountsAnIPv6AddressByItsSlashSixtyFour holds the coarse
// client bound's key: one host's /64 shares one bucket, a different /64 is a
// different bucket, and an IPv4 address is counted as itself.
func TestTheProbeClientKeyCountsAnIPv6AddressByItsSlashSixtyFour(t *testing.T) {
	a := clientSubject("2001:db8:1:2:aaaa::1")
	b := clientSubject("2001:db8:1:2:bbbb::2")
	if a != b {
		t.Errorf("two addresses of one /64 gave %q and %q, want one bucket", a, b)
	}
	if clientSubject("2001:db8:1:3::1") == a {
		t.Errorf("another /64 gave the same key %q", a)
	}
	if got := clientSubject("192.0.2.10"); got != "192.0.2.10" {
		t.Errorf("an IPv4 address gave %q, want itself", got)
	}
	if got := clientSubject("not-an-ip"); got != "not-an-ip" {
		t.Errorf("an unparsable client gave %q, want it as written", got)
	}
}

// TestTheBudgetKeysEscapeTheirSegments holds the key construction: a colon in a
// segment cannot build another budget's key, and one window's key is stable
// while the next window's differs.
func TestTheBudgetKeysEscapeTheirSegments(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	window := time.Hour
	a := budgetKey(budgetIssueAddress, "a:b", window, now)
	b := budgetKey(budgetIssueAddress, "a%3Ab", window, now)
	if a == b {
		t.Errorf("a colon and its escape build the same key %q", a)
	}
	if got := budgetKey(budgetIssueAddress, "", window, time.Unix(1_700_000_000, 0)); got !=
		budgetKey(budgetIssueAddress, "", window, now) {
		t.Errorf("the key is not stable for one window: %q", got)
	}
	if got := budgetKey(budgetIssueAddress, "", window, now.Add(window)); got ==
		budgetKey(budgetIssueAddress, "", window, now) {
		t.Errorf("two windows build the same key %q", got)
	}
}
