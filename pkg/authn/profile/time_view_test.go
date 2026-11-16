package profile

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// requireNullField asserts that one field of a decoded JSON object is null.
func requireNullField(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if string(raw) != "null" {
		t.Errorf("%s = %s, want null for an absent instant", name, raw)
	}
}

// requireUTCInstant asserts that one field of a decoded JSON object is a
// quoted string and that its wire text ends in Z.
func requireUTCInstant(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if !strings.HasSuffix(string(raw), `Z"`) {
		t.Errorf("%s = %s, want an RFC 3339 instant in UTC ending in Z", name, raw)
	}
}

// TestTheProfileInstantsAreWrittenInUTC holds the wire rule for the instants
// the profile carries: a value stored in a non-UTC zone leaves as UTC with a
// trailing Z.
func TestTheProfileInstantsAreWrittenInUTC(t *testing.T) {
	at := time.Date(2027, 3, 4, 5, 6, 7, 0, time.FixedZone("UTC+8", 8*60*60))
	r := newRig(t)
	r.accounts.put(meAccount, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive, DisplayName: "Me", CreatedAt: at,
	})
	r.verification.state = authn.SignupStatus{State: authn.SignupVerified, Email: "me@example.test", Since: at}
	r.accounts.identifiers[meAccount] = []authn.Identifier{{
		ID: "id-1", Kind: authn.IdentifierEmail, Value: "me@example.test",
		VerifiedAt: at, VerifiedVia: authn.VerifiedViaEmailCode,
	}}
	r.accounts.links[meAccount] = []authn.ExternalBinding{{ID: "link-1", Provider: "idp-1", CreatedAt: at, LastLoginAt: at}}

	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the profile: %v", err)
	}
	requireUTCInstant(t, body, "created_at")

	var verification map[string]json.RawMessage
	if err := json.Unmarshal(body["verification"], &verification); err != nil {
		t.Fatalf("decoding the verification: %v", err)
	}
	requireUTCInstant(t, verification, "since")

	var identifiers []map[string]json.RawMessage
	if err := json.Unmarshal(body["identifiers"], &identifiers); err != nil {
		t.Fatalf("decoding the identifiers: %v", err)
	}
	if len(identifiers) != 1 {
		t.Fatalf("identifiers = %+v, want one", identifiers)
	}
	requireUTCInstant(t, identifiers[0], "verified_at")

	var links []map[string]json.RawMessage
	if err := json.Unmarshal(body["links"], &links); err != nil {
		t.Fatalf("decoding the links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v, want one", links)
	}
	requireUTCInstant(t, links[0], "created_at")
	requireUTCInstant(t, links[0], "last_login_at")
}

// TestTheOptionalInstantsAreNullWhenAbsent holds the wire rule for the profile
// instants a record may lack: the account's creation instant, a binding's
// creation instant and the verified-since instant are null rather than the
// empty string.
func TestTheOptionalInstantsAreNullWhenAbsent(t *testing.T) {
	r := newRig(t)
	r.accounts.links[meAccount] = []authn.ExternalBinding{{ID: "link-1", Provider: "idp-1"}}
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the profile: %v", err)
	}
	requireNullField(t, body, "created_at")

	var verification map[string]json.RawMessage
	if err := json.Unmarshal(body["verification"], &verification); err != nil {
		t.Fatalf("decoding the verification: %v", err)
	}
	requireNullField(t, verification, "since")

	var links []map[string]json.RawMessage
	if err := json.Unmarshal(body["links"], &links); err != nil {
		t.Fatalf("decoding the links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v, want one", links)
	}
	requireNullField(t, links[0], "created_at")
	if _, present := links[0]["last_login_at"]; present {
		t.Errorf("last_login_at = %s, want the field omitted for a binding never used", links[0]["last_login_at"])
	}
}
