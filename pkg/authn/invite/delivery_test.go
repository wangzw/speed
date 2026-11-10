package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

// TestTheIssueRouteReportsTheEmailDelivery holds the issue answer to carrying
// the outcome of the notice. With no notifier in the assembly the honest answer
// is not_configured, and the invitation is still issued.
func TestTheIssueRouteReportsTheEmailDelivery(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Token == "" {
		t.Error("the answer carries no token")
	}
	if want := authn.InviteNotConfigured.String(); answered.EmailDelivery != want {
		t.Errorf("email_delivery = %q, want %q when no notifier is configured", answered.EmailDelivery, want)
	}
	if h.rowCount() != 1 {
		t.Errorf("rows written = %d, want one", h.rowCount())
	}
}

// issuedAnswer is the part of the issue answer a delivery case reads. raw is the
// answer as it was written, kept so a case can assert that the token field is
// absent rather than merely empty.
type issuedAnswer struct {
	Invitation    AuthnInviteInvitation `json:"invitation"`
	Token         string                `json:"token"`
	EmailDelivery string                `json:"email_delivery"`
	raw           string
}

func (h *harness) issueOverRoute(email string) issuedAnswer {
	h.t.Helper()
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": email})
	if w.Code != http.StatusCreated {
		h.t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		h.t.Fatalf("decoding the answer: %v", err)
	}
	answered.raw = w.Body.String()
	return answered
}

// fakeNames stands in for the roster's id-to-name seam with a name that
// differs from the identifier, so a case can tell which of the two reached the
// notice.
type fakeNames map[string]string

func (m fakeNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return m[id], nil
}

// errNameUnavailable is the failure a case scripts for the name seam.
var errNameUnavailable = errors.New("the roster could not answer")

// failingNames is the name seam in its outage direction: no name is known,
// and the identifier must not stand in for one.
type failingNames struct{}

func (failingNames) TenantNameOf(context.Context, string) (string, error) {
	return "", errNameUnavailable
}

// fakeNotifier is the notifier seam a case scripts. It records every notice and
// the deadline the call carried, so a case can assert the answer waited on a
// bounded call rather than on the notifier's own time.
type fakeNotifier struct {
	mu       sync.Mutex
	delivery authn.InviteDelivery
	err      error
	panics   bool
	notices  []authn.InviteNotice
	deadline time.Time
	bounded  bool
}

func (n *fakeNotifier) Notify(ctx context.Context, notice authn.InviteNotice) (authn.InviteDelivery, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notices = append(n.notices, notice)
	n.deadline, n.bounded = ctx.Deadline()
	if n.panics {
		panic("the notifier stopped")
	}
	return n.delivery, n.err
}

func (n *fakeNotifier) last() (authn.InviteNotice, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.notices) == 0 {
		return authn.InviteNotice{}, false
	}
	return n.notices[len(n.notices)-1], true
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.notices)
}

// deliverAuditRows narrows the recorded audit rows to the delivery action.
func (h *harness) deliverAuditRows() []auditEntryLike {
	h.t.Helper()
	var rows []auditEntryLike
	for _, e := range h.audit.entries() {
		if e.Action == actionDeliver {
			rows = append(rows, auditEntryLike{Object: e.Object, Outcome: e.Outcome, Detail: e.Detail})
		}
	}
	return rows
}

// auditEntryLike is the part of an audit row a delivery case inspects.
type auditEntryLike struct {
	Object  string
	Outcome string
	Detail  map[string]string
}

