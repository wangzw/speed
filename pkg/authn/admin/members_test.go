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

// TestTheMemberViewCarriesTheAccountDisplayName checks that a member row carries the account's display name or the empty string.
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

// TestTheMemberViewCarriesTheAccountUsername checks that a member row carries the account's username by kind, or the empty string.
func TestTheMemberViewCarriesTheAccountUsername(t *testing.T) {
	r := newRig(t)
	r.accounts.put(userID, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive, DisplayName: "Ada Lovelace",
	})
	r.accounts.putIdentifiers(userID,
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "ada@example.com"},
		authn.Identifier{Kind: authn.IdentifierUsername, Value: "ada"},
	)
	emailOnly := authn.AccountID("member-with-email-only")
	r.accounts.put(emailOnly, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive, DisplayName: "Email Only",
	})
	r.accounts.putIdentifiers(emailOnly,
		authn.Identifier{Kind: authn.IdentifierEmail, Value: "only@example.com"},
	)
	r.members.put(authn.Membership{
		Account: emailOnly, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin,
	})
	gone := authn.AccountID("member-without-account")
	r.members.put(authn.Membership{
		Account: gone, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin,
	})

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	page := readMemberPage(t, rec.Body.Bytes())
	byAccount := map[string]map[string]any{}
	for _, member := range page.members {
		byAccount[fmt.Sprint(member["account"])] = member
	}
	if got := fmt.Sprint(byAccount[string(userID)]["username"]); got != "ada" {
		t.Errorf("the member %q carries username %q, want the account's username %q", userID, got, "ada")
	}
	if got := fmt.Sprint(byAccount[string(emailOnly)]["username"]); got != "" {
		t.Errorf("the member %q carries username %q, want the empty string: an email is not a username", emailOnly, got)
	}
	if got := fmt.Sprint(byAccount[string(gone)]["username"]); got != "" {
		t.Errorf("the member %q carries username %q, want the empty string", gone, got)
	}

	for account, member := range byAccount {
		if fmt.Sprint(member["username"]) == account {
			t.Errorf("the member %q carries its own account identifier as username", account)
		}
	}

	if got := r.accounts.identifierBatchCalls(); got != 1 {
		t.Errorf("the member page made %d batch identifier reads, want one", got)
	}
	if got := r.accounts.identifierSingleCalls(); got != 0 {
		t.Errorf("the member page made %d per-account identifier reads, want none", got)
	}
}

// TestTheMemberListWhoseIdentifierReadFailsIs503 checks that a page whose usernames cannot be read is a 503, not empty usernames.
func TestTheMemberListWhoseIdentifierReadFailsIs503(t *testing.T) {
	r := newRig(t)
	r.accounts.failIdentifiers(errors.New("the identifier store is down"))
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("listing members over a failed identifier read: status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"members"`) {
		t.Errorf("the refusal %q carries a member page, want none", rec.Body.String())
	}
}

// TestTheMemberListRefusesANonUTF8Query checks that a query that is not valid UTF-8 is a 400 before the membership read.
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

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members?query=Ada", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid query: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := r.members.lastQuery.Query; got != "Ada" {
		t.Errorf("the membership read received query %q, want %q", got, "Ada")
	}
}

// TestTheMemberListHandsTheQueryToTheMembershipRead holds that the list route parses its optional query parameter and hands it to the membership read.
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

type memberPage struct {
	body    map[string]any
	members []map[string]any
}

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

// TestTheMemberViewCarriesRolesAndJoinedAt holds that a member row tells the administration face which roles the account holds and when it joined.
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

// TestTheMemberViewCarriesNoOwnerField checks that the member list exposes no organization-owner field.
func TestTheMemberViewCarriesNoOwnerField(t *testing.T) {
	r := newRig(t)
	r.members.put(authn.Membership{Account: userID, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin})

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
	for _, key := range []string{"owner", "ownerAccount", "owner_account", "OwnerAccount"} {
		if _, present := member[key]; present {
			t.Errorf("the member view carries %q, want no owner field", key)
		}
	}
}

// TestTheMemberListAnswersANextCursor holds the page envelope: a full page names the cursor of the next one, and the last page names none.
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

// TestTheMemberListProbesForAnotherPageAtTheMaximumSize checks that a full page at the store's ceiling probes for one more row.
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

// TestTheMemberListNamesNoCursorOnTheLastFullPage checks that a full last page names no cursor.
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

// TestTheMemberWriteRefusesSuspendingTheCaller checks that an administrator cannot suspend their own membership.
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

// TestTheMemberWriteSuspendsAnotherMember holds that the refusal is about the caller alone: suspending somebody else still writes the membership.
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

// TestTheMemberListReadsTheMembersRolesInOneBulkCall checks that one bulk role read answers the page, sorted ascending.
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

// TestTheMemberListAnswersWithoutRolesWhenTheSeamIsAbsent checks that an assembly with no role reader lists members with empty role arrays.
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

// TestTheMemberListDegradesWhenTheRoleReadFails holds the failure direction: a policy store that cannot answer costs the listing its roles, not its members.
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

func pageAccounts(p memberPage) []string {
	out := make([]string, 0, len(p.members))
	for _, member := range p.members {
		out = append(out, fmt.Sprint(member["account"]))
	}
	return out
}

