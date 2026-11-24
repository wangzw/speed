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

type wireIdentifier struct {
	Kind           string `json:"kind"`
	Value          string `json:"value"`
	Proven         bool   `json:"proven"`
	SignIn         bool   `json:"sign_in"`
	Contact        bool   `json:"contact"`
	DefaultContact bool   `json:"default_contact"`
}

func identifiersField(t *testing.T, account map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	raw, ok := account["identifiers"]
	if !ok {
		t.Fatal("the account view carries no identifiers member")
	}
	return raw
}

func decodeIdentifiers(t *testing.T, raw json.RawMessage) []wireIdentifier {
	t.Helper()
	var out []wireIdentifier
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding the identifiers array %s: %v", raw, err)
	}
	return out
}

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

func listAccountView(t *testing.T, r *rig, id authn.AccountID) map[string]json.RawMessage {
	t.Helper()
	return listAccountViewQuery(t, r, "", id)
}

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

var bothReads = []struct {
	name string
	view func(t *testing.T, r *rig, id authn.AccountID) map[string]json.RawMessage
}{
	{"single", singleAccountView},
	{"list", listAccountView},
}

// TestBothAccountReadsCarryTheLoginIdentifiers checks that the list and single reads both publish an account's login identifiers.
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

// TestTheIdentifierViewCarriesOnlyTheSixFields checks that the identifier view publishes only its six declared fields.
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

// TestAnAccountWithNoIdentifiersAnswersAnEmptyArray checks that an account with no identifiers publishes an empty array, never null.
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

// TestTheAccountViewOrdersIdentifiersByKindThenValue holds the documented order: username, email and phone, and within one kind by value ascending.
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

// TestAnIdentifierIsProvenOnlyWhenItsWayProvesOwnership checks that a proof reads proven and an assertion does not.
func TestAnIdentifierIsProvenOnlyWhenItsWayProvesOwnership(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "proved@example.com", VerifiedVia: authn.VerifiedViaEmailCode},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "asserted", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "invited@example.com", VerifiedVia: authn.VerifiedViaInvite},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "self@example.com", VerifiedVia: authn.VerifiedViaSelf},
		authn.Identifier{Kind: authn.IdentifierPhone, Value: "+15550000002", VerifiedVia: authn.VerifiedViaSMSCode},
	)
	got := decodeIdentifiers(t, identifiersField(t, singleAccountView(t, r, userID)))
	want := []wireIdentifier{
		{Kind: "username", Value: "asserted", Proven: false},
		{Kind: "email", Value: "invited@example.com", Proven: false},
		{Kind: "email", Value: "proved@example.com", Proven: true},
		{Kind: "email", Value: "self@example.com", Proven: false},
		{Kind: "phone", Value: "+15550000002", Proven: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identifiers = %+v, want %+v", got, want)
	}
}

// TestTheAccountListReadsIdentifiersOncePerPage checks that a page reads identifiers in one batch call, not once per row.
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

// TestEveryListedAccountCarriesTheIdentifiersOfItsSingleRead checks that the batch read answers each row as the single read does.
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

// TestAFailedIdentifierBatchFailsThePage checks that a failed identifier batch is the page's 503.
func TestAFailedIdentifierBatchFailsThePage(t *testing.T) {
	r := newRig(t)
	r.accounts.failIdentifiers(errors.New("the store is down"))
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts", nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("listing accounts over a failed identifier read: status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
}

// TestTheAccountListShowsATombstoneWithNoIdentifiers checks that a tombstone in a page answers with an empty identifier array.
func TestTheAccountListShowsATombstoneWithNoIdentifiers(t *testing.T) {
	r := newRig(t)
	const gone authn.AccountID = "acct-tombstone"
	r.accounts.put(gone, authn.Account{Kind: authn.KindUser, Status: authn.StatusDeleted})
	r.accounts.putIdentifiers(gone, authn.Identifier{Kind: authn.IdentifierEmail, Value: "gone@example.com", VerifiedVia: authn.VerifiedViaAdmin})
	view := listAccountViewQuery(t, r, "?status=deleted", gone)
	if got := string(bytes.TrimSpace(identifiersField(t, view))); got != "[]" {
		t.Errorf("identifiers = %s, want []", got)
	}
	if n := r.accounts.identifiersRead(gone); n != 0 {
		t.Errorf("the identifier read ran %d time(s) for a tombstone, want none", n)
	}
}

// TestTheAccountListShowsARowDeletedMidPageWithoutIdentifiers checks that a row deleted mid-page answers with an empty array, not a failure.
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
