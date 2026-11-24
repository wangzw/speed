package policy

import (
	"strings"
	"testing"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"

	"github.com/vislake/speed/pkg/authz"
)

// brokenMatcherModel parses, so an enforcer over it is built, but its matcher
// calls a function the model defines nowhere, so every evaluation of it fails.
// It is how a decision reaches the engine's failure path; the model the process
// enforces with is the root package's and has no such clause.
const brokenMatcherModel = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = noSuchFunc(r.sub) && r.dom == p.dom
`

// A decision whose matcher cannot run is a failure, not a denial and not a
// permission.
func TestAllowedCarriesTheEnginesFailure(t *testing.T) {
	m, err := casbinmodel.NewModelFromString(brokenMatcherModel)
	if err != nil {
		t.Fatalf("building the model: %v", err)
	}
	enforcer, err := casbin.NewEnforcer(m)
	if err != nil {
		t.Fatalf("building the enforcer: %v", err)
	}
	snapshot := &Snapshot{enforcer: enforcer}

	allowed, err := snapshot.Allowed(authz.Query{Subject: "acc1", Domain: "acme", Object: "account", Action: "read"})
	if err == nil {
		t.Fatalf("an engine failure came back as %t with no error, want a failure", allowed)
	}
	if allowed {
		t.Error("an engine failure came back as allowed, want a refusal")
	}
}

// A model text that does not parse is refused at load, with the parse
// failure carried out and no snapshot beside it.
func TestLoadRefusesAMalformedModel(t *testing.T) {
	snapshot, err := Load(t.Context(), openHandle(t), "this is not a model")
	if err == nil {
		t.Fatalf("Load accepted a malformed model and returned %v", snapshot)
	}
	if snapshot != nil {
		t.Errorf("Load returned a snapshot %v beside the error", snapshot)
	}
}

// A self-check whose fixtures cannot run reports every one of them and
// refuses the text.
func TestSelfCheckRefusesAModelItCannotRun(t *testing.T) {
	err := SelfCheck("this is not a model")
	if err == nil {
		t.Fatal("SelfCheck accepted a model text it cannot run")
	}
	if n := strings.Count(err.Error(), "could not run"); n != len(fixtures) {
		t.Errorf("SelfCheck reported %d fixture(s) that could not run, want all %d", n, len(fixtures))
	}
}
