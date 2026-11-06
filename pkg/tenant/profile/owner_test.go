package profile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// The account read is a stand-in for authn.Accounts. Only Get and
// IdentifiersMany are read by this surface; the rest return an error naming
// themselves, so a handler that reaches for another method fails loudly.
var _ authn.Accounts = (*stubAccounts)(nil)

// stubAccounts is the account store this surface's owner resolution reads. It
// answers from what a case set and fails with the error a case set.
type stubAccounts struct {
	accounts       map[authn.AccountID]authn.Account
	identifiers    map[authn.AccountID][]authn.Identifier
	getErr         error
	identifiersErr error
	got            []authn.AccountID
}

func (a *stubAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.got = append(a.got, id)
	if a.getErr != nil {
		return authn.Account{}, a.getErr
	}
	account, ok := a.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return account, nil
}

func (a *stubAccounts) IdentifiersMany(_ context.Context, ids []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	if a.identifiersErr != nil {
		return nil, a.identifiersErr
	}
	out := make(map[authn.AccountID][]authn.Identifier, len(ids))
	for _, id := range ids {
		if held, ok := a.identifiers[id]; ok {
			out[id] = append([]authn.Identifier(nil), held...)
		}
	}
	return out, nil
}

func (*stubAccounts) Lookup(context.Context, authn.IdentifierKind, string) (authn.AccountID, error) {
	return "", errors.New("tenant.profile: the owner read does not look up an identifier")
}

func (*stubAccounts) ByExternal(context.Context, string, string) (authn.AccountID, error) {
	return "", errors.New("tenant.profile: the owner read does not look up an external identity")
}

func (*stubAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	return nil, errors.New("tenant.profile: the owner read lists several identifiers at once")
}

func (*stubAccounts) Externals(context.Context, authn.AccountID) ([]authn.ExternalBinding, error) {
	return nil, errors.New("tenant.profile: the owner read does not list external identities")
}

func (*stubAccounts) List(context.Context, authn.AccountQuery) ([]authn.Account, string, error) {
	return nil, "", errors.New("tenant.profile: the owner read does not list accounts")
}

func (*stubAccounts) ListServiceSecrets(context.Context, authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	return nil, errors.New("tenant.profile: the owner read does not list service secrets")
}

// The owner fixture keeps the account identifier, the display name and the
// username different from one another, so a resolution that puts the wrong one
// into either field fails the case.
const (
	ownerAccountID = "01a1b156-7c2e-7000-9000-00000000abcd"
	ownerName      = "Alex Morgan"
	ownerUser      = "alex.morgan"
)

// ownerTenant is a tenant whose owner is the fixture account.
func ownerTenant() tenant.Tenant {
	row := sampleTenant()
	row.OwnerAccount = ownerAccountID
	return row
}

// ownerAccounts answers the fixture owner with its display name and username,
// plus an email address so the username is not the only identifier.
func ownerAccounts() *stubAccounts {
	return &stubAccounts{
		accounts: map[authn.AccountID]authn.Account{
			ownerAccountID: {ID: ownerAccountID, DisplayName: ownerName},
		},
		identifiers: map[authn.AccountID][]authn.Identifier{
			ownerAccountID: {
				{Kind: authn.IdentifierEmail, Value: "alex@acme.test"},
				{Kind: authn.IdentifierUsername, Value: ownerUser},
			},
		},
	}
}

// newRigOver builds a rig over the product the module assembly built, so the
// optional seams the assembly takes up are the ones a case supplies. The rig's
// own stand-ins keep the rest of the case in control.
func newRigOver(t *testing.T, accounts authn.Accounts) *rig {
	t.Helper()
	r := newRig(t)
	var built any
	if err := (moduleAssembly{accounts: accounts, record: &built}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the module assembly failed: %v", err)
	}
	assembled, ok := built.(*product)
	if !ok || assembled == nil {
		t.Fatalf("the assembly built %T, want this module's product", built)
	}
	assembled.catalog = r.catalog
	assembled.guard = r.guard
	assembled.audit = r.audit
	assembled.verification = r.signup
	assembled.routeGuard = permissiveRouteGuard{}
	assembled.kind = kindStub{}
	assembled.logger = r.logs.logger()
	assembled.cfg = resolved{endpoints: []string{"api"}}
	r.p = assembled
	r.mount()
	return r
}

