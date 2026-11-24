package dbsuite

import (
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// RequireOptionalLogging holds a database module's descriptor to the one
// requirement such a module declares: the log capability, optional; which names
// the descriptor in a failure.
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
