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

// designSentinels is every sentinel of the root package the failure tables
// name.
var designSentinels = map[string]error{
	"ErrNoRouter": ErrNoRouter, "ErrNoVerifier": ErrNoVerifier, "ErrClaimConflict": ErrClaimConflict,
	"ErrInvalidClaim": ErrInvalidClaim, "ErrInvalidConfig": ErrInvalidConfig, "ErrUnknownEndpoint": ErrUnknownEndpoint,
	"ErrExemptionUnguarded": ErrExemptionUnguarded, "ErrAccessConflict": ErrAccessConflict,
	"ErrRoutesUnconfigured": ErrRoutesUnconfigured, "ErrRoutesUnguarded": ErrRoutesUnguarded,
	"ErrInsecureConfig": ErrInsecureConfig, "ErrNoEncryptionKey": ErrNoEncryptionKey, "ErrInvalidKey": ErrInvalidKey,
	"ErrNotMigrated": ErrNotMigrated, "ErrMethodConflict": ErrMethodConflict, "ErrFactorConflict": ErrFactorConflict,
	"ErrNoLoginMethod": ErrNoLoginMethod, "ErrNoFactor": ErrNoFactor, "ErrDrainTimeout": ErrDrainTimeout,
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

// TestTheSentinelsAreExactlyTheDesignsTables fails when a named sentinel is
// missing or an undeclared one exists.
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

// TestEveryDesignSentinelIsDistinctAndSafeToLog checks each sentinel is
// distinct, under the authn: prefix, with no format verb.
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

// TestASentinelSurvivesWrapping checks errors.Is finds a sentinel through
// %w wraps and not the one next to it.
func TestASentinelSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrCleanupIncomplete))
	if !errors.Is(err, ErrCleanupIncomplete) {
		t.Error("a wrapped ErrCleanupIncomplete is not found")
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrAccountNotFound) {
		t.Error("a wrapped ErrCleanupIncomplete reads as another sentinel")
	}
}

// TestLinkRequiredErrorKeepsTheAddressOutOfItsText wants Is to find
// ErrLinkRequired while the text names no address.
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

// TestContactRequiredProblemTypeIsTheWireValue pins the exported code to its
// wire value.
func TestContactRequiredProblemTypeIsTheWireValue(t *testing.T) {
	if ContactRequiredProblemType != "urn:speed:authn:contact-required" {
		t.Errorf("ContactRequiredProblemType = %q, want %q",
			ContactRequiredProblemType, "urn:speed:authn:contact-required")
	}
}

// TestErrInsecureConfigStatesAProtectionTheDeploymentNeeds pins the sentinel's
// exact text.
func TestErrInsecureConfigStatesAProtectionTheDeploymentNeeds(t *testing.T) {
	const want = "authn: the configuration weakens a protection the deployment cannot run without"
	if got := ErrInsecureConfig.Error(); got != want {
		t.Errorf("ErrInsecureConfig reads %q, want %q", got, want)
	}
}
