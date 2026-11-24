package policy

import (
	"strings"
	"testing"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"

	"github.com/vislake/speed/pkg/authz"
)

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

// TestAllowedCarriesTheEnginesFailure checks that allowed carries the engine's failure.
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

// TestLoadRefusesAMalformedModel checks that load refuses a malformed model.
func TestLoadRefusesAMalformedModel(t *testing.T) {
	snapshot, err := Load(t.Context(), openHandle(t), "this is not a model")
	if err == nil {
		t.Fatalf("Load accepted a malformed model and returned %v", snapshot)
	}
	if snapshot != nil {
		t.Errorf("Load returned a snapshot %v beside the error", snapshot)
	}
}

// TestSelfCheckRefusesAModelItCannotRun checks that selfCheck refuses a model it cannot run.
func TestSelfCheckRefusesAModelItCannotRun(t *testing.T) {
	err := SelfCheck("this is not a model")
	if err == nil {
		t.Fatal("SelfCheck accepted a model text it cannot run")
	}
	if n := strings.Count(err.Error(), "could not run"); n != len(fixtures) {
		t.Errorf("SelfCheck reported %d fixture(s) that could not run, want all %d", n, len(fixtures))
	}
}
