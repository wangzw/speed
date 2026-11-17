package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// wireIdentifier is one identifier as the account view publishes it: the six
// fields the view declares and nothing else.
type wireIdentifier struct {
	Kind           string `json:"kind"`
	Value          string `json:"value"`
	Proven         bool   `json:"proven"`
	SignIn         bool   `json:"sign_in"`
	Contact        bool   `json:"contact"`
	DefaultContact bool   `json:"default_contact"`
}

// identifiersField reads the raw identifiers member of one account view, so a
// case can hold the wire shape itself rather than a decoded slice in which an
// absent array and an empty one are both nil.
func identifiersField(t *testing.T, account map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	raw, ok := account["identifiers"]
	if !ok {
		t.Fatal("the account view carries no identifiers member")
	}
	return raw
}

// decodeIdentifiers reads an identifiers member into its wire shape.
func decodeIdentifiers(t *testing.T, raw json.RawMessage) []wireIdentifier {
	t.Helper()
	var out []wireIdentifier
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding the identifiers array %s: %v", raw, err)
	}
	return out
}

// singleAccountView drives GET B/accounts/:id and returns the account view as
// its raw JSON members.
func singleAccountView(t *testing.T, r *rig, id authn.AccountID) map[string]json.RawMessage {
	t.Helper()
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(id), nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the account: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Account map[string]json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding the account envelope: %v", err)
	}
	return envelope.Account
}

// listAccountView drives GET B/accounts and returns the view of one account
// from the page it answers with.
func listAccountView(t *testing.T, r *rig, id authn.AccountID) map[string]json.RawMessage {
	t.Helper()
	return listAccountViewQuery(t, r, "", id)
}

// listAccountViewQuery drives GET B/accounts with a query string and returns
// the view of one account from the page it answers with.
func listAccountViewQuery(t *testing.T, r *rig, query string, id authn.AccountID) map[string]json.RawMessage {
	t.Helper()
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts"+query, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing accounts: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding the account list: %v", err)
	}
	for _, a := range list.Accounts {
		var got string
		if err := json.Unmarshal(a["id"], &got); err != nil {
			continue
		}
		if got == string(id) {
			return a
		}
	}
	t.Fatalf("the list carries no view of account %q", id)
	return nil
}

// bothReads names the two reads that carry an account view, so a case can hold
// a property of both without repeating it.
var bothReads = []struct {
	name string
	view func(t *testing.T, r *rig, id authn.AccountID) map[string]json.RawMessage
}{
	{"single", singleAccountView},
	{"list", listAccountView},
}

// TestBothAccountReadsCarryTheLoginIdentifiers holds that the list read and the
// single read both publish an account's login identifiers: the two of them, with
// the kind, value and proven flag of each.
func TestBothAccountReadsCarryTheLoginIdentifiers(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{
			Kind: authn.IdentifierEmail, Value: "alice@example.com", VerifiedVia: authn.VerifiedViaEmailCode,
			SignIn: true, Contact: true, DefaultContact: true,
		},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "alice", VerifiedVia: authn.VerifiedViaAdmin, SignIn: true},
	)
	want := []wireIdentifier{
		{Kind: "username", Value: "alice", Proven: false, SignIn: true},
		{Kind: "email", Value: "alice@example.com", Proven: true, SignIn: true, Contact: true, DefaultContact: true},
	}
	for _, read := range bothReads {
		t.Run(read.name, func(t *testing.T) {
			raw := identifiersField(t, read.view(t, r, userID))
			if got := decodeIdentifiers(t, raw); !reflect.DeepEqual(got, want) {
				t.Errorf("identifiers = %+v, want %+v", got, want)
			}
		})
	}
}

