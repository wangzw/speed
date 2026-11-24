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

func issueAs(h *harness, account authn.AccountID, email string) (Issued, error) {
	ctx := authntest.Context(h.t, context.Background(), authntest.WithAccount(account))
	return h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: email})
}

// TestThePerAddressIssueBudgetAnswersThrottled checks that one address is throttled after its window's allowance, a resend included, and that the canonical inbox form is counted once.
func TestThePerAddressIssueBudgetAnswersThrottled(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 2)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 100)

	for i, address := range []string{"alice+one@example.com", "Alice+two@example.com"} {
		issued, err := issueAs(h, adminID, address)
		if err != nil {
			t.Fatalf("issuance %d to the same inbox gave %v, want it let through", i+1, err)
		}
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("revoking issuance %d: %v", i+1, err)
		}
	}
	if _, err := issueAs(h, adminID, "alice@example.com"); err == nil {
		t.Fatal("a third issuance to the same canonical inbox was let through, want throttled")
	} else {
		throttleOf(t, err)
	}

	if _, err := issueAs(h, adminID, "bob@example.com"); err != nil {
		t.Errorf("an issuance to another address gave %v, want it let through", err)
	}
}

// TestThePerIssuerIssueBudgetAnswersThrottled checks that one issuing account is throttled after its allowance while another operator has its own.
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

// TestTheGlobalIssueBudgetAnswersThrottled checks that the deployment's ceiling applies across addresses and operators.
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

// TestTheIssueRouteAnswers429WithRetryAfter checks that a spent send budget answers 429 with the throttled problem type and a Retry-After.
func TestTheIssueRouteAnswers429WithRetryAfter(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueIssuerPath, 1)
	h.store.set(issueAddressPath, 100)
	h.store.set(issueGlobalPath, 100)
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

// TestTheAcceptanceProbeBudgetAnswersThrottledWithoutRevealingTheToken checks that a token's probe budget answers the same 429 whether or not the token exists.
func TestTheAcceptanceProbeBudgetAnswersThrottledWithoutRevealingTheToken(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 1)
	h.store.set(probeClientPath, 100)
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	path := "/api/v1/auth/invitations/"

	if w := h.request(http.MethodGet, path+issued.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("the first view of a real token answered %d, want 200: %s", w.Code, w.Body.String())
	}
	real := h.request(http.MethodGet, path+issued.Token, nil)
	if real.Code != http.StatusTooManyRequests {
		t.Fatalf("the second view of a real token answered %d, want 429", real.Code)
	}

	const unknown = "spd_inv_thisTokenDoesNotExist"
	if w := h.request(http.MethodGet, path+unknown, nil); w.Code != http.StatusNotFound {
		t.Fatalf("the first view of an unknown token answered %d, want 404", w.Code)
	}
	fake := h.request(http.MethodGet, path+unknown, nil)
	if fake.Code != http.StatusTooManyRequests {
		t.Fatalf("the second view of an unknown token answered %d, want 429: a spent budget must not "+
			"tell a caller whether the token exists", fake.Code)
	}

	if real.Body.String() != fake.Body.String() {
		t.Errorf("the throttled answer to a real token is %q and to an unknown token %q; they must read alike",
			real.Body.String(), fake.Body.String())
	}
	if real.Header().Get("Retry-After") != fake.Header().Get("Retry-After") {
		t.Errorf("Retry-After differs between a real and an unknown token: %q and %q",
			real.Header().Get("Retry-After"), fake.Header().Get("Retry-After"))
	}
}

// TestThePerClientProbeBudgetAnswersThrottled checks that one client is throttled across distinct tokens by its own bound.
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

// TestTheBudgetsFallBackToTheirDefaultsWhenTheStoreFails checks that a failed settings read leaves the declared defaults in force.
func TestTheBudgetsFallBackToTheirDefaultsWhenTheStoreFails(t *testing.T) {
	h := newHarness(t)
	h.store.failOn(issueAddressPath, errors.New("the settings store is down"))

	got := h.p.issueBudget()
	if got.address != DefaultIssueAddressLimit || got.issuer != DefaultIssueIssuerLimit ||
		got.global != DefaultIssueGlobalLimit || got.window != DefaultIssueWindow {
		t.Errorf("a failed read gave %+v, want the declared defaults", got)
	}
}

// TestAThrottledIssuanceWritesNoRow checks that an over-budget issuance writes no invitation row.
func TestAThrottledIssuanceWritesNoRow(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 1)
	first, err := issueAs(h, adminID, "alice@example.com")
	if err != nil {
		t.Fatalf("the first issuance gave %v", err)
	}
	if revokeErr := h.p.Revoke(context.Background(), tenant, first.Invitation.ID); revokeErr != nil {
		t.Fatalf("revoking the first issuance: %v", revokeErr)
	}
	before := h.rowCount()
	_, err = issueAs(h, adminID, "alice@example.com")
	if err == nil {
		t.Fatal("the second issuance was let through")
	}
	throttleOf(t, err)
	if got := h.rowCount(); got != before {
		t.Errorf("the throttled issuance changed the row count from %d to %d", before, got)
	}
}

// TestAnAttemptTheChecksRefuseSpendsOnlyTheIssuersBudget checks that a refused attempt spends the issuer's budget alone, not the address's.
func TestAnAttemptTheChecksRefuseSpendsOnlyTheIssuersBudget(t *testing.T) {
	t.Run("refused attempts against one address do not spend its budget", func(t *testing.T) {
		h := newHarness(t)
		issued := h.issue(tenant, "alice@example.com")
		h.asAdmin()
		for i := 0; i < 4; i++ {
			if w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"}); w.Code != http.StatusConflict {
				t.Fatalf("refused attempt %d answered %d, want 409: %s", i+1, w.Code, w.Body.String())
			}
		}
		if w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil); w.Code != http.StatusOK {
			t.Fatalf("the resend answered %d, want 200: the address budget must not have been spent by "+
				"attempts that wrote nothing: %s", w.Code, w.Body.String())
		}
	})

	t.Run("another tenant's refused attempt does not spend the address", func(t *testing.T) {
		h := newHarness(t)
		h.store.set(issueAddressPath, 2)
		h.store.set(issueIssuerPath, 100)
		h.store.set(issueGlobalPath, 100)
		issued := h.issue(tenant, "alice@example.com")

		h.accounts.put("alice@example.com", "acct-alice")
		h.members.hold(authn.Membership{Tenant: "tenant-b", Account: "acct-alice", Active: true})
		ctx := authntest.Context(t, context.Background(), authntest.WithAccount(adminID))
		if _, err := h.p.Issue(ctx, IssueInvite{Tenant: "tenant-b", Email: "alice@example.com"}); !errors.Is(err, ErrInvitationMember) {
			t.Fatalf("the refusal in the other tenant gave %v, want ErrInvitationMember", err)
		}

		if _, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("resending after another tenant's refused attempt gave %v, want it let through: "+
				"the address budget is shared wide and must not be spent by an attempt that wrote nothing", err)
		}
	})
}

// TestTheSettingsSchemaDeclaresTheBudgetKeys checks that the settings schema declares every budget key.
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

// TestTheDeclaredBudgetDefaultsAreInDomain checks that the shipped defaults lie inside their domains.
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

// TestTheProbeClientKeyCountsAnIPv6AddressByItsSlashSixtyFour checks the client key: one /64 shares a bucket, another differs, and IPv4 is counted as itself.
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

// TestTheBudgetKeysEscapeTheirSegments checks that a colon cannot forge another budget's key and that a key is stable within one window.
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
