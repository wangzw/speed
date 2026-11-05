package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

// memberPage is one member listing as the wire carries it, read as a map so that
// a case about a field the view does not carry yet fails on the answer rather
// than on the package not compiling.
type memberPage struct {
	body    map[string]any
	members []map[string]any
}

// readMemberPage decodes a member listing into its members and the page envelope.
func readMemberPage(t *testing.T, body []byte) memberPage {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decoding the member list: %v", err)
	}
	raw, ok := doc["members"].([]any)
	if !ok {
		t.Fatalf("the member list carries members = %T, want an array", doc["members"])
	}
	members := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		member, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a member row is %T, want an object", item)
		}
		members = append(members, member)
	}
	return memberPage{body: doc, members: members}
}

// memberByAccount finds one member row in a page.
func (p memberPage) memberByAccount(t *testing.T, account string) map[string]any {
	t.Helper()
	for _, member := range p.members {
		if member["account"] == account {
			return member
		}
	}
	t.Fatalf("the member %q is missing from the page", account)
	return nil
}

// TestTheMemberViewCarriesRolesAndJoinedAt holds that a member row tells the
// administration face which roles the account holds and when it joined. The
// roles are the tenant's role names in ascending order and always an array; the
// join instant is the membership's creation instant in UTC.
func TestTheMemberViewCarriesRolesAndJoinedAt(t *testing.T) {
	r := newRig(t)
	joined := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	r.members.put(authn.Membership{
		Account: userID, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin, CreatedAt: joined,
	})

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
	roles, ok := member["roles"].([]any)
	if !ok {
		t.Fatalf("the member carries roles = %T, want an array", member["roles"])
	}
	if len(roles) != 0 {
		t.Errorf("the member carries roles %v, want the empty array", roles)
	}
	rawJoined, ok := member["joined_at"].(string)
	if !ok {
		t.Fatalf("the member carries joined_at = %T, want a timestamp", member["joined_at"])
	}
	gotJoined, err := time.Parse(time.RFC3339, rawJoined)
	if err != nil {
		t.Fatalf("parsing joined_at %q: %v", rawJoined, err)
	}
	if !gotJoined.Equal(joined) {
		t.Errorf("joined_at = %s, want %s", gotJoined, joined)
	}
}

// TestTheMemberListAnswersANextCursor holds the page envelope: a full page names
// the cursor of the next one, and the last page names none.
func TestTheMemberListAnswersANextCursor(t *testing.T) {
	r := newRig(t)
	for _, id := range []authn.AccountID{"member-a", "member-b", "member-c"} {
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	}

	first := r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?limit=2", nil, fresh()...)
	if first.Code != http.StatusOK {
		t.Fatalf("the first page: status = %d, want 200; body %s", first.Code, first.Body.String())
	}
	page := readMemberPage(t, first.Body.Bytes())
	if len(page.members) != 2 {
		t.Fatalf("the first page holds %d members, want 2", len(page.members))
	}
	next, ok := page.body["next_cursor"].(string)
	if !ok {
		t.Fatalf("the page carries next_cursor = %T, want a string", page.body["next_cursor"])
	}
	if next != "member-b" {
		t.Errorf("next_cursor = %q, want %q", next, "member-b")
	}

	second := r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?limit=2&after="+next, nil, fresh()...)
	if second.Code != http.StatusOK {
		t.Fatalf("the second page: status = %d, want 200; body %s", second.Code, second.Body.String())
	}
	page = readMemberPage(t, second.Body.Bytes())
	if len(page.members) != 1 || page.members[0]["account"] != "member-c" {
		t.Fatalf("the second page holds %v, want member-c alone", page.members)
	}
	if got, _ := page.body["next_cursor"].(string); got != "" {
		t.Errorf("the last page names next_cursor %q, want none", got)
	}
}