// ownerFields is the part of the profile view the owner cases read.
type ownerFields struct {
	OwnerAccount     string `json:"ownerAccount"`
	OwnerDisplayName string `json:"ownerDisplayName"`
	OwnerUsername    string `json:"ownerUsername"`
}

func ownerOf(t *testing.T, w *httptest.ResponseRecorder) ownerFields {
	t.Helper()
	var body struct {
		Profile ownerFields `json:"profile"`
	}
	decode(t, w, &body)
	return body.Profile
}

// assertNoAccountID holds that neither owner field carries the account
// identifier the fixture separates from the name and the username.
func assertNoAccountID(t *testing.T, owner ownerFields) {
	t.Helper()
	if owner.OwnerDisplayName == ownerAccountID {
		t.Errorf("ownerDisplayName = %q, want a display name, never the account identifier", owner.OwnerDisplayName)
	}
	if owner.OwnerUsername == ownerAccountID {
		t.Errorf("ownerUsername = %q, want a username, never the account identifier", owner.OwnerUsername)
	}
}

// TestTheReadShowsTheOwnerByDisplayNameAndUsername holds the read's answer: the
// owner's display name and username, resolved from the account the roster row
// names, beside the account key the ownership decision uses.
func TestTheReadShowsTheOwnerByDisplayNameAndUsername(t *testing.T) {
	r := newRigOver(t, ownerAccounts())
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerAccount != ownerAccountID {
		t.Errorf("ownerAccount = %q, want %q", owner.OwnerAccount, ownerAccountID)
	}
	if owner.OwnerDisplayName != ownerName {
		t.Errorf("ownerDisplayName = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != ownerUser {
		t.Errorf("ownerUsername = %q, want %q", owner.OwnerUsername, ownerUser)
	}
	assertNoAccountID(t, owner)
}

// TestTheWriteShowsTheOwnerByDisplayNameAndUsername holds the same resolution
// on the write's answer, which is built from the row the request already read.
func TestTheWriteShowsTheOwnerByDisplayNameAndUsername(t *testing.T) {
	r := newRigOver(t, ownerAccounts())
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerAccount != ownerAccountID {
		t.Errorf("ownerAccount = %q, want %q", owner.OwnerAccount, ownerAccountID)
	}
	if owner.OwnerDisplayName != ownerName {
		t.Errorf("ownerDisplayName = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != ownerUser {
		t.Errorf("ownerUsername = %q, want %q", owner.OwnerUsername, ownerUser)
	}
	assertNoAccountID(t, owner)
}

// TestWithNoAccountSeamTheOwnerNamesAreEmpty holds an assembly that delivers no
// account read: the owner is unresolved but is never replaced by the account
// identifier, on either route.
func TestWithNoAccountSeamTheOwnerNamesAreEmpty(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   any
	}{
		{"read", http.MethodGet, nil},
		{"write", http.MethodPatch, validWrite()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.held = ownerTenant()

			w := r.do(c.method, profileTarget, c.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
			}
			owner := ownerOf(t, w)
			if owner.OwnerDisplayName != "" || owner.OwnerUsername != "" {
				t.Errorf("owner = (%q, %q), want both empty with no account seam",
					owner.OwnerDisplayName, owner.OwnerUsername)
			}
			if owner.OwnerAccount != ownerAccountID {
				t.Errorf("ownerAccount = %q, want %q", owner.OwnerAccount, ownerAccountID)
			}
			assertNoAccountID(t, owner)
		})
	}
}

