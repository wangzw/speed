// Package logdeps carries the descriptor-level cases that a module taking a logger declares the logging requirement.
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

func modulesMissingTheLoggingDependency(modules []core.Module) []string {
	var missing []string
	for _, module := range modules {
		if !declaresLoggingDependency(module) {
			missing = append(missing, module.Name)
		}
	}
	return missing
}

func declaresLoggingDependency(module core.Module) bool {
	for _, requirement := range module.Requires {
		if requirement.Token == (*log.Logger)(nil) {
			return true
		}
	}
	return false
}

// TestEveryModuleThatTakesALoggerDeclaresTheLoggingDependency checks that every module resolving the
// logging capability declares a requirement on it.
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

type otherCapability interface{ otherCapability() }

// TestTheLoggingDependencyCaseFlagsAViolation is the control for the case above.
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
