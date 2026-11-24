package policy

import (
	"fmt"
	"strings"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
)

const (
	checkDomainA = "selfcheck-a"
	checkDomainB = "selfcheck-b"
	checkRole    = "selfcheck-role"
	checkAccount = "selfcheck-account"
	checkObject  = "account"
	checkAction  = "read"
)

type fixture struct {
	name     string
	grants   [][4]string
	bindings [][3]string
	query    [4]string

	again bool

	want bool
}

var fixtures = []fixture{
	{
		name:     "a role's grant reaches the account bound to it in the same domain",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainA}},
		query:    [4]string{checkAccount, checkDomainA, checkObject, checkAction},
		want:     true,
	},
	{
		name:     "a binding in another domain does not resolve the subject's role here",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainB}},
		query:    [4]string{checkAccount, checkDomainA, checkObject, checkAction},
		want:     false,
	},
	{
		name:     "a grant in another domain does not apply to a query in this one",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainB}},
		query:    [4]string{checkAccount, checkDomainB, checkObject, checkAction},
		want:     false,
	},
	{
		name:     "an object other than the grant's is not matched",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainA}},
		query:    [4]string{checkAccount, checkDomainA, "session", checkAction},
		want:     false,
	},
	{
		name:     "an action other than the grant's is not matched",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainA}},
		query:    [4]string{checkAccount, checkDomainA, checkObject, "write"},
		want:     false,
	},
	{
		name:   "an account bound to nothing is refused",
		grants: [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		query:  [4]string{checkAccount, checkDomainA, checkObject, checkAction},
		want:   false,
	},
	{
		name:     "the same query is answered the same way twice",
		grants:   [][4]string{{checkRole, checkDomainA, checkObject, checkAction}},
		bindings: [][3]string{{checkAccount, checkRole, checkDomainA}},
		query:    [4]string{checkAccount, checkDomainA, checkObject, checkAction},
		again:    true,
		want:     true,
	},
}

// SelfCheck builds a model from text and asserts the closed set of behaviours
// the enforcement path depends on. Every fixture that disagrees is reported,
// one line each, and a mismatch fails the startup.
func SelfCheck(text string) error {
	var reports []string
	for _, f := range fixtures {
		got, err := runFixture(text, f)
		if err != nil {
			reports = append(reports, fmt.Sprintf("the fixture %q could not run: %v", f.name, err))
			continue
		}
		if got != f.want {
			reports = append(reports, fmt.Sprintf("the fixture %q answered %t, want %t", f.name, got, f.want))
		}
	}
	if len(reports) == 0 {
		return nil
	}
	return fmt.Errorf("authz/policy: the model text does not behave as the design states: %s",
		strings.Join(reports, "; "))
}

func runFixture(text string, f fixture) (bool, error) {
	model, err := casbinmodel.NewModelFromString(text)
	if err != nil {
		return false, err
	}
	enforcer, err := casbin.NewEnforcer(model)
	if err != nil {
		return false, err
	}
	// The fixtures are written into the model alone, with auto-save off, so no row reaches a store.
	enforcer.EnableAutoSave(false)
	for _, g := range f.grants {
		_, err = enforcer.AddPolicy(g[0], g[1], g[2], g[3])
		if err != nil {
			return false, err
		}
	}
	for _, b := range f.bindings {
		_, err = enforcer.AddGroupingPolicy(b[0], b[1], b[2])
		if err != nil {
			return false, err
		}
	}

	got, err := enforcer.Enforce(f.query[0], f.query[1], f.query[2], f.query[3])
	if err != nil {
		return false, err
	}
	if f.again {
		var second bool
		second, err = enforcer.Enforce(f.query[0], f.query[1], f.query[2], f.query[3])
		if err != nil {
			return false, err
		}
		if second != got {
			return false, fmt.Errorf("the same query answered %t then %t", got, second)
		}
	}
	return got, nil
}