// TestAnUnknownOwnerAccountLeavesTheOwnerNamesEmpty holds an owner the account
// store does not know, a tombstone included: both names are empty and neither
// falls back to the identifier.
func TestAnUnknownOwnerAccountLeavesTheOwnerNamesEmpty(t *testing.T) {
	r := newRigOver(t, &stubAccounts{})
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerDisplayName != "" || owner.OwnerUsername != "" {
		t.Errorf("owner = (%q, %q), want both empty for an unknown account",
			owner.OwnerDisplayName, owner.OwnerUsername)
	}
	assertNoAccountID(t, owner)
}

// TestAStaticOwnerSubjectLeavesTheOwnerNamesEmpty holds a static credential
// table subject, which has no account row: both names are empty and no account
// read is made.
func TestAStaticOwnerSubjectLeavesTheOwnerNamesEmpty(t *testing.T) {
	accounts := ownerAccounts()
	r := newRigOver(t, accounts)
	row := sampleTenant()
	row.OwnerAccount = authn.StaticPrefix + "ops"
	r.catalog.held = row

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerDisplayName != "" || owner.OwnerUsername != "" {
		t.Errorf("owner = (%q, %q), want both empty for a static subject",
			owner.OwnerDisplayName, owner.OwnerUsername)
	}
	if len(accounts.got) != 0 {
		t.Errorf("the account store was read for a static subject: %v", accounts.got)
	}
}

// TestAnOwnerAccountReadFailureIsAServiceUnavailable holds a Get failure on
// both routes: a 503 with no cause on the wire, the cause in the log.
func TestAnOwnerAccountReadFailureIsAServiceUnavailable(t *testing.T) {
	cause := errors.New("the account store is down")
	cases := []struct {
		name   string
		method string
		body   any
	}{
		{"read", http.MethodGet, nil},
		{"write", http.MethodPatch, validWrite()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := ownerAccounts()
			accounts.getErr = cause
			r := newRigOver(t, accounts)
			r.catalog.held = ownerTenant()

			w := r.do(c.method, profileTarget, c.body)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body %q", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), cause.Error()) {
				t.Errorf("the 503 carries the cause on the wire: %q", w.Body.String())
			}
			if !strings.Contains(r.logs.String(), cause.Error()) {
				t.Errorf("the cause was not logged: logs %q", r.logs.String())
			}
		})
	}
}

// TestAnOwnerIdentifierReadFailureIsAServiceUnavailable holds an IdentifiersMany
// failure on both routes: a 503 with no cause on the wire, the cause in the log.
func TestAnOwnerIdentifierReadFailureIsAServiceUnavailable(t *testing.T) {
	cause := errors.New("the identifier store is down")
	cases := []struct {
		name   string
		method string
		body   any
	}{
		{"read", http.MethodGet, nil},
		{"write", http.MethodPatch, validWrite()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := ownerAccounts()
			accounts.identifiersErr = cause
			r := newRigOver(t, accounts)
			r.catalog.held = ownerTenant()

			w := r.do(c.method, profileTarget, c.body)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body %q", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), cause.Error()) {
				t.Errorf("the 503 carries the cause on the wire: %q", w.Body.String())
			}
			if !strings.Contains(r.logs.String(), cause.Error()) {
				t.Errorf("the cause was not logged: logs %q", r.logs.String())
			}
		})
	}
}

// TestAnOwnerWithoutAUsernameHasOnlyTheDisplayName holds the one field that can
// stand alone: an account with no username identifier still names the owner by
// display name, and the username stays empty rather than borrowing anything.
func TestAnOwnerWithoutAUsernameHasOnlyTheDisplayName(t *testing.T) {
	accounts := &stubAccounts{
		accounts: map[authn.AccountID]authn.Account{
			ownerAccountID: {ID: ownerAccountID, DisplayName: ownerName},
		},
		identifiers: map[authn.AccountID][]authn.Identifier{
			ownerAccountID: {{Kind: authn.IdentifierEmail, Value: "alex@acme.test"}},
		},
	}
	r := newRigOver(t, accounts)
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerDisplayName != ownerName {
		t.Errorf("ownerDisplayName = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != "" {
		t.Errorf("ownerUsername = %q, want empty for an account with no username", owner.OwnerUsername)
	}
	assertNoAccountID(t, owner)
}
