package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheMemberViewCarriesTheAccountDisplayName holds that a member row carries
// the account's display name, and the empty string when the account has none.
// The member read is the only identity source of the workspace administration
// face, which does not read the platform account surface.
func TestTheMemberViewCarriesTheAccountDisplayName(t *testing.T) {
	r := newRig(t)
	r.accounts.put(userID, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive, DisplayName: "Ada Lovelace",
	})
	noName := authn.AccountID("member-without-name")
	r.accounts.put(noName, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.members.put(authn.Membership{
		Account: noName, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin,
	})
	// A membership row whose account is gone by the time the page resolves the
	// name: the row still answers, with the empty name.
	gone := authn.AccountID("member-without-account")
	r.members.put(authn.Membership{
		Account: gone, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin,
	})

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var got AuthnAdminMemberList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the member list: %v", err)
	}
	byAccount := map[string]AuthnAdminMember{}
	for _, member := range got.Members {
		byAccount[member.Account] = member
	}
	if name := byAccount[string(userID)].DisplayName; name != "Ada Lovelace" {
		t.Errorf("the member %q carries display_name %q, want the account's display name", userID, name)
	}
	if name, ok := byAccount[string(noName)]; !ok {
		t.Fatalf("the member %q is missing from the list", noName)
	} else if name.DisplayName != "" {
		t.Errorf("the member %q carries display_name %q, want the empty string", noName, name.DisplayName)
	}
	if name, ok := byAccount[string(gone)]; !ok {
		t.Fatalf("the member %q is missing from the list", gone)
	} else if name.DisplayName != "" {
		t.Errorf("the member %q carries display_name %q, want the empty string", gone, name.DisplayName)
	}
}

// TestTheMemberListRefusesANonUTF8Query holds that the query parameter is
// text: a byte sequence that is not valid UTF-8 is a 400 and the membership
// read is never made, rather than a filter the read hands to the database,
// which rejects it and answers an outage instead of the shape error the caller
// caused.
func TestTheMemberListRefusesANonUTF8Query(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?query=%BF", "?query=caf%C3", "?query=%FF%FE"} {
		rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members"+q, nil, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if r.members.listCalls != 0 {
		t.Errorf("the member read ran %d time(s) on a non-UTF-8 query, want no read", r.members.listCalls)
	}

	// A query that is valid UTF-8 text reaches the read as written.
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members?query=Ada", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid query: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := r.members.lastQuery.Query; got != "Ada" {
		t.Errorf("the membership read received query %q, want %q", got, "Ada")
	}
}

// TestTheMemberListHandsTheQueryToTheMembershipRead holds that the list route
// parses its optional query parameter and hands it to the membership read. The
// member picker of the workspace surface drives this read.
func TestTheMemberListHandsTheQueryToTheMembershipRead(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members?query=Ada", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := r.members.lastQuery.Query; got != "Ada" {
		t.Errorf("the membership read received query %q, want %q", got, "Ada")
	}
}
