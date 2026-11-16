package authn

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// designSentinels is every sentinel of the root package that the design's two
// failure tables name, the startup table and the runtime table together, written
// out. ErrMalformedHash, the three egress sentinels and the tenant module's
// sentinels are not here: the design puts them in packages of their own.
var designSentinels = map[string]error{
	// startup
	"ErrNoRouter": ErrNoRouter, "ErrNoVerifier": ErrNoVerifier, "ErrClaimConflict": ErrClaimConflict,
	"ErrInvalidClaim": ErrInvalidClaim, "ErrInvalidConfig": ErrInvalidConfig, "ErrUnknownEndpoint": ErrUnknownEndpoint,
	"ErrExemptionUnguarded": ErrExemptionUnguarded, "ErrAccessConflict": ErrAccessConflict,
	"ErrRoutesUnconfigured": ErrRoutesUnconfigured, "ErrRoutesUnguarded": ErrRoutesUnguarded,
	"ErrInsecureConfig": ErrInsecureConfig, "ErrNoEncryptionKey": ErrNoEncryptionKey, "ErrInvalidKey": ErrInvalidKey,
	"ErrNotMigrated": ErrNotMigrated, "ErrMethodConflict": ErrMethodConflict, "ErrFactorConflict": ErrFactorConflict,
	"ErrNoLoginMethod": ErrNoLoginMethod, "ErrNoFactor": ErrNoFactor, "ErrDrainTimeout": ErrDrainTimeout,
	// runtime
	"ErrInvalidAccountID": ErrInvalidAccountID, "ErrAccountNotFound": ErrAccountNotFound, "ErrAccountExists": ErrAccountExists,
	"ErrContactTaken":       ErrContactTaken,
	"ErrInvalidCredentials": ErrInvalidCredentials, "ErrPasswordPolicy": ErrPasswordPolicy,
	"ErrInvalidIdentifier": ErrInvalidIdentifier, "ErrUsernameReserved": ErrUsernameReserved,
	"ErrInvalidArgument": ErrInvalidArgument, "ErrFactorLimit": ErrFactorLimit,
	"ErrIdentifierRequired": ErrIdentifierRequired, "ErrUsernameImmutable": ErrUsernameImmutable,
	"ErrAccountUnavailable": ErrAccountUnavailable,
	"ErrMethodDisabled":     ErrMethodDisabled, "ErrCleanupIncomplete": ErrCleanupIncomplete,
	"ErrSessionRevoked": ErrSessionRevoked, "ErrThrottled": ErrThrottled, "ErrFlowInvalid": ErrFlowInvalid,
	"ErrStepOutOfOrder": ErrStepOutOfOrder, "ErrLinkRequired": ErrLinkRequired, "ErrNotProvisioned": ErrNotProvisioned,
	"ErrSignupClosed": ErrSignupClosed, "ErrContactRequired": ErrContactRequired,
	"ErrPhoneRequired":  ErrPhoneRequired,
	"ErrDomainMismatch": ErrDomainMismatch, "ErrProvisionRefused": ErrProvisionRefused,
	"ErrJITUsernameMissing": ErrJITUsernameMissing, "ErrJITUsernameInvalid": ErrJITUsernameInvalid,
	"ErrJITUsernameTaken": ErrJITUsernameTaken, "ErrJITUsernameStrategy": ErrJITUsernameStrategy,
	"ErrBindingConflict": ErrBindingConflict, "ErrLastLoginMethod": ErrLastLoginMethod, "ErrLastFactor": ErrLastFactor,
	"ErrLastTenantAdmin":         ErrLastTenantAdmin,
	"ErrAccountLastTenantAdmin":  ErrAccountLastTenantAdmin,
	"ErrAccountOwnsOrganization": ErrAccountOwnsOrganization,
	"ErrCannotSuspendSelf":       ErrCannotSuspendSelf,
	"ErrFactorNotEnrolled":       ErrFactorNotEnrolled, "ErrReplay": ErrReplay, "ErrStepUpRequired": ErrStepUpRequired,
	"ErrFactorNotSeasoned": ErrFactorNotSeasoned,
	"ErrFactorFlagged":     ErrFactorFlagged,
	"ErrSessionNotFound":   ErrSessionNotFound, "ErrCSRF": ErrCSRF, "ErrTokenInvalid": ErrTokenInvalid,
	"ErrInvalidGrant": ErrInvalidGrant, "ErrRefreshReuse": ErrRefreshReuse, "ErrNoSigningKey": ErrNoSigningKey,
	"ErrReservedClaim": ErrReservedClaim, "ErrImpersonationRestricted": ErrImpersonationRestricted,
	"ErrImpersonationActive": ErrImpersonationActive, "ErrNotImpersonating": ErrNotImpersonating,
	"ErrImpersonationNeedsSession": ErrImpersonationNeedsSession, "ErrForbidden": ErrForbidden,
	"ErrAuditRequired": ErrAuditRequired, "ErrUnsupportedRegion": ErrUnsupportedRegion,
	"ErrDomainClaimed": ErrDomainClaimed, "ErrAssertionReplay": ErrAssertionReplay,
	"ErrProviderNotFound": ErrProviderNotFound,
	"ErrUnavailable":      ErrUnavailable, "ErrBusy": ErrBusy,
	"ErrRoleUnknown": ErrRoleUnknown, "ErrRoleProtected": ErrRoleProtected,
	"ErrEmailUncertain": ErrEmailUncertain, "ErrHumanVerificationRequired": ErrHumanVerificationRequired,
	"ErrHumanProofTooLong": ErrHumanProofTooLong,
}

