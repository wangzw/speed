package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// wireIdentifier is one identifier as the account view publishes it: the three
// fields the view declares and nothing else.
type wireIdentifier struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Proven bool   `json:"proven"`
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
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "alice@example.com", VerifiedVia: authn.VerifiedViaEmailCode},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "alice", VerifiedVia: authn.VerifiedViaAdmin},
	)
	want := []wireIdentifier{
		{Kind: "username", Value: "alice", Proven: false},
		{Kind: "email", Value: "alice@example.com", Proven: true},
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

// TestTheIdentifierViewCarriesOnlyTheThreeFields holds the view's own shape: it
// is built field by field, so no identifier of the row, verification instant or
// named way reaches a client by being there.
func TestTheIdentifierViewCarriesOnlyTheThreeFields(t *testing.T) {
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
	want := map[string]bool{"kind": true, "value": true, "proven": true}
	for _, k := range got {
		if !want[k] {
			t.Errorf("the identifier view carries %q, which is not one of its three fields", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the identifier view carries %v, want the three fields kind, value and proven", got)
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
// fail.
func TestTheAccountViewOrdersIdentifiersByKindThenValue(t *testing.T) {
	r := newRig(t)
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierPhone, Value: "+15550000001", VerifiedVia: authn.VerifiedViaSMSCode},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "b@example.com", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "zeta", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "a@example.com", VerifiedVia: authn.VerifiedViaAdmin},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "alpha", VerifiedVia: authn.VerifiedViaAdmin},
	)
	got := decodeIdentifiers(t, identifiersField(t, singleAccountView(t, r, userID)))
	want := []wireIdentifier{
		{Kind: "username", Value: "alpha"},
		{Kind: "username", Value: "zeta"},
		{Kind: "email", Value: "a@example.com"},
		{Kind: "email", Value: "b@example.com"},
		{Kind: "phone", Value: "+15550000001", Proven: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identifiers = %+v, want %+v", got, want)
	}
}

// TestAnIdentifierIsProvenOnlyWhenItsWayProvesOwnership holds the proven rule:
// a proof (sms-code, email-code, invite, idp:<provider>) reads true and an
// assertion (admin, import-unverified, self) reads false.
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
