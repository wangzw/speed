package admin

import (
	"encoding/json"
	"slices"
	"sort"
	"testing"
)

// TestTheViewFieldSetsArePinned checks that the view field sets are pinned.
func TestTheViewFieldSetsArePinned(t *testing.T) {
	cases := map[string]struct {
		view any
		want []string
	}{
		"permission": {AuthzAdminPermissionView{}, []string{"permission", "description", "group", "dangerous", "grantable"}},
		"role": {AuthzAdminRoleView{}, []string{
			"tenant", "role", "builtin", "description",
			"permission_count", "member_count", "can_manage", "created_at",
		}},
		"grant":   {AuthzAdminGrantView{}, []string{"tenant", "role", "permission", "resource", "action", "created_at"}},
		"binding": {AuthzAdminBindingView{}, []string{"tenant", "account", "role", "granted_at"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(tc.view)
			if err != nil {
				t.Fatalf("marshalling the %s view: %v", name, err)
			}
			got := keysOf(object(t, string(raw)))
			sort.Strings(got)
			want := slices.Clone(tc.want)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Fatalf("the %s view's fields = %v, want %v", name, got, want)
			}
		})
	}
}
