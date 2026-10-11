package selfservice_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

const secretSubject = "subject-9f3a-never-shown"

func (h *harness) richAccount() {
	h.accounts.account.MFARequired = true
	h.accounts.identifiers = []authn.Identifier{
		{ID: "ident-1", Kind: authn.IdentifierUsername, Value: "ada", VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaAdmin},
		{ID: "ident-2", Kind: authn.IdentifierPhone, Value: "+8613800000000", VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaSMSCode},
	}
	h.accounts.externals = []authn.ExternalBinding{
		{ID: "link-1", Provider: "oidc:corp", Subject: secretSubject, Email: "ada@corp.example", EmailVerified: true, CreatedAt: now.Add(-24 * time.Hour), LastLoginAt: now.Add(-time.Hour)},
		{ID: "link-2", Provider: "github", Subject: "12345678", CreatedAt: now.Add(-48 * time.Hour)},
	}
}

// TestMeReturnsTheProfileTheIdentifiersAndTheLinks holds the answer of GET /me
// field by field: the account's own data, each identifier and each binding with
// the identifier to unlink it by.
func TestMeReturnsTheProfileTheIdentifiersAndTheLinks(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	w := h.get("/me")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /me = %d, want 200: %s", w.Code, w.Body)
	}
	var got struct {
		AccountID   string `json:"account_id"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
		MFARequired bool   `json:"mfa_required"`
		PasswordSet bool   `json:"password_set"`
		CreatedAt   string `json:"created_at"`
		Identifiers []struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			Value       string `json:"value"`
			VerifiedVia string `json:"verified_via"`
			VerifiedAt  string `json:"verified_at"`
		} `json:"identifiers"`
		Links []struct {
			ID          string `json:"id"`
			Provider    string `json:"provider"`
			Email       string `json:"email"`
			CreatedAt   string `json:"created_at"`
			LastLoginAt string `json:"last_login_at"`
		} `json:"links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	if got.AccountID != string(account) || got.Kind != "user" || got.DisplayName != "Ada" || !got.MFARequired || !got.PasswordSet {
		t.Errorf("profile = %+v", got)
	}
	if got.CreatedAt != "2026-10-09T12:00:00Z" {
		t.Errorf("created_at = %q", got.CreatedAt)
	}
	if len(got.Identifiers) != 2 || got.Identifiers[1].ID != "ident-2" || got.Identifiers[1].Kind != "phone" ||
		got.Identifiers[1].Value != "+8613800000000" || got.Identifiers[1].VerifiedVia != "sms-code" ||
		got.Identifiers[1].VerifiedAt != "2026-10-11T11:59:00Z" {
		t.Errorf("identifiers = %+v", got.Identifiers)
	}
	if len(got.Links) != 2 {
		t.Fatalf("links = %+v, want two", got.Links)
	}
	if l := got.Links[0]; l.ID != "link-1" || l.Provider != "oidc:corp" || l.Email != "ada@corp.example" ||
		l.CreatedAt != "2026-10-10T12:00:00Z" || l.LastLoginAt != "2026-10-11T11:00:00Z" {
		t.Errorf("link 1 = %+v", l)
	}
	if l := got.Links[1]; l.LastLoginAt != "" || l.Email != "" {
		t.Errorf("a link that never logged in and has no address carries them: %+v", l)
	}
}

// TestMeKeepsMFARequiredAndPasswordSetApart holds that the two booleans are wired
// to their own account fields: an account that requires a factor and has no
// password, and one that has a password and requires no factor, read back as they
// are.
func TestMeKeepsMFARequiredAndPasswordSetApart(t *testing.T) {
	for _, c := range []struct {
		name             string
		mfa, hasPassword bool
	}{
		{"requires a factor, no password", true, false},
		{"has a password, no factor required", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.accounts.account.MFARequired = c.mfa
			h.accounts.account.PasswordSet = c.hasPassword
			var got struct {
				MFARequired bool `json:"mfa_required"`
				PasswordSet bool `json:"password_set"`
			}
			if err := json.Unmarshal(h.get("/me").Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.MFARequired != c.mfa || got.PasswordSet != c.hasPassword {
				t.Errorf("mfa_required=%v password_set=%v, want %v and %v", got.MFARequired, got.PasswordSet, c.mfa, c.hasPassword)
			}
		})
	}
}

// TestMeNeverCarriesASubject holds that no binding's provider-side subject is in
// the answer, under any key: the answer is searched as text, so a field added
// later under another name is found too.
func TestMeNeverCarriesASubject(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	body := h.get("/me").Body.String()
	for _, secret := range []string{secretSubject, "12345678", "subject"} {
		if strings.Contains(body, secret) {
			t.Errorf("GET /me carries %q: %s", secret, body)
		}
	}
}

// TestMeIsReadFromTheIdentityAndNotTheRequest holds that the account read is the
// identity's and that an account with nothing bound answers empty lists, not
// null, for a client that iterates them.
func TestMeIsReadFromTheIdentityAndNotTheRequest(t *testing.T) {
	h := newHarness(t)
	h.as(authntest.WithAccount("acct-7"))
	w := h.get("/me")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /me = %d", w.Code)
	}
	for _, name := range []string{"accounts.Get acct-7", "accounts.Identifiers acct-7", "accounts.Externals acct-7"} {
		if !slices.Contains(h.log.all(), name) {
			t.Errorf("calls %v lack %q", h.log.all(), name)
		}
	}
	if !strings.Contains(w.Body.String(), `"identifiers":[]`) || !strings.Contains(w.Body.String(), `"links":[]`) {
		t.Errorf("empty lists are not written as arrays: %s", w.Body)
	}
}

// TestMeIsOpenToAnImpersonatingIdentity holds the one route an impersonating
// identity reaches: it reads the impersonated account, and no freshness is asked
// for, because the route changes nothing.
func TestMeIsOpenToAnImpersonatingIdentity(t *testing.T) {
	h := newHarness(t)
	h.as(authntest.WithActor("admin-1"), authntest.WithScope("tenant-a"))
	if w := h.get("/me"); w.Code != http.StatusOK {
		t.Fatalf("GET /me as an impersonating identity = %d, want 200", w.Code)
	}
	if h.log.count("mfa.Factors") != 0 {
		t.Error("GET /me asked for the account's factors: it asks for no freshness")
	}
}

// TestMeWithoutAnIdentityIsRefused holds the gateway stand-in's 401 and that no
// dependency was called for it.
func TestMeWithoutAnIdentityIsRefused(t *testing.T) {
	h := newHarness(t)
	h.m.AsAnonymous()
	if w := h.get("/me"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me with no identity = %d, want 401", w.Code)
	}
	if calls := h.log.all(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

// TestMeMapsTheFailuresOfTheAccountStore holds that a store that cannot answer is
// a 503 and not an empty profile, and that an account that is no longer there is
// the plain 401 of an identity that no longer stands for anything.
func TestMeMapsTheFailuresOfTheAccountStore(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{authn.ErrUnavailable, http.StatusServiceUnavailable},
		{errors.New("driver: broken pipe"), http.StatusServiceUnavailable},
		{authn.ErrAccountNotFound, http.StatusUnauthorized},
	} {
		h := newHarness(t)
		h.accounts.err = c.err
		w := h.get("/me")
		if w.Code != c.want {
			t.Errorf("GET /me with %v = %d, want %d", c.err, w.Code, c.want)
		}
		if strings.Contains(w.Body.String(), "broken pipe") {
			t.Errorf("the cause is in the answer: %s", w.Body)
		}
	}
}