// TestASentNoticeReportsSentAndAuditsNothing holds the successful delivery:
// the notice carries the invitation's tenant name, normalized address and
// token, the answer reports sent, and a send is not a failure, so it writes no
// delivery audit row.
func TestASentNoticeReportsSentAndAuditsNothing(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	// The fixture's identifier and name differ, so a notice that carried the
	// identifier could not pass for one that carried the name.
	h.p.tenantNames = fakeNames{"tenant-a": "acme"}

	answered := h.issueOverRoute("Alice@Example.com")
	if got := answered.EmailDelivery; got != authn.InviteSent.String() {
		t.Fatalf("email_delivery = %q, want sent", got)
	}
	notice, ok := notifier.last()
	if !ok {
		t.Fatal("the notifier was not called")
	}
	if notice.Tenant != "acme" || notice.Email != "alice@example.com" {
		t.Errorf("notice = %+v, want the tenant name and the normalized address", notice)
	}
	if notice.Tenant == "tenant-a" {
		t.Error("the notice carries the internal tenant identifier")
	}
	if !strings.HasPrefix(notice.Token, "spd_inv_") {
		t.Errorf("the notice carries %q, want the acceptance token", notice.Token)
	}
	if answered.Token != "" {
		t.Errorf("a sent notice was answered with the token %q; the recipient holds it already", answered.Token)
	}
	if strings.Contains(answered.raw, "spd_inv_") {
		t.Errorf("the answer of a sent notice carries a token: %s", answered.raw)
	}
	if strings.Contains(answered.raw, `"token"`) {
		t.Errorf("the answer of a sent notice carries a token field: %s", answered.raw)
	}
	if notice.ExpiresAt.IsZero() {
		t.Error("the notice carries no deadline")
	}
	if !notifier.bounded {
		t.Fatal("the notifier was called without a deadline")
	}
	// The bound is five seconds from the call. The lower bound only has to show
	// the deadline was not already spent.
	if left := time.Until(notifier.deadline); left <= 0 || left > notifyTimeout {
		t.Errorf("the notice's deadline leaves %s, want at most %s", left, notifyTimeout)
	}
	if rows := h.deliverAuditRows(); len(rows) != 0 {
		t.Errorf("a sent notice wrote %d delivery audit rows, want none", len(rows))
	}
}

// TestAFailedNoticeReportsFailedAndStillAnswers201 holds the delivery failure:
// the invitation is issued all the same, the answer is still 201 with its
// one-time token, the outcome is failed, and the failure leaves a best-effort
// audit row linked to the issue.
func TestAFailedNoticeReportsFailedAndStillAnswers201(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteFailed, err: errors.New("the carrier refused the message")}
	h.p.notifier = notifier
	h.p.tenantNames = fakeNames{"tenant-a": "acme"}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteFailed.String() {
		t.Fatalf("email_delivery = %q, want failed", got)
	}
	if answered.Token == "" {
		t.Error("a failed notice withheld the token")
	}
	if h.rowCount() != 1 {
		t.Errorf("rows written = %d, want the invitation still issued", h.rowCount())
	}
	rows := h.deliverAuditRows()
	if len(rows) != 1 {
		t.Fatalf("delivery audit rows = %d, want one", len(rows))
	}
	if rows[0].Object != answered.Invitation.ID || rows[0].Outcome != outcomeFailed {
		t.Errorf("delivery audit row = %+v, want the invitation and a failed outcome", rows[0])
	}
	if rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("delivery audit tenant = %q, want the name acme", rows[0].Detail[detailTenant])
	}
	for _, value := range rows[0].Detail {
		if value == answered.Token {
			t.Error("the delivery audit row carries the token")
		}
	}
	logged := h.logs.String()
	if !strings.Contains(logged, "the invitation notice was not delivered") {
		t.Errorf("the failed notice was not logged: %s", logged)
	}
	if strings.Contains(logged, answered.Token) {
		t.Error("the log carries the acceptance token")
	}
}

// TestAnUnconfiguredNoticeIsNotAFailure holds the notifier's own not_configured
// answer: nothing was attempted, so the answer reports it and no audit row is
// written.
func TestAnUnconfiguredNoticeIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.p.notifier = &fakeNotifier{delivery: authn.InviteNotConfigured}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteNotConfigured.String() {
		t.Errorf("email_delivery = %q, want not_configured", got)
	}
	if !strings.HasPrefix(answered.Token, "spd_inv_") {
		t.Errorf("an unconfigured notice withheld the token; the inviter has to hand the link over")
	}
	if rows := h.deliverAuditRows(); len(rows) != 0 {
		t.Errorf("an unconfigured notice wrote %d delivery audit rows, want none", len(rows))
	}
}

// TestANotifierPanicIsAFailedNotice holds the defective notifier: a panic is a
// failed delivery, so the answer is still 201 rather than the request ending in
// the entry point's 500.
func TestANotifierPanicIsAFailedNotice(t *testing.T) {
	h := newHarness(t)
	h.p.notifier = &fakeNotifier{panics: true}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteFailed.String() {
		t.Errorf("email_delivery = %q, want failed", got)
	}
	if rows := h.deliverAuditRows(); len(rows) != 1 {
		t.Errorf("delivery audit rows = %d, want one", len(rows))
	}
}