// TestTheMemberListProbesForAnotherPageAtTheMaximumSize holds the second probe
// the page needs at the store's ceiling: the read cannot ask for one row more
// than the maximum, so a full page is asked again for a single row past its last
// account.
func TestTheMemberListProbesForAnotherPageAtTheMaximumSize(t *testing.T) {
	r := newRig(t)
	for i := 0; i <= authn.MaxPageLimit; i++ {
		id := authn.AccountID(fmt.Sprintf("member-%05d", i))
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	}

	rec := r.do(http.MethodGet, fmt.Sprintf("/api/v1/tenants/%s/members?limit=%d", tenantB, authn.MaxPageLimit), nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	page := readMemberPage(t, rec.Body.Bytes())
	if len(page.members) != authn.MaxPageLimit {
		t.Fatalf("the page holds %d members, want %d", len(page.members), authn.MaxPageLimit)
	}
	if got, _ := page.body["next_cursor"].(string); got == "" {
		t.Error("the full page names no next_cursor, want the cursor of the next page")
	}
	if r.members.listCalls != 2 {
		t.Errorf("the membership read ran %d time(s), want the page read and one probe", r.members.listCalls)
	}
}

// TestTheMemberListNamesNoCursorOnTheLastFullPage holds the other side of the
// probe: a page that fills the ceiling exactly and has nothing after it names no
// cursor, so a client stops instead of asking for an empty page.
func TestTheMemberListNamesNoCursorOnTheLastFullPage(t *testing.T) {
	r := newRig(t)
	for i := 0; i < authn.MaxPageLimit; i++ {
		id := authn.AccountID(fmt.Sprintf("member-%05d", i))
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	}

	rec := r.do(http.MethodGet, fmt.Sprintf("/api/v1/tenants/%s/members?limit=%d", tenantB, authn.MaxPageLimit), nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	page := readMemberPage(t, rec.Body.Bytes())
	if got, _ := page.body["next_cursor"].(string); got != "" {
		t.Errorf("the last page names next_cursor %q, want none", got)
	}
}

// TestTheMemberWriteRefusesSuspendingTheCaller holds the rule that an
// administrator cannot suspend their own membership: a tenant with at least one
// active administrator stays governable, while another administrator may suspend
// or remove them. The refusal is a 409 of this capability's own code and no
// membership write is made.
func TestTheMemberWriteRefusesSuspendingTheCaller(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(adminID),
		map[string]any{"active": false}, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("suspending the caller: status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if got, want := problemType(t, rec.Body.String()), "urn:speed:authn:cannot-suspend-self"; got != want {
		t.Errorf("problem type = %q, want %q", got, want)
	}
	if len(r.members.puts) != 0 {
		t.Errorf("a refused self-suspension wrote %+v, want no membership write", r.members.puts)
	}
}

// TestTheMemberWriteSuspendsAnotherMember holds that the refusal is about the
// caller alone: suspending somebody else still writes the membership.
func TestTheMemberWriteSuspendsAnotherMember(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(userID),
		map[string]any{"active": false}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("suspending another member: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if len(r.members.puts) != 1 || r.members.puts[0].Active {
		t.Errorf("the membership write = %+v, want one suspension", r.members.puts)
	}
}

// TestTheMemberListReadsTheMembersRolesInOneBulkCall holds the seam's shape: one
// call answers the whole page, and the view sorts the names ascending even when
// an implementation answers another order.
func TestTheMemberListReadsTheMembersRolesInOneBulkCall(t *testing.T) {
	r := newRig(t)
	r.roles.roles = map[authn.AccountID][]string{userID: {"zulu", "alpha"}}

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
	raw, ok := member["roles"].([]any)
	if !ok {
		t.Fatalf("the member carries roles = %T, want an array", member["roles"])
	}
	got := make([]string, 0, len(raw))
	for _, name := range raw {
		got = append(got, fmt.Sprint(name))
	}
	if want := []string{"alpha", "zulu"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the member carries roles %v, want %v", got, want)
	}
	if n := r.roles.seen(); n != 1 {
		t.Errorf("the role read ran %d time(s), want one bulk call for the page", n)
	}
}

// TestTheMemberListAnswersWithoutRolesWhenTheSeamIsAbsent holds the optional
// dependency: an assembly with no role reader still lists members, with empty
// role arrays.
func TestTheMemberListAnswersWithoutRolesWhenTheSeamIsAbsent(t *testing.T) {
	r := newRig(t, noMemberRoles)
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
	roles, ok := member["roles"].([]any)
	if !ok || len(roles) != 0 {
		t.Errorf("the member carries roles = %v, want the empty array", member["roles"])
	}
}

// TestTheMemberListDegradesWhenTheRoleReadFails holds the failure direction: a
// policy store that cannot answer costs the listing its roles, not its members.
func TestTheMemberListDegradesWhenTheRoleReadFails(t *testing.T) {
	r := newRig(t)
	r.roles.err = errors.New("the policy store is down")

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
	if roles, ok := member["roles"].([]any); !ok || len(roles) != 0 {
		t.Errorf("the member carries roles = %v, want the empty array", member["roles"])
	}
	if logs := r.logs.String(); !strings.Contains(logs, "reading the members' roles failed") {
		t.Errorf("the failed role read left no warning; log: %s", logs)
	}
}
