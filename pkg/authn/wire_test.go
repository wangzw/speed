package authn

import (
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// TestTheNewValueTypesAreTheModelsOwn holds that each type the root re-exports is
// the model's type and not a second declaration of it: an alias turned into a
// definition compiles, and the two would then convert only by hand.
func TestTheNewValueTypesAreTheModelsOwn(t *testing.T) {
	for _, c := range []struct{ root, model reflect.Type }{
		{reflect.TypeFor[AccountKind](), reflect.TypeFor[model.AccountKind]()},
		{reflect.TypeFor[AccountStatus](), reflect.TypeFor[model.AccountStatus]()},
		{reflect.TypeFor[Account](), reflect.TypeFor[model.Account]()},
		{reflect.TypeFor[CreateAccount](), reflect.TypeFor[model.CreateAccount]()},
		{reflect.TypeFor[Verification](), reflect.TypeFor[model.Verification]()},
		{reflect.TypeFor[SignupState](), reflect.TypeFor[model.SignupState]()},
		{reflect.TypeFor[SignupStatus](), reflect.TypeFor[model.SignupStatus]()},
		{reflect.TypeFor[RegisterAccount](), reflect.TypeFor[model.RegisterAccount]()},
		{reflect.TypeFor[AccountQuery](), reflect.TypeFor[model.AccountQuery]()},
		{reflect.TypeFor[ExternalBinding](), reflect.TypeFor[model.ExternalBinding]()},
		{reflect.TypeFor[ServiceSecretInfo](), reflect.TypeFor[model.ServiceSecretInfo]()},
		{reflect.TypeFor[Membership](), reflect.TypeFor[model.Membership]()},
		{reflect.TypeFor[Page](), reflect.TypeFor[model.Page]()},
		{reflect.TypeFor[VerifyResult](), reflect.TypeFor[model.VerifyResult]()},
		{reflect.TypeFor[SetPasswordOptions](), reflect.TypeFor[model.SetPasswordOptions]()},
		{reflect.TypeFor[SessionKind](), reflect.TypeFor[model.SessionKind]()},
		{reflect.TypeFor[ClientInfo](), reflect.TypeFor[model.ClientInfo]()},
		{reflect.TypeFor[NewSession](), reflect.TypeFor[model.NewSession]()},
		{reflect.TypeFor[Issued](), reflect.TypeFor[model.Issued]()},
		{reflect.TypeFor[SessionInfo](), reflect.TypeFor[model.SessionInfo]()},
		{reflect.TypeFor[SessionStatus](), reflect.TypeFor[model.SessionStatus]()},
		{reflect.TypeFor[Elevation](), reflect.TypeFor[model.Elevation]()},
		{reflect.TypeFor[Start](), reflect.TypeFor[model.Start]()},
		{reflect.TypeFor[FlowState](), reflect.TypeFor[model.FlowState]()},
		{reflect.TypeFor[StepInput](), reflect.TypeFor[model.StepInput]()},
		{reflect.TypeFor[MFAOutcome](), reflect.TypeFor[model.MFAOutcome]()},
		{reflect.TypeFor[MFAResult](), reflect.TypeFor[model.MFAResult]()},
		{reflect.TypeFor[ImpersonationRequest](), reflect.TypeFor[model.ImpersonationRequest]()},
		{reflect.TypeFor[ImpersonationGrant](), reflect.TypeFor[model.ImpersonationGrant]()},
		{reflect.TypeFor[GrantQuery](), reflect.TypeFor[model.GrantQuery]()},
		{reflect.TypeFor[IssueToken](), reflect.TypeFor[model.IssueToken]()},
		{reflect.TypeFor[AccessToken](), reflect.TypeFor[model.AccessToken]()},
		{reflect.TypeFor[RefreshToken](), reflect.TypeFor[model.RefreshToken]()},
		{reflect.TypeFor[SendCode](), reflect.TypeFor[model.SendCode]()},
		{reflect.TypeFor[SSOProvider](), reflect.TypeFor[model.SSOProvider]()},
	} {
		if c.root != c.model {
			t.Errorf("the root's %s is not the model's %s", c.root, c.model)
		}
	}
}

// TestTheWireValuesOfTheNewConstantsAreFrozen pins what is written to a table, a
// token, a payload or a route: the status numbers of the status column, the texts
// of the kind columns, the closed sets that other modules and clients compare.
// The model's own tests hold the same values; these are read through the root
// names, which is where a host and every module reads them.
func TestTheWireValuesOfTheNewConstantsAreFrozen(t *testing.T) {
	if StatusDisabled != 0 || StatusActive != 1 || StatusDeleted != 2 {
		t.Errorf("account statuses read %d, %d, %d, want 0, 1, 2 (the status column)", StatusDisabled, StatusActive, StatusDeleted)
	}
	if MFANone != 0 || MFAFactor != 1 || MFAEnroll != 2 {
		t.Errorf("MFA outcomes read %d, %d, %d, want 0, 1, 2", MFANone, MFAFactor, MFAEnroll)
	}
	if SignupNone != 0 || SignupPending != 1 || SignupVerified != 2 {
		t.Errorf("signup states read %d, %d, %d, want 0, 1, 2 (the signup_state column)",
			SignupNone, SignupPending, SignupVerified)
	}
	for _, c := range [][2]string{
		{string(KindUser), "user"},
		{string(KindService), "service"},
		{string(SessionBrowser), "browser"},
		{string(SessionToken), "token"},
		{string(SessionService), "service"},
		{PurposeLogin, "login"},
		{PurposeLink, "link"},
		{PurposeStepUp, "step-up"},
		{PurposeEnroll, "enroll"},
		{InputSubmit, "submit"},
		{InputCallback, "callback"},
		{SMSPurposeLogin, "login"},
		{SMSPurposeMFA, "mfa"},
		{SMSPurposeEnroll, "enroll"},
		{SMSPurposeReset, "reset"},
		{SSOProtocolOIDC, "oidc"},
		{SSOProtocolSAML, "saml"},
		{ProvisioningJIT, "jit"},
		{ProvisioningPreprovisioned, "preprovisioned"},
		{VerifiedViaSMSCode, "sms-code"},
		{VerifiedViaIDPPrefix, "idp:"},
		{VerifiedViaInvite, "invite"},
		{VerifiedViaAdmin, "admin"},
		{VerifiedViaImportUnverified, "import-unverified"},
		{EndExplicit, "explicit"},
		{EndRevoked, "revoked"},
		{EndExpired, "expired"},
		{EndSessionEnded, "session-ended"},
		{EndActorDisabled, "actor-disabled"},
		{EndTargetDisabled, "target-disabled"},
		{EndMembershipRemoved, "membership-removed"},
		{EndStartFailed, "start-failed"},
		{ReservedPrefixIDP, "idp."},
		{ReservedPrefixAuthn, "authn."},
	} {
		if c[0] != c[1] {
			t.Errorf("constant reads %q, want %q", c[0], c[1])
		}
	}
}

// TestTheActionsAreTheDesignsFifteenAndAreUnique holds the values the permission
// model is promised: a published action is never renamed, and the set is exactly
// the design's.
func TestTheActionsAreTheDesignsFifteenAndAreUnique(t *testing.T) {
	want := map[Action]string{
		ActionAccountRead:         "authn.account.read",
		ActionAccountCreate:       "authn.account.create",
		ActionAccountUpdate:       "authn.account.update",
		ActionAccountDisable:      "authn.account.disable",
		ActionAccountDelete:       "authn.account.delete",
		ActionMembershipManage:    "authn.membership.manage",
		ActionSessionRead:         "authn.session.read",
		ActionSessionRevoke:       "authn.session.revoke",
		ActionMFAReset:            "authn.mfa.reset",
		ActionClientManage:        "authn.client.manage",
		ActionIDPManage:           "authn.idp.manage",
		ActionImpersonateStart:    "authn.impersonation.start",
		ActionImpersonationRead:   "authn.impersonation.read",
		ActionImpersonationRevoke: "authn.impersonation.revoke",
		ActionTenantAccess:        "authn.tenant.access",
	}
	if len(want) != 15 {
		t.Fatalf("the table above has %d actions, and two constants share a value", len(want))
	}
	for a, text := range want {
		if string(a) != text {
			t.Errorf("an action reads %q, want %q", a, text)
		}
	}
}

// TestTheAttributeBoundsAreTheDesignsNumbers holds the sub-bounds as the design
// writes them, read through the root, together with the arithmetic that makes
// them fit the gateway's totals.
func TestTheAttributeBoundsAreTheDesignsNumbers(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"AccountAttributesMax", AccountAttributesMax, 24},
		{"AccountAttributeBytesMax", AccountAttributeBytesMax, 2048},
		{"AttributeKeyMax", AttributeKeyMax, 64},
		{"AccountAttributeValueMax", AccountAttributeValueMax, 256},
		{"SessionIDPAttributesMax", SessionIDPAttributesMax, 8},
		{"SessionIDPKeyMax", SessionIDPKeyMax, 32},
		{"SessionIDPValueMax", SessionIDPValueMax, 128},
		{"DefaultPageLimit", DefaultPageLimit, 100},
		{"MaxPageLimit", MaxPageLimit, 1000},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if got := AccountAttributesMax + SessionIDPAttributesMax; got != 32 {
		t.Errorf("the entries of both writers come to %d, want 32", got)
	}
	if got := AccountAttributeBytesMax + SessionIDPAttributesMax*(SessionIDPKeyMax+SessionIDPValueMax); got != 3328 {
		t.Errorf("the worst case of both writers is %d bytes, want 3328", got)
	}
}