// TestTheIdentifierViewCarriesOnlyTheSixFields holds the view's own shape: it
// is built field by field, so no identifier of the row, verification instant or
// named way reaches a client by being there.
func TestTheIdentifierViewCarriesOnlyTheSixFields(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{ID: "id-1", Kind: authn.IdentifierEmail, Value: "alice@example.com", VerifiedVia: authn.VerifiedViaEmailCode},
	)
	var elements []map[string]json.RawMessage
	if err := json.Unmarshal(identifiersField(t, singleAccountView(t, r, userID)), &elements); err != nil {
		t.Fatalf("decoding the identifiers array: %v", err)
	}
	if len(elements) != 1 {
		t.Fatalf("the view carries %d identifiers, want 1", len(elements))
	}
	got := make([]string, 0, len(elements[0]))
	for k := range elements[0] {
		got = append(got, k)
	}
	want := map[string]bool{
		"kind": true, "value": true, "proven": true,
		"sign_in": true, "contact": true, "default_contact": true,
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("the identifier view carries %q, which is not one of its six fields", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the identifier view carries %v, want the six fields kind, value, proven, sign_in, contact and default_contact", got)
	}
}

// TestAnAccountWithNoIdentifiersAnswersAnEmptyArray holds that an account with
// none publishes an empty array, never null, so a client can iterate the member
// without a nil check.
func TestAnAccountWithNoIdentifiersAnswersAnEmptyArray(t *testing.T) {
	r := newRig(t)
	for _, read := range bothReads {
		t.Run(read.name, func(t *testing.T) {
			raw := identifiersField(t, read.view(t, r, userID))
			if got := string(bytes.TrimSpace(raw)); got != "[]" {
				t.Errorf("identifiers = %s, want []", got)
			}
		})
	}
}

// TestTheAccountViewOrdersIdentifiersByKindThenValue holds the documented order:
// username, email and phone, and within one kind by value ascending. The stored
// order is deliberately different, so a view that passed the read through would
// fail. Both reads are held to it: the list path is the one the batch read now
// feeds.
func TestTheAccountViewOrdersIdentifiersByKindThenValue(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierPhone, Value: "+15550000001", VerifiedVia: authn.VerifiedViaSMSCode},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "b@example.com", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "zeta", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "a@example.com", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "alpha", VerifiedVia: authn.VerifiedViaAdmin},
	)
	want := []wireIdentifier{
		{Kind: "username", Value: "alpha"},
		{Kind: "username", Value: "zeta"},
		{Kind: "email", Value: "a@example.com"},
		{Kind: "email", Value: "b@example.com"},
		{Kind: "phone", Value: "+15550000001", Proven: true},
	}
	for _, read := range bothReads {
		t.Run(read.name, func(t *testing.T) {
			got := decodeIdentifiers(t, identifiersField(t, read.view(t, r, userID)))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("identifiers = %+v, want %+v", got, want)
			}
		})
	}
}

// TestAnIdentifierIsProvenOnlyWhenItsWayProvesOwnership holds the proven rule:
// a proof (sms-code, email-code, idp:<provider>) reads true and an
// assertion (invite, admin, import-unverified, self) reads false.
func TestAnIdentifierIsProvenOnlyWhenItsWayProvesOwnership(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "proved@example.com", VerifiedVia: authn.VerifiedViaEmailCode},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "asserted", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "self@example.com", VerifiedVia: authn.VerifiedViaSelf},
		authn.Identifier{Kind: authn.IdentifierPhone, Value: "+15550000002", VerifiedVia: authn.VerifiedViaSMSCode},
	)
	got := decodeIdentifiers(t, identifiersField(t, singleAccountView(t, r, userID)))
	want := []wireIdentifier{
		{Kind: "username", Value: "asserted", Proven: false},
		{Kind: "email", Value: "proved@example.com", Proven: true},
		{Kind: "email", Value: "self@example.com", Proven: false},
		{Kind: "phone", Value: "+15550000002", Proven: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identifiers = %+v, want %+v", got, want)
	}
}

// TestTheAccountListReadsIdentifiersOncePerPage holds the cost of a page: one
// batch read for the whole page, never one read per row, however many accounts
// it lists.
func TestTheAccountListReadsIdentifiersOncePerPage(t *testing.T) {
	r := newRig(t)
	other := authn.AccountID("acct-other")
	r.accounts.put(other, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	for id, value := range map[authn.AccountID]string{
		adminID: "admin@example.com", userID: "user@example.com", other: "other@example.com",
	} {
		r.accounts.putIdentifiers(id, authn.Identifier{Kind: authn.IdentifierEmail, Value: value, VerifiedVia: authn.VerifiedViaEmailCode})
	}
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing accounts: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding the account list: %v", err)
	}
	if len(list.Accounts) != 3 {
		t.Fatalf("the page carries %d accounts, want 3", len(list.Accounts))
	}
	if got := r.accounts.identifierBatchCalls(); got != 1 {
		t.Errorf("the page made %d batch identifier reads, want 1", got)
	}
	if got := r.accounts.identifierSingleCalls(); got != 0 {
		t.Errorf("the page made %d per-account identifier reads, want none", got)
	}
}

