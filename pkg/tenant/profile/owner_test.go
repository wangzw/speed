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

var _ authn.Accounts = (*stubAccounts)(nil)

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

const (
	ownerAccountID = "01a1b156-7c2e-7000-9000-00000000abcd"
	ownerName      = "Alex Morgan"
	ownerUser      = "alex.morgan"
)

func ownerTenant() tenant.Tenant {
	row := sampleTenant()
	row.OwnerAccount = ownerAccountID
	return row
}

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

type ownerFields struct {
	OwnerAccount     string `json:"owner_account"`
	OwnerDisplayName string `json:"owner_display_name"`
	OwnerUsername    string `json:"owner_username"`
}

func ownerOf(t *testing.T, w *httptest.ResponseRecorder) ownerFields {
	t.Helper()
	var body struct {
		Profile ownerFields `json:"profile"`
	}
	decode(t, w, &body)
	return body.Profile
}

func assertNoAccountID(t *testing.T, owner ownerFields) {
	t.Helper()
	if owner.OwnerDisplayName == ownerAccountID {
		t.Errorf("owner_display_name = %q, want a display name, never the account identifier", owner.OwnerDisplayName)
	}
	if owner.OwnerUsername == ownerAccountID {
		t.Errorf("owner_username = %q, want a username, never the account identifier", owner.OwnerUsername)
	}
}

// TestTheReadShowsTheOwnerByDisplayNameAndUsername checks that the read resolves the owner's display name and username from the account.
func TestTheReadShowsTheOwnerByDisplayNameAndUsername(t *testing.T) {
	r := newRigOver(t, ownerAccounts())
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerAccount != ownerAccountID {
		t.Errorf("owner_account = %q, want %q", owner.OwnerAccount, ownerAccountID)
	}
	if owner.OwnerDisplayName != ownerName {
		t.Errorf("owner_display_name = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != ownerUser {
		t.Errorf("owner_username = %q, want %q", owner.OwnerUsername, ownerUser)
	}
	assertNoAccountID(t, owner)
}

// TestTheWriteShowsTheOwnerByDisplayNameAndUsername checks the same owner resolution on the write's answer.
func TestTheWriteShowsTheOwnerByDisplayNameAndUsername(t *testing.T) {
	r := newRigOver(t, ownerAccounts())
	r.catalog.held = ownerTenant()

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	owner := ownerOf(t, w)
	if owner.OwnerAccount != ownerAccountID {
		t.Errorf("owner_account = %q, want %q", owner.OwnerAccount, ownerAccountID)
	}
	if owner.OwnerDisplayName != ownerName {
		t.Errorf("owner_display_name = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != ownerUser {
		t.Errorf("owner_username = %q, want %q", owner.OwnerUsername, ownerUser)
	}
	assertNoAccountID(t, owner)
}

// TestWithNoAccountSeamTheOwnerNamesAreEmpty checks that without an account read both owner names are empty, never the account identifier.
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
				t.Errorf("owner_account = %q, want %q", owner.OwnerAccount, ownerAccountID)
			}
			assertNoAccountID(t, owner)
		})
	}
}

// TestAnUnknownOwnerAccountLeavesTheOwnerNamesEmpty checks that an owner the account store does not know leaves both names empty.
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

// TestAStaticOwnerSubjectLeavesTheOwnerNamesEmpty checks that a static credential-table subject leaves both names empty and makes no account read.
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

// TestAnOwnerAccountReadFailureIsAServiceUnavailable checks that an account read failure is a 503 with the cause only in the log.
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

// TestAnOwnerIdentifierReadFailureIsAServiceUnavailable checks that an identifier read failure is a 503 with the cause only in the log.
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

// TestAnOwnerWithoutAUsernameHasOnlyTheDisplayName checks that an owner with no username identifier is named by display name alone.
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
		t.Errorf("owner_display_name = %q, want %q", owner.OwnerDisplayName, ownerName)
	}
	if owner.OwnerUsername != "" {
		t.Errorf("owner_username = %q, want empty for an account with no username", owner.OwnerUsername)
	}
	assertNoAccountID(t, owner)
}
