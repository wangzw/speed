// Package logdeps carries the descriptor-level cases about the logging
// dependency: a module that takes a logger out of the registry has to declare
// the requirement on the logging capability.
//
// The cases read module descriptors and run no host, which is the only shape
// this requirement has. A module that took a logger without declaring the
// requirement is handed one all the same, as long as some other module in the
// run declared it — one declaration anywhere orders the logging module ahead of
// every construction and behind every shutdown — so the omission leaves no
// trace in a run until the module that carries the declaration is removed, and
// then it surfaces as a startup failure in a module nobody changed. What the
// omission costs meanwhile is invisible too: a module constructed before the
// logging module resolves no logger and falls back to the process default
// logger, which writes to standard output and bypasses the configured
// destination.
//
// The modules are listed by hand because nothing in a descriptor says a module
// logs: what a module resolves is inside its New or its Init. A module added
// later that both omits the declaration and is not listed here passes silently,
// which is the price the design records for the one assertion this requirement
// has.
package logdeps

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/admin"
	"github.com/vislake/speed/pkg/authn/impersonation"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mfa/webauthn"
	"github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/authn/sms"
	"github.com/vislake/speed/pkg/authn/sso"
	"github.com/vislake/speed/pkg/authn/sso/saml"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/authn/token"
	"github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant/admission"
	"github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

// modulesThatTakeALogger are the modules that resolve the logging capability in
// their New or their Init and hold a Named logger out of it. The list is
// written out because a descriptor says nothing about what a module resolves:
// whether a module holds a logger is inside its callbacks.
func modulesThatTakeALogger() []core.Module {
	return []core.Module{
		resolve.Module(),
		catalog.Module(),
		admission.Module(),
		settings.Module(),
		token.Module(),
		sms.Module(),
		admin.Module(),
		mfa.Module(),
		webauthn.Module(),
		sso.Module(),
		saml.Module(),
		mount.Module(),
		account.Module(),
		impersonation.Module(),
		login.Module(),
		session.Module(),
		static.Module(),
		policy.Module(),
	}
}

// modulesMissingTheLoggingDependency returns the names of the modules that
// declare no requirement on the logging capability. It is factored out of the
// case below so that a fabricated descriptor can be held to it, which is what
// keeps the case from passing over an empty list or over a check that reports
// nothing.
func modulesMissingTheLoggingDependency(modules []core.Module) []string {
	var missing []string
	for _, module := range modules {
		if !declaresLoggingDependency(module) {
			missing = append(missing, module.Name)
		}
	}
	return missing
}

// declaresLoggingDependency reports whether a descriptor declares a requirement
// on the logging capability. A token is a typed nil pointer, so comparing two
// of them compares the capability each designates.
func declaresLoggingDependency(module core.Module) bool {
	for _, requirement := range module.Requires {
		if requirement.Token == (*log.Logger)(nil) {
			return true
		}
	}
	return false
}

// TestEveryModuleThatTakesALoggerDeclaresTheLoggingDependency is the case
// design-log.md names: a module that takes a logger out of the registry
// declares the requirement, which is what orders the logging module ahead of
// its construction and behind its shutdown.
//
// The declaration is optional and not required: a module takes the logging
// capability up only when it is present, so a command that never names the
// logging module is a legal assembly the logger's absence does not fail. The
// optional declaration orders the two modules exactly as a required one does
// (see core), which is the whole of what this case states — a required one
// would make every such command have to name the logging module, changing the
// assembly contract.
func TestEveryModuleThatTakesALoggerDeclaresTheLoggingDependency(t *testing.T) {
	t.Parallel()
	modules := modulesThatTakeALogger()
	if len(modules) == 0 {
		t.Fatal("no module is listed, so this case checks nothing")
	}
	for _, name := range modulesMissingTheLoggingDependency(modules) {
		t.Errorf("module %q takes its logger out of the registry and declares no requirement on the %T "+
			"capability, so nothing orders the logging module around it; without the declaration the module "+
			"may be constructed before logging and fall back to the process default logger, which writes to "+
			"standard output and bypasses the configured destination",
			name, (*log.Logger)(nil))
	}
}

// otherCapability is a typed nil pointer to an interface that designates no
// capability any module delivers. It gives a fabricated descriptor a
// requirement that is not the logging one, so that the fabricated module has to
// be reported and a real one does not.
type otherCapability interface{ otherCapability() }

// TestTheLoggingDependencyCaseFlagsAViolation is the control for the case above.
// It holds a fabricated descriptor that declares no logging requirement to the
// same check and requires that it be reported, so a green run of the case above
// is not a run of a check that reports nothing over an empty list. The second
// half is the discrimination: a descriptor that does carry the requirement,
// optional like the real ones, is not reported.
func TestTheLoggingDependencyCaseFlagsAViolation(t *testing.T) {
	t.Parallel()
	violating := core.Module{
		Name:     "fabricated.module.that.takes.a.logger",
		Requires: []core.Requirement{{Token: (*otherCapability)(nil)}},
	}
	if got := modulesMissingTheLoggingDependency([]core.Module{violating}); len(got) != 1 || got[0] != violating.Name {
		t.Errorf("the check did not report the module %q, which declares no logging requirement: got %v",
			violating.Name, got)
	}
	compliant := core.Module{
		Name: "fabricated.module.with.the.requirement",
		Requires: []core.Requirement{
			{Token: (*otherCapability)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
	}
	if got := modulesMissingTheLoggingDependency([]core.Module{compliant}); len(got) != 0 {
		t.Errorf("the check reported the module %q, which declares the logging requirement: got %v",
			compliant.Name, got)
	}
}
