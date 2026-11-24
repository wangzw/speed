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

// TestTheIssueRouteReportsTheEmailDelivery checks that the issue answer carries the notice outcome, here not_configured.
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

type fakeNames map[string]string

func (m fakeNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return m[id], nil
}

var errNameUnavailable = errors.New("the roster could not answer")

type failingNames struct{}

func (failingNames) TenantNameOf(context.Context, string) (string, error) {
	return "", errNameUnavailable
}

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

type auditEntryLike struct {
	Object  string
	Outcome string
	Detail  map[string]string
}

// TestASentNoticeReportsSentAndAuditsNothing checks that a sent notice reports sent, withholds the token from the answer, and writes no delivery audit row.
func TestASentNoticeReportsSentAndAuditsNothing(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
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
	if left := time.Until(notifier.deadline); left <= 0 || left > notifyTimeout {
		t.Errorf("the notice's deadline leaves %s, want at most %s", left, notifyTimeout)
	}
	if rows := h.deliverAuditRows(); len(rows) != 0 {
		t.Errorf("a sent notice wrote %d delivery audit rows, want none", len(rows))
	}
}

// TestAFailedNoticeReportsFailedAndStillAnswers201 checks that a failed notice still answers 201 with its token and writes one failed delivery audit row.
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

// TestAnUnconfiguredNoticeIsNotAFailure checks that not_configured is reported as such and writes no audit row.
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

// TestANotifierPanicIsAFailedNotice checks that a notifier panic is a failed delivery, not the entry point's 500.
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

// TestAnUnknownDeliveryOutcomeIsAFailedNotice checks that a delivery value outside the set is reported as failed.
func TestAnUnknownDeliveryOutcomeIsAFailedNotice(t *testing.T) {
	h := newHarness(t)
	h.p.notifier = &fakeNotifier{delivery: authn.InviteDelivery(9)}

	answered := h.issueOverRoute("alice@example.com")
	if got := answered.EmailDelivery; got != authn.InviteFailed.String() {
		t.Errorf("email_delivery = %q, want failed", got)
	}
}

// TestARefusedIssueCallsNoNotifier checks that a refused issue never calls the notifier.
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

// TestTheNoticeNamesTheTenantByNameNeverTheIdentifier checks that the notice and its log carry the tenant name, never the internal identifier.
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

// TestANoticeWithNoNameResolverNamesNoTenant checks that an absent name resolver names no tenant rather than falling back to the identifier.
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

// TestANoticeWithAFailingNameResolverNamesNoTenant checks that an unreadable name is no name, not the identifier.
func TestANoticeWithAFailingNameResolverNamesNoTenant(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	h.p.tenantNames = failingNames{}

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

// TestTheNoticeCarriesTheRequestLocale checks that the request's locale reaches the notice.
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