// TestTheMemberListFiltersByRoleExactly checks the role filter's exact match, its empty value and an unknown role.
func TestTheMemberListFiltersByRoleExactly(t *testing.T) {
	r := newRig(t)
	for _, id := range []authn.AccountID{"member-a", "member-b", "member-c", "member-d"} {
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	}
	r.roles.roles = map[authn.AccountID][]string{
		"member-a": {"admin"},
		"member-b": {"member"},
		"member-c": {"admin", "auditor"},
		"member-d": {"administrators"},
	}

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?role=admin", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtering by role: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	page := readMemberPage(t, rec.Body.Bytes())
	if got, want := strings.Join(pageAccounts(page), ","), "member-a,member-c"; got != want {
		t.Errorf("the page holds %q, want %q: the role filter is exact", got, want)
	}
	if got, _ := page.body["next_cursor"].(string); got != "" {
		t.Errorf("the page names next_cursor %q, want none", got)
	}

	for _, role := range []string{"admi", "administrator"} {
		rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?role="+role, nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("role %q: status = %d, want 200; body %s", role, rec.Code, rec.Body.String())
		}
		if got := pageAccounts(readMemberPage(t, rec.Body.Bytes())); len(got) != 0 {
			t.Errorf("role %q selected %v, want nobody", role, got)
		}
	}

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?role=no-such-role", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("an unknown role: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	page = readMemberPage(t, rec.Body.Bytes())
	if got := pageAccounts(page); len(got) != 0 {
		t.Errorf("an unknown role selected %v, want nobody", got)
	}
	if _, ok := page.body["members"].([]any); !ok {
		t.Errorf("an unknown role's answer carries members = %T, want an array", page.body["members"])
	}

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?role=", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("an empty role: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got, want := strings.Join(pageAccounts(readMemberPage(t, rec.Body.Bytes())), ","),
		"member-a,member-b,member-c,member-d"; got != want {
		t.Errorf("an empty role selected %q, want every member %q", got, want)
	}
}

// TestTheMemberListPagesThroughRoleMatches checks that walking a role-filtered list reaches every match once.
func TestTheMemberListPagesThroughRoleMatches(t *testing.T) {
	r := newRig(t)
	admins := map[authn.AccountID]bool{}
	r.roles.roles = map[authn.AccountID][]string{}
	for i := 1; i <= 8; i++ {
		id := authn.AccountID(fmt.Sprintf("member-%02d", i))
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
		if i%2 == 0 {
			admins[id] = true
			r.roles.roles[id] = []string{"admin"}
		} else {
			r.roles.roles[id] = []string{"member"}
		}
	}

	var walked []string
	after := ""
	for pages := 0; ; pages++ {
		if pages > len(admins)+1 {
			t.Fatalf("the walk did not end; walked %v", walked)
		}
		target := "/api/v1/tenants/" + tenantB + "/members?role=admin&limit=2"
		if after != "" {
			target += "&after=" + after
		}
		rec := r.do(http.MethodGet, target, nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("page after %q: status = %d, want 200; body %s", after, rec.Code, rec.Body.String())
		}
		page := readMemberPage(t, rec.Body.Bytes())
		accounts := pageAccounts(page)
		if len(accounts) > 2 {
			t.Fatalf("the page holds %v, want at most 2 members", accounts)
		}
		for _, account := range accounts {
			if !admins[authn.AccountID(account)] {
				t.Errorf("the page carries %q, which does not hold the filtered role", account)
			}
		}
		walked = append(walked, accounts...)
		after, _ = page.body["next_cursor"].(string)
		if after == "" {
			break
		}
		if last := accounts[len(accounts)-1]; after != last {
			t.Fatalf("next_cursor = %q, want the last matching account %q", after, last)
		}
	}

	want := []string{"member-02", "member-04", "member-06", "member-08"}
	if got := strings.Join(walked, ","); got != strings.Join(want, ",") {
		t.Errorf("walking the filtered pages reached %q, want %q: no match is skipped or repeated", got, want)
	}
}

// TestTheMemberListCombinesTheRoleFilterWithTheQuery checks that the role filter and the text query compose.
func TestTheMemberListCombinesTheRoleFilterWithTheQuery(t *testing.T) {
	r := newRig(t)
	for _, id := range []authn.AccountID{"member-a", "member-b"} {
		r.members.put(authn.Membership{Account: id, Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	}
	r.roles.roles = map[authn.AccountID][]string{
		"member-a": {"admin"},
		"member-b": {"member"},
	}

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members?role=admin&query=Ada", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtering by role and query: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := pageAccounts(readMemberPage(t, rec.Body.Bytes())); strings.Join(got, ",") != "member-a" {
		t.Errorf("the page holds %v, want member-a alone", got)
	}
	if len(r.members.queries) == 0 {
		t.Fatal("the filtered walk made no membership read")
	}
	for _, query := range r.members.queries {
		if query.Query != "Ada" {
			t.Errorf("a membership read carried query %q, want %q on every page of the walk", query.Query, "Ada")
		}
	}
	if n := r.roles.seen(); n != 1 {
		t.Errorf("the role read ran %d time(s), want one snapshot for the filter and the view", n)
	}
}

// TestTheMemberListRefusesARoleFilterWithoutTheSeam checks that a role filter without a role reader is a 503.
func TestTheMemberListRefusesARoleFilterWithoutTheSeam(t *testing.T) {
	r := newRig(t, noMemberRoles)

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members?role=admin", nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("filtering by role without a reader: status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"members"`) {
		t.Errorf("the refusal %q carries a members view, want none", rec.Body.String())
	}

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members without a role: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

// TestTheMemberListRefusesARoleFilterWhenTheRoleReadFails checks that a failing role read refuses the filter with a 503.
func TestTheMemberListRefusesARoleFilterWhenTheRoleReadFails(t *testing.T) {
	r := newRig(t)
	r.roles.err = errors.New("the policy store is down")

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members?role=admin", nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("filtering by role with a failing reader: status = %d, want 503; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"members"`) {
		t.Errorf("the refusal %q carries a members view, want none", rec.Body.String())
	}

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing members without a role: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}