// declaredSentinels reads the Err variables the non-test files of the root
// package declare.
func declaredSentinels(t *testing.T) []string {
	t.Helper()
	bp, err := build.Default.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, name := range bp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, s := range gd.Specs {
				for _, n := range s.(*ast.ValueSpec).Names {
					if strings.HasPrefix(n.Name, "Err") && n.IsExported() {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestTheSentinelsAreExactlyTheDesignsTables holds the set of sentinels against
// the tables in both directions: one the design names and the package lacks, and
// one the package declares that no table names. The set is closed over what this
// directory judges for itself.
func TestTheSentinelsAreExactlyTheDesignsTables(t *testing.T) {
	declared := declaredSentinels(t)
	for name := range designSentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("the design names %s and the package does not declare it", name)
		}
	}
	for _, name := range declared {
		if _, ok := designSentinels[name]; !ok {
			t.Errorf("the package declares %s, which neither failure table names", name)
		}
	}
}

// TestEveryDesignSentinelIsDistinctAndSafeToLog holds each sentinel apart from
// the others, under the authn: prefix, with no format verb and no word that says
// it carries a value. An error text reaches logs and, through wraps, responses'
// causes, so what it names is a class and never a value.
func TestEveryDesignSentinelIsDistinctAndSafeToLog(t *testing.T) {
	names := make([]string, 0, len(designSentinels))
	for n := range designSentinels {
		names = append(names, n)
	}
	slices.Sort(names)
	for i, a := range names {
		ea := designSentinels[a]
		if ea == nil {
			t.Errorf("%s is nil", a)
			continue
		}
		text := ea.Error()
		if !strings.HasPrefix(text, "authn: ") {
			t.Errorf("%s reads %q, want the authn: prefix", a, text)
		}
		if strings.Contains(text, "%") {
			t.Errorf("%s reads %q, which holds a format verb", a, text)
		}
		for _, b := range names[i+1:] {
			eb := designSentinels[b]
			if errors.Is(ea, eb) || errors.Is(eb, ea) || text == eb.Error() {
				t.Errorf("%s and %s are not two failures", a, b)
			}
		}
	}
}

// TestASentinelSurvivesWrapping holds the one property callers use: errors.Is
// finds a sentinel through the %w wraps the design requires, and does not find
// the one next to it.
func TestASentinelSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrCleanupIncomplete))
	if !errors.Is(err, ErrCleanupIncomplete) {
		t.Error("a wrapped ErrCleanupIncomplete is not found")
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrAccountNotFound) {
		t.Error("a wrapped ErrCleanupIncomplete reads as another sentinel")
	}
}

// TestLinkRequiredErrorKeepsTheAddressOutOfItsText holds the split the design
// states: the value carries the address so a surface can name the collision,
// while its text stays the sentinel's, so a log line names no address.
func TestLinkRequiredErrorKeepsTheAddressOutOfItsText(t *testing.T) {
	err := fmt.Errorf("resolve: %w", &LinkRequiredError{Email: "ann@home.org"})
	if !errors.Is(err, ErrLinkRequired) {
		t.Error("a LinkRequiredError is not found through errors.Is")
	}
	if err.Error() != "resolve: "+ErrLinkRequired.Error() {
		t.Errorf("the text reads %q, want the sentinel's", err.Error())
	}
	if strings.Contains(err.Error(), "ann@home.org") {
		t.Errorf("the text %q names the address", err.Error())
	}
}

// TestContactRequiredProblemTypeIsTheWireValue holds the exported code to its
// wire value. The three management faces and a host's own surfaces write this
// constant, so its value is a contract those surfaces share; pinning the literal
// here is what makes a rename fail rather than drift silently, because the
// surfaces' own cases assert against the constant and would pass whatever it
// held.
func TestContactRequiredProblemTypeIsTheWireValue(t *testing.T) {
	if ContactRequiredProblemType != "urn:speed:authn:contact-required" {
		t.Errorf("ContactRequiredProblemType = %q, want %q",
			ContactRequiredProblemType, "urn:speed:authn:contact-required")
	}
}

// TestErrInsecureConfigStatesAProtectionTheDeploymentNeeds holds the text an
// operator reads when startup refuses an http public URL or a same-site of none.
// No setting lets a deployment give either protection up, so the text names a
// protection the deployment cannot run without, not one it could be told to
// drop.
func TestErrInsecureConfigStatesAProtectionTheDeploymentNeeds(t *testing.T) {
	const want = "authn: the configuration weakens a protection the deployment cannot run without"
	if got := ErrInsecureConfig.Error(); got != want {
		t.Errorf("ErrInsecureConfig reads %q, want %q", got, want)
	}
}