// TestEveryListedAccountCarriesTheIdentifiersOfItsSingleRead holds that a page's
// batch read answers each row exactly as the single read answers the same
// account: the proven flag and the documented order included. The stored order
// differs from the view's, and the mix of proving and asserting ways makes the
// proven flag a discriminator, not a constant.
func TestEveryListedAccountCarriesTheIdentifiersOfItsSingleRead(t *testing.T) {
	r := newRig(t)
	other := authn.AccountID("acct-other")
	r.accounts.put(other, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierPhone, Value: "+15550000001", VerifiedVia: authn.VerifiedViaSMSCode},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "user@example.com", VerifiedVia: authn.VerifiedViaEmailCode},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "user", VerifiedVia: authn.VerifiedViaAdmin},
	)
	r.accounts.putIdentifiers(adminID,
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "admin", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "admin@example.com", VerifiedVia: authn.VerifiedViaImportUnverified},
	)
	for _, id := range []authn.AccountID{adminID, userID, other} {
		single := decodeIdentifiers(t, identifiersField(t, singleAccountView(t, r, id)))
		listed := decodeIdentifiers(t, identifiersField(t, listAccountView(t, r, id)))
		if !reflect.DeepEqual(listed, single) {
			t.Errorf("%q in the list = %+v, want the single read's %+v", id, listed, single)
		}
	}
}

// TestAFailedIdentifierBatchFailsThePage holds that the batch read's failure is
// still the page's 503: replacing the per-row read must not turn a store failure
// into empty arrays.
func TestAFailedIdentifierBatchFailsThePage(t *testing.T) {
	r := newRig(t)
	r.accounts.failIdentifiers(errors.New("the store is down"))
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts", nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("listing accounts over a failed identifier read: status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
}

// TestTheAccountListShowsATombstoneWithNoIdentifiers holds that a page holding
// a tombstone still answers: deleting an account erases its identifiers and the
// identifier read reports a tombstone as authn.ErrAccountNotFound, so the list
// read must not wait on it. The row carries an empty array.
func TestTheAccountListShowsATombstoneWithNoIdentifiers(t *testing.T) {
	r := newRig(t)
	const gone authn.AccountID = "acct-tombstone"
	r.accounts.put(gone, authn.Account{Kind: authn.KindUser, Status: authn.StatusDeleted})
	// An identifier left bound to the row must not be read: deletion erases it,
	// so the stale row is not the view's answer.
	r.accounts.putIdentifiers(gone, authn.Identifier{Kind: authn.IdentifierEmail, Value: "gone@example.com", VerifiedVia: authn.VerifiedViaAdmin})
	view := listAccountViewQuery(t, r, "?status=deleted", gone)
	if got := string(bytes.TrimSpace(identifiersField(t, view))); got != "[]" {
		t.Errorf("identifiers = %s, want []", got)
	}
	if n := r.accounts.identifiersRead(gone); n != 0 {
		t.Errorf("the identifier read ran %d time(s) for a tombstone, want none", n)
	}
}

// TestTheAccountListShowsARowDeletedMidPageWithoutIdentifiers holds that an
// account deleted between the page read and its identifier read does not fail
// the whole page: the row stays in the snapshot and answers with an empty
// array. The single read still answers 404 for an account that is gone, because
// it names one account rather than a page.
func TestTheAccountListShowsARowDeletedMidPageWithoutIdentifiers(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID, authn.Identifier{Kind: authn.IdentifierEmail, Value: "alice@example.com"})
	r.accounts.vanish(userID)
	view := listAccountView(t, r, userID)
	if got := string(bytes.TrimSpace(identifiersField(t, view))); got != "[]" {
		t.Errorf("identifiers = %s, want []", got)
	}
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID), nil, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("the single read of a gone account: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}
