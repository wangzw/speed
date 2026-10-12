package policy

import (
	"fmt"
	"strings"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
)

// The fixed facts the self-check is built from. The names are the fixtures'
// own; nothing in the module matches on them.
const (
	checkDomainA = "selfcheck-a"
	checkDomainB = "selfcheck-b"
	checkRole    = "selfcheck-role"
	checkAccount = "selfcheck-account"
	checkObject  = "account"
	checkAction  = "read"
)

// fixture is one assertion about the model's behaviour: the policy rows it is
// given and the query it puts, with the answer the model must give.
//
// The rows are written directly into a fresh in-memory model, never through the
// store: the self-check is about the shape of the model text, not about what
// any database holds.
type fixture struct {
	name     string
	grants   [][4]string // role, domain, object, action
	bindings [][3]string // account, role, domain
	query    [4]string   // account, domain, object, action

	// again asks the query a second time and holds the second answer to the
	// first: it is the fixture for the decision being a pure function of the
	// model and the query, with nothing carried between two calls.
	again bool

	want bool
}

// fixtures are the whole self-check. Each case turns on a clause a wrong model
// text silently changes: the pair of domain cases catches a matcher missing the
// subject's domain argument and one missing the policy's, the object and action
// cases catch a field compared by anything but equality, the no-binding case
// catches a matcher that allows in the absence of a match, and the repeat case
// catches a decision that is not a pure function.
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
// the enforcement path depends on.
//
// A model text that is wrong does not fail to load; it evaluates to true or
// false where the design says the opposite, and nothing downstream would say
// so. The self-check is what stands in for that: it runs on the exact text the
// process will enforce with, before the process serves a request, and a
// mismatch fails the startup.
//
// Every fixture that disagrees is reported, one line each, rather than stopping
// at the first: a text with several wrong clauses names them all in one run.
// The line names the fixture and the answer the model gave, so the clause to
// fix is where a reader looks.
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

// runFixture evaluates one fixture on a fresh model, so that no case's rows
// reach another's. A repeat fixture runs its query a second time and holds the
// second answer to the first.
func runFixture(text string, f fixture) (bool, error) {
	model, err := casbinmodel.NewModelFromString(text)
	if err != nil {
		return false, err
	}
	enforcer, err := casbin.NewEnforcer(model)
	if err != nil {
		return false, err
	}
	// The fixtures are written into the model alone: there is no store to
	// persist through, and auto-save is left off so the rows stay in memory.
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
