package demotest

import (
	"slices"
	"testing"
)

// TestChildEnvForwardsOnlyTheListedVariables checks that a listed variable
// reaches the child and one the host exports outside the list does not.
func TestChildEnvForwardsOnlyTheListedVariables(t *testing.T) {
	t.Setenv("TREECHECK_ENV_PROBE", "outside")
	t.Setenv("LANG", "C.UTF-8")
	env := ChildEnv()
	if slices.Contains(env, "TREECHECK_ENV_PROBE=outside") {
		t.Errorf("ChildEnv forwarded TREECHECK_ENV_PROBE, which its list does not name")
	}
	if !slices.Contains(env, "LANG=C.UTF-8") {
		t.Errorf("ChildEnv did not forward LANG=C.UTF-8, which its list names")
	}
}
