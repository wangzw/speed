package persist

import (
	"strings"
	"testing"

	casbinmodel "github.com/casbin/casbin/v3/model"

	"github.com/vislake/speed/pkg/authz"
)

// pRuleOfTwoFields and gRuleOfFourFields are model texts whose p and g rules are
// deliberately of the wrong arity, so that loading a row of the adapter's shape
// into the model fails (a p rule is checked for a size other than its own, a g
// rule for one shorter than its own). They exist to drive LoadPolicy's failure
// path; the model the process enforces with is the root package's and is not
// this shape.
const pRuleOfTwoFields = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom
`

const gRuleOfFourFields = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom, r.obj) && r.dom == p.dom && r.obj == p.obj && r.act == p.act
`

// A rule that does not carry one field per column of its table is refused.
// Accepting one would write a row from the fields it happens to hold and drop
// the rest without a word.
func TestARuleOfTheWrongLengthIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		adapter := New(t.Context(), handle, "acme")

		cases := []struct {
			name  string
			ptype string
			rule  []string
		}{
			{name: "a p rule with a field too many", ptype: "p", rule: []string{"roleA", "acme", "account", "read", "extra"}},
			{name: "a p rule with a field too few", ptype: "p", rule: []string{"roleA", "acme", "account"}},
			{name: "a g rule with a field too many", ptype: "g", rule: []string{"acc1", "roleA", "acme", "extra"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if err := adapter.AddPolicy(c.ptype, c.ptype, c.rule); err == nil {
					t.Errorf("AddPolicy wrote %s", c.name)
				}
			})
		}
		if n := count(t, handle, "authz_grants") + count(t, handle, "authz_bindings"); n != 0 {
			t.Errorf("the refused writes left %d row(s), want none", n)
		}
	})
}

// A policy type the adapter has no table for is refused as such, on both the
// write and the filtered removal. The refusal names the missing table rather
// than some other misreading of the call.
func TestAnUnknownPolicyTypeIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		adapter := New(t.Context(), handle, "acme")

		err := adapter.AddPolicy("x", "x", []string{"a", "b"})
		if err == nil {
			t.Fatal("AddPolicy accepted a policy type with no table")
		}
		if !strings.Contains(err.Error(), "no table") {
			t.Errorf("AddPolicy's refusal %q does not name the missing table", err.Error())
		}

		err = adapter.RemoveFilteredPolicy("x", "x", 0, "a")
		if err == nil {
			t.Fatal("RemoveFilteredPolicy accepted a policy type with no table")
		}
		if !strings.Contains(err.Error(), "no table") {
			t.Errorf("RemoveFilteredPolicy's refusal %q does not name the missing table", err.Error())
		}
	})
}

// A rule that cannot be loaded is reported by the policy type it belongs to and
// not by its values: the error names the p or g rule, never the role, the
// tenant, the resource or the action it carries. The caller logs this error, and
// a policy value reaches no log line.
func TestLoadPolicyNamesTheRuleAndNotItsValues(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		for _, c := range []struct {
			name string
			text string
			rule string
			old  string
		}{
			{name: "a p rule", text: pRuleOfTwoFields, rule: "authz/persist: loading a p rule", old: "loading the p rule ["},
			{name: "a g rule", text: gRuleOfFourFields, rule: "authz/persist: loading a g rule", old: "loading the g rule ["},
		} {
			t.Run(c.name, func(t *testing.T) {
				m, err := casbinmodel.NewModelFromString(c.text)
				if err != nil {
					t.Fatalf("building the model: %v", err)
				}
				err = New(t.Context(), handle, "").LoadPolicy(m)
				if err == nil {
					t.Fatalf("loading into %s of another arity gave no error", c.name)
				}
				if !strings.Contains(err.Error(), c.rule) {
					t.Errorf("the error %q does not name %s", err.Error(), c.name)
				}
				if strings.Contains(err.Error(), c.old) {
					t.Errorf("the error %q repeats the rule's own values", err.Error())
				}
			})
		}
	})
}

// A read that fails is reported with the read it was, so the failure can be
// told from a load of the other table.
func TestLoadPolicyNamesAReadFailure(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)
		pool, err := handle.DB()
		if err != nil {
			t.Fatalf("reaching the handle's pool: %v", err)
		}
		if err = pool.Close(); err != nil {
			t.Fatalf("closing the pool: %v", err)
		}

		m, err := casbinmodel.NewModelFromString(authz.ModelText)
		if err != nil {
			t.Fatalf("building the model: %v", err)
		}
		err = New(t.Context(), handle, "").LoadPolicy(m)
		if err == nil {
			t.Fatal("a load through a closed pool returned no error")
		}
		if !strings.Contains(err.Error(), "authz/persist: loading the grants") {
			t.Errorf("the error %q does not name the read that failed", err.Error())
		}
	})
}
