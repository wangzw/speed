package dbsuite

import (
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// RequireOptionalLogging holds a database module's descriptor to the one
// requirement such a module declares: the log capability, optional. which names
// the descriptor in a failure.
//
// Optional, because a database has to start in an assembly that never took
// logging in; declared, because an assembly that did take it in has to have it
// constructed ahead of the database, and nothing but the declaration orders the
// two. A descriptor that drops the requirement, names another capability,
// requires the log capability outright or declares one more beside it fails.
//
// Every failure is reported, so a descriptor that is wrong in two ways says so
// in one run; only a wrong count stops the check, because there is then no one
// requirement to look at.
func RequireOptionalLogging(t testing.TB, which string, module core.Module) {
	t.Helper()
	if len(module.Requires) != 1 {
		t.Fatalf("%s declares %d requirements, and it requires the log capability alone: %+v",
			which, len(module.Requires), module.Requires)
	}
	requirement := module.Requires[0]
	if want := reflect.TypeFor[*log.Logger](); reflect.TypeOf(requirement.Token) != want {
		t.Errorf("%s requires %T, and the capability it takes up is %v", which, requirement.Token, want)
	}
	if !requirement.Optional {
		t.Errorf("%s requires the log capability outright, so a host that took no logging in could not "+
			"start its database", which)
	}
}
