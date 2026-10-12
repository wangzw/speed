package main

import (
	"os"
	"strings"
	"testing"
)

// TestNoSelfWrittenRoleDecisionRemains is the case for the host's route
// permissions having replaced its self-written role checks: no non-test source
// names a handler-side role gate a route ran before it declared a permission. A
// route's permission is decided by the authorization layer's middleware from the
// declaration surface, not by a role a handler compares, so the least-role
// helpers and the deal and administration gates they carried are gone. The
// companion case in fixture_test.go holds the removed decision seam and its
// module to the same reading; this one adds the deal and administration gates
// that compared roles by hand.
func TestNoSelfWrittenRoleDecisionRemains(t *testing.T) {
	t.Parallel()
	gone := []string{
		"func memberWith(", "func roleHere(", "func roleIn(",
		"func canWriteDeals(", "func canAdminister(",
	}
	for _, name := range nonTestSources(t, ".") {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, text := range gone {
			if strings.Contains(string(body), text) {
				t.Errorf("%s still names %q, a handler-side role check the route declarations replaced", name, text)
			}
		}
	}
}

// TestTheMembersModuleIssuesNoStatementOnTheLegacyRoleTable is the case for the
// host's member role having moved off its own table: the members module lists no
// model, so none of its handlers issues a statement on member_roles. The former
// table is declared by the role-import command alone, which reads it once to
// carry each row into the policy; a serving module that listed the model would be
// one whose handlers read or wrote the role table the authorization policy
// replaced. A members module that still listed MemberRole would fail here.
func TestTheMembersModuleIssuesNoStatementOnTheLegacyRoleTable(t *testing.T) {
	t.Parallel()
	found := false
	for _, a := range applications {
		if a.module.Name != membersModuleName {
			continue
		}
		found = true
		for _, model := range a.models {
			if _, ok := model.(MemberRole); ok {
				t.Errorf("the members module lists the MemberRole model, so one of its handlers issues a " +
					"statement on member_roles, the table the authorization policy replaced")
			}
		}
	}
	if !found {
		t.Fatalf("no application module is registered under %q", membersModuleName)
	}
}