// TestAnUnknownDeliveryOutcomeIsAFailedNotice holds the closed set: a notifier
// that returns a value outside it and no error is not a send, so it is a
// failure rather than being written into the answer.
func TestAnUnknownDeliveryOutcomeIsAFailedNotice(t *testing.T) {
	h := newHarness(t)
	h.p.notifier = &fakeNotifier{delivery: authn.InviteDelivery(9)}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteFailed.String() {
		t.Errorf("email_delivery = %q, want failed", got)
	}
}

// TestARefusedIssueCallsNoNotifier holds the order: a request the issue refuses
// never reaches the notifier, so no notice describes an invitation that was not
// issued.
func TestARefusedIssueCallsNoNotifier(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier

	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "not-an-address"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if notifier.count() != 0 {
		t.Errorf("the notifier was called %d times for a refused issue, want none", notifier.count())
	}
}

// TestTheNoticeNamesTheTenantByNameNeverTheIdentifier holds the outbound rule
// on the invitation notice: the recipient reads the name, and the internal
// identifier the issue path was handed by its path guard never reaches the
// notice. The fixture's two values differ, so a notice carrying either one is
// distinguishable.
func TestTheNoticeNamesTheTenantByNameNeverTheIdentifier(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	h.p.tenantNames = fakeNames{"tenant-a": "acme"}

	h.issueOverRoute("alice@example.com")
	notice, ok := notifier.last()
	if !ok {
		t.Fatal("the notifier was not called")
	}
	if notice.Tenant != "acme" {
		t.Errorf("notice.Tenant = %q, want the name acme", notice.Tenant)
	}
	if strings.Contains(notice.Tenant, "tenant-a") {
		t.Errorf("notice.Tenant = %q carries the internal identifier", notice.Tenant)
	}
	if strings.Contains(h.logs.String(), "tenant-a") {
		t.Errorf("the delivery log carries the internal identifier: %s", h.logs.String())
	}
}

// TestANoticeWithNoNameResolverNamesNoTenant holds the absent seam: an
// assembly without the resolver renders no tenant rather than falling back to
// the identifier, which is internal.
func TestANoticeWithNoNameResolverNamesNoTenant(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	h.p.tenantNames = nil

	h.issueOverRoute("alice@example.com")
	notice, ok := notifier.last()
	if !ok {
		t.Fatal("the notifier was not called")
	}
	if notice.Tenant != "" {
		t.Errorf("notice.Tenant = %q, want empty when no name resolver is assembled", notice.Tenant)
	}
}

// TestANoticeWithAFailingNameResolverNamesNoTenant holds the resolver's outage
// direction: a name that cannot be read is no name, and the identifier must
// not stand in for it.
func TestANoticeWithAFailingNameResolverNamesNoTenant(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	h.p.tenantNames = failingNames{}

	// The issue route resolves the name for its own audit row too, so the
	// notice is driven directly: this case is about the notice's own answer to a
	// seam that cannot read a name.
	issued := h.issue("tenant-a", "alice@example.com")
	if got := h.p.deliverInvite(context.Background(), issued, newActionID()); got != authn.InviteSent {
		t.Fatalf("delivery = %s, want sent", got)
	}
	notice, ok := notifier.last()
	if !ok {
		t.Fatal("the notifier was not called")
	}
	if notice.Tenant != "" {
		t.Errorf("notice.Tenant = %q, want empty when the name could not be read", notice.Tenant)
	}
}

// TestTheNoticeCarriesTheRequestLocale holds the language the notice is
// rendered in to the request's own: the locale the request context carries
// reaches the notice unchanged.
func TestTheNoticeCarriesTheRequestLocale(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	issued := h.issue("tenant-a", "alice@example.com")

	ctx := i18n.WithLocale(context.Background(), "zh-CN")
	if got := h.p.deliverInvite(ctx, issued, newActionID()); got != authn.InviteSent {
		t.Fatalf("delivery = %s, want sent", got)
	}
	notice, ok := notifier.last()
	if !ok || notice.Locale != "zh-CN" {
		t.Errorf("notice locale = %q, want zh-CN", notice.Locale)
	}
}
