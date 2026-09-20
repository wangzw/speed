package main

import (
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// This case reads the descriptors instead of running the host, which is why it
// is not in e2e_test.go: the property it states leaves no trace in a run.
//
// A module that takes a logger out of the registry without declaring the
// requirement is handed one all the same, as long as some other module in the
// run declared it — one declaration anywhere puts logging ahead of every
// construction and behind every shutdown, so the module with the omission looks
// exactly like the modules without it. The omission only surfaces when the
// module that carries the declaration is removed or stands down, and then it
// surfaces as a startup failure in a module nobody changed. The declaration is
// therefore worth asserting where it is written rather than where it is felt.
func TestEveryModuleThatLogsDeclaresTheLoggingDependency(t *testing.T) {
	// Every module of this host that holds a logger. The list is written out
	// because nothing in a descriptor says a module logs: what a module
	// resolves is inside its New, and the declaration below is the only
	// outward statement of it.
	for _, module := range []core.Module{appModule(), localGreeterModule(), remoteGreeterModule()} {
		if !requiresCapability(module, (*log.Logger)(nil)) {
			t.Errorf("module %q takes its logger from the registry and declares no requirement "+
				"on the %T capability, so nothing orders logging around it",
				module.Name, (*log.Logger)(nil))
		}
	}
}

// The opposite assertion of the one above, and the two do not contradict each
// other: a logger is the product of a module, so a module that takes one has
// to say so and be ordered around the module that makes it, while the reading
// surface is a product the host supplies before the first construction and
// there is no module to be ordered around.
//
// Declaring it would not be a harmless extra line. Nothing in the registry
// delivers the reading capability, so a Requires naming it is a requirement
// with no provider, and the run fails at startup. This case catches the line
// where it is written rather than leaving it to the first run of whichever
// host imported the module.
func TestNoModuleDeclaresTheConfigReaderRequirement(t *testing.T) {
	for _, module := range []core.Module{
		appModule(), localGreeterModule(), remoteGreeterModule(), hostModule(),
	} {
		if requiresCapability(module, (*config.Reader)(nil)) {
			t.Errorf("module %q declares a requirement on the %T capability, which no module "+
				"delivers: the reading surface is supplied by the host before the first "+
				"module is constructed", module.Name, (*config.Reader)(nil))
		}
	}
}

// The host module delivers no capability, which is what keeps "no command
// names it" and "no command outside serve takes it in" the same statement: a
// module is taken into a command by being named there or by providing what a
// named module requires, and a module that provides nothing can only arrive
// the first way. serve is the exception and arrives neither way — it takes in
// every available module, and the ones carrying resources alone are available
// too.
//
// This is asserted here because a run shows nothing of it. The module has no
// New and contributes no argument, so a command that took it in and one that
// did not would produce the same output, and the e2e case that reads the
// identity under greet would go on passing while no longer testing that a
// command which does not take the module in still reads it.
func TestTheHostModuleProvidesNothingThatCouldPullItIntoACommand(t *testing.T) {
	module := hostModule()
	if len(module.Provides) != 0 {
		t.Errorf("module %q declares %d provision(s); a module that delivers a capability is "+
			"taken into every command that requires it, named there or not",
			module.Name, len(module.Provides))
	}
	if module.New != nil {
		t.Errorf("module %q constructs something; it carries declarations that precede all "+
			"configuration and has nothing to build", module.Name)
	}
}

// requiresCapability reports whether a descriptor declares a requirement on a
// capability. A token is a typed nil pointer, so comparing two of them compares
// the capability each designates.
func requiresCapability(module core.Module, capability core.Token) bool {
	for _, requirement := range module.Requires {
		if requirement.Token == capability {
			return true
		}
	}
	return false
}
