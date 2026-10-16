package authn

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// exportedSurface is the root package's exported surface, written out, one
// entry per declaration a dependant can see and so one this module has to keep.
// The aliases are included with what they carry: an alias's fields, and the
// method set of the type behind it, are on the surface even though the source
// of this package declares none of them, which is why the list is read from the
// type checker's view and not from this package's syntax.
//
// The list is here rather than derived because deriving it would only restate
// whatever the code happens to say. Pinned, a new exported symbol has to be
// added by hand, and the entry is where the reader asks whether it belongs on
// the surface at all.
var exportedSurface = []string{
	"const AccessOpen",
	"const AccessOptional",
	"const AccessRequired",
	"const AccessUnknown",
	"const AccountAttributeBytesMax",
	"const AccountAttributeValueMax",
	"const AccountAttributesMax",
	"const ActionAccountCreate",
	"const ActionAccountDelete",
	"const ActionAccountDisable",
	"const ActionAccountRead",
	"const ActionAccountUpdate",
	"const ActionClientManage",
	"const ActionIDPManage",
	"const ActionImpersonateStart",
	"const ActionImpersonationRead",
	"const ActionImpersonationRevoke",
	"const ActionMFAReset",
	"const ActionMembershipManage",
	"const ActionSessionRead",
	"const ActionSessionRevoke",
	"const ActionTenantAccess",
	"const AssuranceMulti",
	"const AssuranceNone",
	"const AssuranceSingle",
	"const AttributeKeyMax",
	"const DefaultPageLimit",
	"const EndActorDisabled",
	"const EndExpired",
	"const EndExplicit",
	"const EndMembershipRemoved",
	"const EndRevoked",
	"const EndSessionEnded",
	"const EndStartFailed",
	"const EndTargetDisabled",
	"const EventAccountCreated",
	"const EventAccountDeleted",
	"const EventAccountStatusChanged",
	"const EventBindingChanged",
	"const EventFactorChanged",
	"const EventIDPChanged",
	"const EventIdentifierChanged",
	"const EventImpersonationEnded",
	"const EventImpersonationStarted",
	"const EventLoginSucceeded",
	"const EventMembershipChanged",
	"const EventPasswordChanged",
	"const EventSessionRevoked",
	"const EventThrottleEngaged",
	"const EventTokenReuseDetected",
	"const FactorActive",
	"const FactorDisabled",
	"const FactorPending",
	"const FactorSMS",
	"const FactorTOTP",
	"const FactorWebAuthn",
	"const FieldCode",
	"const FieldPassword",
	"const FieldPhone",
	"const FieldText",
	"const IdentifierEmail",
	"const IdentifierPhone",
	"const IdentifierUsername",
	"const InputCallback",
	"const InputSubmit",
	"const KindService",
	"const KindUser",
	"const MFAEnroll",
	"const MFAFactor",
	"const MFANone",
	"const MaxPageLimit",
	"const MethodClientSecret",
	"const MethodKindEnterprise",
	"const MethodKindForm",
	"const MethodKindRedirect",
	"const MethodKindWebAuthn",
	"const MethodOAuth",
	"const MethodOIDC",
	"const MethodPassword",
	"const MethodProtocolOAuth2",
	"const MethodProtocolOIDC",
	"const MethodProtocolSAML",
	"const MethodSAML",
	"const MethodSMS",
	"const MethodStatic",
	"const MethodTOTP",
	"const MethodWebAuthn",
	"const ProvisioningJIT",
	"const ProvisioningPreprovisioned",
	"const PurposeEnroll",
	"const PurposeLink",
	"const PurposeLogin",
	"const PurposeStepUp",
	"const ReasonAccountDeleted",
	"const ReasonAccountDisabled",
	"const ReasonAdmin",
	"const ReasonEvicted",
	"const ReasonExpired",
	"const ReasonFactorReset",
	"const ReasonPasswordChange",
	"const ReasonRefreshReuse",
	"const ReasonSelf",
	"const ReasonThrottled",
	"const ReservedPrefixAuthn",
	"const ReservedPrefixIDP",
	"const SMSPurposeEnroll",
	"const SMSPurposeLogin",
	"const SMSPurposeMFA",
	"const SSOProtocolOIDC",
	"const SSOProtocolSAML",
	"const ScopeAccount",
	"const ScopeClient",
	"const ScopeFactor",
	"const ScopeIdentifier",
	"const ScopePhone",
	"const SessionBrowser",
	"const SessionIDPAttributesMax",
	"const SessionIDPKeyMax",
	"const SessionIDPValueMax",
	"const SessionService",
	"const SessionToken",
	"const StaticPrefix",
	"const StatusActive",
	"const StatusDeleted",
	"const StatusDisabled",
	"const VerifiedViaAdmin",
	"const VerifiedViaEmailCode",
	"const VerifiedViaIDPPrefix",
	"const VerifiedViaImportUnverified",
	"const VerifiedViaInvite",
	"const VerifiedViaSMSCode",
	"const VerifiedViaSelf",
	"field Accepts.Accounts",
	"field Accepts.Cookies",
	"field Accepts.Impersonation",
	"field Accepts.SSO",
	"field Accepts.Schemes",
	"field AccessToken.ExpiresAt",
	"field AccessToken.Value",
	"field Account.Attributes",
	"field Account.CreatedAt",
	"field Account.DisplayName",
	"field Account.ID",
	"field Account.Kind",
	"field Account.MFARequired",
	"field Account.PasswordSet",
	"field Account.Protected",
	"field Account.Status",
	"field Account.ValidAfter",
	"field AccountCreated.AccountID",
	"field AccountCreated.Kind",
	"field AccountCreated.Via",
	"field AccountDeleted.AccountID",
	"field AccountQuery.After",
	"field AccountQuery.DisplayNamePrefix",
	"field AccountQuery.IdentifierKind",
	"field AccountQuery.IdentifierPrefix",
	"field AccountQuery.Kind",
	"field AccountQuery.Limit",
	"field AccountQuery.Status",
	"field AccountStatusChanged.AccountID",
	"field AccountStatusChanged.From",
	"field AccountStatusChanged.ReasonClass",
	"field AccountStatusChanged.To",
	"field BindingChanged.AccountID",
	"field BindingChanged.Change",
	"field BindingChanged.ProviderClass",
	"field BindingChanged.Source",
	"field ClientInfo.IP",
	"field ClientInfo.UserAgent",
	"field CreateAccount.Attributes",
	"field CreateAccount.DisplayName",
	"field CreateAccount.Email",
	"field CreateAccount.EmailVerifiedVia",
	"field CreateAccount.ID",
	"field CreateAccount.Kind",
	"field CreateAccount.MFARequired",
	"field CreateAccount.MustChangePassword",
	"field CreateAccount.Password",
	"field CreateAccount.PasswordHash",
	"field CreateAccount.Phone",
	"field CreateAccount.PhoneVerifiedVia",
	"field CreateAccount.Username",
	"field CreateAccount.Via",
	"field Credential.Cookie",
	"field Credential.Peer",
	"field Credential.Scheme",
	"field Credential.Value",
	"field Decision.Action",
	"field Decision.Actor",
	"field Decision.Params",
	"field Decision.Target",
	"field Decision.TargetAccount",
	"field Decision.Tenant",
	"field Elevation.Assurance",
	"field Elevation.AuthTime",
	"field Elevation.IDPAssured",
	"field Elevation.Methods",
	"field External.Attributes",
	"field External.DisplayName",
	"field External.Email",
	"field External.EmailVerified",
	"field External.Provider",
	"field External.Subject",
	"field External.Tenant",
	"field ExternalBinding.CreatedAt",
	"field ExternalBinding.Email",
	"field ExternalBinding.EmailVerified",
	"field ExternalBinding.ID",
	"field ExternalBinding.LastLoginAt",
	"field ExternalBinding.Provider",
	"field ExternalBinding.Subject",
	"field ExternalLink.Email",
	"field ExternalLink.EmailVerified",
	"field ExternalLink.Provider",
	"field ExternalLink.Subject",
	"field FactorChanged.AccountID",
	"field FactorChanged.By",
	"field FactorChanged.Change",
	"field FactorChanged.FactorID",
	"field FactorChanged.Kind",
	"field FactorRecord.Account",
	"field FactorRecord.ActivatedAt",
	"field FactorRecord.CreatedAt",
	"field FactorRecord.ID",
	"field FactorRecord.Kind",
	"field FactorRecord.Label",
	"field FactorRecord.LastUsedAt",
	"field FactorRecord.Status",
	"field Field.Kind",
	"field Field.Name",
	"field Field.Required",
	"field Field.Secret",
	"field FlowState.Account",
	"field FlowState.ID",
	"field FlowState.Locale",
	"field FlowState.Private",
	"field FlowState.Provider",
	"field FlowState.Purpose",
	"field FlowState.SendsLeft",
	"field FlowState.Session",
	"field FlowState.Step",
	"field GrantQuery.Actor",
	"field GrantQuery.After",
	"field GrantQuery.Limit",
	"field GrantQuery.Target",
	"field GrantQuery.Tenant",
	"field IDPChanged.Change",
	"field IDPChanged.IDPID",
	"field Identifier.ID",
	"field Identifier.Kind",
	"field Identifier.Value",
	"field Identifier.VerifiedAt",
	"field Identifier.VerifiedVia",
	"field IdentifierChanged.AccountID",
	"field IdentifierChanged.Change",
	"field IdentifierChanged.Kind",
	"field ImpersonationEnded.EndedBy",
	"field ImpersonationEnded.GrantID",
	"field ImpersonationGrant.Actor",
	"field ImpersonationGrant.EndReason",
	"field ImpersonationGrant.EndedAt",
	"field ImpersonationGrant.EndedBy",
	"field ImpersonationGrant.EndsAt",
	"field ImpersonationGrant.ID",
	"field ImpersonationGrant.Reason",
	"field ImpersonationGrant.SessionID",
	"field ImpersonationGrant.StartedAt",
	"field ImpersonationGrant.Target",
	"field ImpersonationGrant.Tenant",
	"field ImpersonationRequest.Reason",
	"field ImpersonationRequest.TTL",
	"field ImpersonationRequest.Target",
	"field ImpersonationRequest.Tenant",
	"field ImpersonationStarted.AccountID",
	"field ImpersonationStarted.ActorID",
	"field ImpersonationStarted.ExpiresAt",
	"field ImpersonationStarted.GrantID",
	"field ImpersonationStarted.SessionID",
	"field IssueToken.Account",
	"field IssueToken.Actor",
	"field IssueToken.Assurance",
	"field IssueToken.AuthTime",
	"field IssueToken.ClientID",
	"field IssueToken.ImpersonationID",
	"field IssueToken.Methods",
	"field IssueToken.Scope",
	"field IssueToken.Session",
	"field IssueToken.TTL",
	"field Issued.ExpiresAt",
	"field Issued.ID",
	"field Issued.Token",
	"field LoginSucceeded.AccountID",
	"field LoginSucceeded.Assurance",
	"field LoginSucceeded.Delivery",
	"field LoginSucceeded.Methods",
	"field LoginSucceeded.SessionID",
	"field MFAResult.Assurance",
	"field MFAResult.IDPAssured",
	"field Membership.Account",
	"field Membership.Active",
	"field Membership.CreatedAt",
	"field Membership.SSOExempt",
	"field Membership.Source",
	"field Membership.Tenant",
	"field MembershipChanged.AccountID",
	"field MembershipChanged.Change",
	"field MembershipChanged.Source",
	"field MethodDescriptor.Fields",
	"field MethodDescriptor.Kind",
	"field MethodDescriptor.Label",
	"field MethodDescriptor.Name",
	"field MethodDescriptor.OpensAccount",
	"field MethodDescriptor.Protocol",
	"field NewSession.AbsoluteTTL",
	"field NewSession.Account",
	"field NewSession.Assurance",
	"field NewSession.Attributes",
	"field NewSession.AuthTime",
	"field NewSession.Client",
	"field NewSession.IDPAssured",
	"field NewSession.Kind",
	"field NewSession.Methods",
	"field NewSession.Provider",
	"field Page.After",
	"field Page.Limit",
	"field Pass.Methods",
	"field Pass.UserVerified",
	"field PasswordChanged.AccountID",
	"field PasswordChanged.By",
	"field Principal.Account",
	"field Principal.Actor",
	"field Principal.Assurance",
	"field Principal.Attributes",
	"field Principal.AuthTime",
	"field Principal.IDPAssured",
	"field Principal.Methods",
	"field Principal.Provider",
	"field Principal.Scope",
	"field Principal.Session",
	"field Proof.Account",
	"field Proof.Aliases",
	"field Proof.External",
	"field Proof.IDPAsserted",
	"field Proof.Identifier",
	"field Proof.Methods",
	"field Proof.PasswordChanged",
	"field Proof.Provider",
	"field Proof.UserVerified",
	"field RefreshToken.ExpiresAt",
	"field RefreshToken.Value",
	"field Requirement.Assurance",
	"field Requirement.Local",
	"field Requirement.MaxAge",
	"field SSOProvider.AllowIDPInitiated",
	"field SSOProvider.AllowSHA1",
	"field SSOProvider.Config",
	"field SSOProvider.Enabled",
	"field SSOProvider.ID",
	"field SSOProvider.MergeExisting",
	"field SSOProvider.Name",
	"field SSOProvider.Protocol",
	"field SSOProvider.Provisioning",
	"field SSOProvider.Tenant",
	"field SSOProvider.TrustIDPMFA",
	"field SSOProvider.Version",
	"field SendCode.Account",
	"field SendCode.Deliver",
	"field SendCode.Locale",
	"field SendCode.Phone",
	"field SendCode.Purpose",
	"field ServiceSecretInfo.CreatedAt",
	"field ServiceSecretInfo.ExpiresAt",
	"field ServiceSecretInfo.ID",
	"field ServiceSecretInfo.Label",
	"field ServiceSecretInfo.LastUsedAt",
	"field ServiceSecretInfo.RevokedAt",
	"field SessionInfo.Account",
	"field SessionInfo.Assurance",
	"field SessionInfo.AuthTime",
	"field SessionInfo.CreatedAt",
	"field SessionInfo.CredentialID",
	"field SessionInfo.ExpiresAt",
	"field SessionInfo.ID",
	"field SessionInfo.ImpersonationID",
	"field SessionInfo.Kind",
	"field SessionInfo.LastSeenAt",
	"field SessionInfo.Revoked",
	"field SessionRevoked.AccountID",
	"field SessionRevoked.ReasonClass",
	"field SessionRevoked.Scope",
	"field SessionRevoked.SessionID",
	"field SessionStatus.Account",
	"field SessionStatus.Active",
	"field SessionStatus.Assurance",
	"field SessionStatus.Attributes",
	"field SessionStatus.AuthTime",
	"field SessionStatus.CreatedAt",
	"field SessionStatus.CredentialID",
	"field SessionStatus.ExpiresAt",
	"field SessionStatus.IDPAssured",
	"field SessionStatus.ImpersonationID",
	"field SessionStatus.Kind",
	"field SessionStatus.Methods",
	"field SessionStatus.Provider",
	"field SetPasswordOptions.Current",
	"field SetPasswordOptions.MustChange",
	"field Start.Account",
	"field Start.Client",
	"field Start.Fields",
	"field Start.Locale",
	"field Start.Purpose",
	"field Start.SendsLeft",
	"field StepInput.Action",
	"field StepInput.Client",
	"field StepInput.Fields",
	"field StepInput.Form",
	"field StepInput.Query",
	"field ThrottleEngaged.AccountID",
	"field ThrottleEngaged.Scope",
	"field ThrottledError.RetryAfter",
	"field TokenReuseDetected.AccountID",
	"field TokenReuseDetected.SessionID",
	"field Verification.Via",
	"field VerifyResult.MustChange",
	"func AccessFromContext",
	"func Authenticated",
	"func Denied",
	"func FreshFirstFactor",
	"func FreshLocalMulti",
	"func FromContext",
	"func ParseAccountID",
	"func StepData",
	"func StepEnrolled",
	"func StepPassed",
	"func StepPrompt",
	"func StepProven",
	"func StepRedirect",
	"func StepRefused",
	"func StepThrottled",
	"func StepUnavailable",
	"func Unavailable",
	"func ValidPayloadReasonClass",
	"func ValidReasonClass",
	"method Account.Clone",
	"method AccountAdmin.AddIdentifier",
	"method AccountAdmin.Create",
	"method AccountAdmin.CreateServiceSecret",
	"method AccountAdmin.Delete",
	"method AccountAdmin.LinkExternal",
	"method AccountAdmin.RemoveIdentifier",
	"method AccountAdmin.RevokeServiceSecret",
	"method AccountAdmin.SetAttributes",
	"method AccountAdmin.SetDisplayName",
	"method AccountAdmin.SetMFARequired",
	"method AccountAdmin.SetProtected",
	"method AccountAdmin.SetStatus",
	"method AccountAdmin.Touch",
	"method AccountAdmin.UnlinkExternal",
	"method AccountID.IsStatic",
	"method AccountID.String",
	"method AccountKind.Valid",
	"method AccountLifecycle.AccountDeleted",
	"method AccountLifecycle.AccountDisabled",
	"method AccountLifecycle.ServiceSecretRevoked",
	"method AccountQuery.Clone",
	"method AccountQuery.EffectiveLimit",
	"method AccountStatus.String",
	"method AccountStatus.Valid",
	"method Accounts.ByExternal",
	"method Accounts.Externals",
	"method Accounts.Get",
	"method Accounts.Identifiers",
	"method Accounts.List",
	"method Accounts.ListServiceSecrets",
	"method Accounts.Lookup",
	"method Challenger.Challenge",
	"method ClaimsContributor.Claims",
	"method CookieClearer.ClearCookie",
	"method CreateAccount.Clone",
	"method Decider.Decide",
	"method Elevation.Clone",
	"method EmailCodeSender.SendCode",
	"method External.Clone",
	"method Factor.Challenge",
	"method Factor.Enroll",
	"method Factor.Kind",
	"method Factor.Remove",
	"method Factor.Verify",
	"method FlowState.Clone",
	"method Gate.Exempt",
	"method Gate.Impersonable",
	"method Gate.Optional",
	"method GrantQuery.EffectiveLimit",
	"method Identity.Account",
	"method Identity.Actor",
	"method Identity.Assurance",
	"method Identity.AssuranceFromIDP",
	"method Identity.Attribute",
	"method Identity.Attributes",
	"method Identity.AuthTime",
	"method Identity.Backend",
	"method Identity.Challenge",
	"method Identity.Impersonating",
	"method Identity.Key",
	"method Identity.Methods",
	"method Identity.Provider",
	"method Identity.Satisfies",
	"method Identity.Scope",
	"method Identity.Session",
	"method Impersonation.Active",
	"method Impersonation.End",
	"method Impersonation.List",
	"method Impersonation.Revoke",
	"method Impersonation.Start",
	"method IssueToken.Clone",
	"method LoginMethod.Answer",
	"method LoginMethod.Begin",
	"method LoginMethod.Describe",
	"method LoginMethod.Name",
	"method MFA.Assurance",
	"method MFA.Challenge",
	"method MFA.Enroll",
	"method MFA.Factors",
	"method MFA.Kinds",
	"method MFA.Remove",
	"method MFA.Require",
	"method MFA.Reset",
	"method MFA.Seasoned",
	"method MFA.Verify",
	"method Memberships.IsMember",
	"method Memberships.Members",
	"method Memberships.Membership",
	"method Memberships.Put",
	"method Memberships.Remove",
	"method Memberships.Tenants",
	"method Method.Valid",
	"method NewSession.Clone",
	"method Page.EffectiveLimit",
	"method Pass.Clone",
	"method Passwords.Clear",
	"method Passwords.ImportHash",
	"method Passwords.Set",
	"method Passwords.Verify",
	"method Passwords.VerifyServiceSecret",
	"method Principal.Clone",
	"method Proof.Clone",
	"method Provisioner.Provision",
	"method SMSCodes.Send",
	"method SMSCodes.Verify",
	"method SSOMetadataRefresher.Protocol",
	"method SSOMetadataRefresher.Refresh",
	"method SSOProvider.Clone",
	"method SSOProviders.ClaimDomain",
	"method SSOProviders.Delete",
	"method SSOProviders.Discover",
	"method SSOProviders.Get",
	"method SSOProviders.List",
	"method SSOProviders.Put",
	"method SSOProviders.RemoveDomain",
	"method SSOProviders.ReverifyDomains",
	"method SSOProviders.Secret",
	"method SSOProviders.Tenant",
	"method SSOProviders.VerifyDomain",
	"method ScopeGuard.Covers",
	"method SessionKind.Valid",
	"method SessionStatus.Clone",
	"method Sessions.ClearCookie",
	"method Sessions.Create",
	"method Sessions.Current",
	"method Sessions.Elevate",
	"method Sessions.ForCredential",
	"method Sessions.Get",
	"method Sessions.List",
	"method Sessions.RevocableSessions",
	"method Sessions.Revoke",
	"method Sessions.RevokeAccount",
	"method Sessions.RevokeCredential",
	"method Sessions.Rotate",
	"method Sessions.SetCookie",
	"method Sessions.SetImpersonation",
	"method Sessions.Status",
	"method Sessions.StatusFresh",
	"method Start.Clone",
	"method Step.WithSends",
	"method StepInput.Clone",
	"method Throttle.Check",
	"method Throttle.Fail",
	"method Throttle.Succeed",
	"method ThrottledError.Error",
	"method ThrottledError.Is",
	"method Tokens.Issue",
	"method Tokens.JWKS",
	"method Tokens.NewRefresh",
	"method Tokens.Refresh",
	"method Tokens.Revoke",
	"method Verification.Proves",
	"method Verification.Valid",
	"method Verifier.Accepts",
	"method Verifier.Name",
	"method Verifier.Verify",
	"type Accepts",
	"type Access",
	"type AccessToken",
	"type Account",
	"type AccountAdmin",
	"type AccountCreated",
	"type AccountDeleted",
	"type AccountID",
	"type AccountKind",
	"type AccountLifecycle",
	"type AccountQuery",
	"type AccountStatus",
	"type AccountStatusChanged",
	"type Accounts",
	"type Action",
	"type Assurance",
	"type BindingChanged",
	"type Challenger",
	"type ClaimsContributor",
	"type ClientInfo",
	"type CookieClearer",
	"type CreateAccount",
	"type Credential",
	"type Decider",
	"type Decision",
	"type Elevation",
	"type EmailCodeSender",
	"type External",
	"type ExternalBinding",
	"type ExternalLink",
	"type Factor",
	"type FactorChanged",
	"type FactorKind",
	"type FactorRecord",
	"type FactorStatus",
	"type Field",
	"type FlowState",
	"type Gate",
	"type GrantQuery",
	"type IDPChanged",
	"type Identifier",
	"type IdentifierChanged",
	"type IdentifierKind",
	"type Identity",
	"type Impersonation",
	"type ImpersonationEnded",
	"type ImpersonationGrant",
	"type ImpersonationRequest",
	"type ImpersonationStarted",
	"type IssueToken",
	"type Issued",
	"type LoginMethod",
	"type LoginSucceeded",
	"type MFA",
	"type MFAOutcome",
	"type MFAResult",
	"type Membership",
	"type MembershipChanged",
	"type Memberships",
	"type Method",
	"type MethodDescriptor",
	"type MethodKind",
	"type MethodProtocol",
	"type NewSession",
	"type Page",
	"type Pass",
	"type PasswordChanged",
	"type Passwords",
	"type Principal",
	"type Proof",
	"type Provisioner",
	"type RefreshToken",
	"type Requirement",
	"type SMSCodes",
	"type SSOMetadataRefresher",
	"type SSOProvider",
	"type SSOProviders",
	"type ScopeGuard",
	"type SendCode",
	"type ServiceSecretInfo",
	"type SessionID",
	"type SessionInfo",
	"type SessionKind",
	"type SessionRevoked",
	"type SessionStatus",
	"type Sessions",
	"type SetPasswordOptions",
	"type Start",
	"type Step",
	"type StepInput",
	"type Throttle",
	"type ThrottleEngaged",
	"type ThrottleScope",
	"type ThrottledError",
	"type TokenReuseDetected",
	"type Tokens",
	"type Verdict",
	"type Verification",
	"type Verifier",
	"type VerifyResult",
	"var ErrAccessConflict",
	"var ErrAccountExists",
	"var ErrAccountNotFound",
	"var ErrAccountUnavailable",
	"var ErrAssertionReplay",
	"var ErrAuditRequired",
	"var ErrBindingConflict",
	"var ErrBusy",
	"var ErrCSRF",
	"var ErrClaimConflict",
	"var ErrCleanupIncomplete",
	"var ErrDomainClaimed",
	"var ErrDomainMismatch",
	"var ErrDrainTimeout",
	"var ErrExemptionUnguarded",
	"var ErrFactorConflict",
	"var ErrFactorFlagged",
	"var ErrFactorLimit",
	"var ErrFactorNotEnrolled",
	"var ErrFlowInvalid",
	"var ErrForbidden",
	"var ErrIdentifierRequired",
	"var ErrImpersonationActive",
	"var ErrImpersonationNeedsSession",
	"var ErrImpersonationRestricted",
	"var ErrInsecureConfig",
	"var ErrInvalidAccountID",
	"var ErrInvalidArgument",
	"var ErrInvalidClaim",
	"var ErrInvalidConfig",
	"var ErrInvalidCredentials",
	"var ErrInvalidGrant",
	"var ErrInvalidIdentifier",
	"var ErrInvalidKey",
	"var ErrLastFactor",
	"var ErrLastLoginMethod",
	"var ErrLinkRequired",
	"var ErrMethodConflict",
	"var ErrMethodDisabled",
	"var ErrNoEncryptionKey",
	"var ErrNoFactor",
	"var ErrNoLoginMethod",
	"var ErrNoRouter",
	"var ErrNoSigningKey",
	"var ErrNoVerifier",
	"var ErrNotImpersonating",
	"var ErrNotMigrated",
	"var ErrNotProvisioned",
	"var ErrPasswordPolicy",
	"var ErrProviderNotFound",
	"var ErrProvisionRefused",
	"var ErrRefreshReuse",
	"var ErrReplay",
	"var ErrReservedClaim",
	"var ErrRoutesUnconfigured",
	"var ErrRoutesUnguarded",
	"var ErrSessionNotFound",
	"var ErrSessionRevoked",
	"var ErrSignupClosed",
	"var ErrStepOutOfOrder",
	"var ErrStepUpRequired",
	"var ErrThrottled",
	"var ErrTokenInvalid",
	"var ErrUnavailable",
	"var ErrUnknownEndpoint",
	"var ErrUnsupportedRegion",
}

// authntestSurface is the exported surface of pkg/authn/authntest, pinned the
// same way and for the same reason: the package is what other modules' tests
// build an identity with, so an entry added to it is a way of building one that
// the gateway's own checks are then asked to match, and an entry removed breaks
// every test that calls it. It has no type with methods or fields to list, only
// the option constructors, the two context builders, the configuration reader
// builders and the defaults a test leaves unsaid.
var authntestSurface = []string{
	"const DefaultAccount",
	"const DefaultBackend",
	"const DefaultChallenge",
	"func Anonymous",
	"func ConfigReader",
	"func ConfigReaderAll",
	"func Context",
	"func DefaultAuthTime",
	"func WithAccess",
	"func WithAccount",
	"func WithActor",
	"func WithAssurance",
	"func WithAttributes",
	"func WithAuthTime",
	"func WithBackend",
	"func WithChallenge",
	"func WithIDPAssured",
	"func WithMethods",
	"func WithProvider",
	"func WithScope",
	"func WithSession",
	"type Option",
}

// forbiddenNames are names the surface may never carry, whatever the kind:
// the ownership model has no place in it, and the readers of a verdict and of an
// identity's internals are functions of the internal packages and not part of
// this one.
var forbiddenNames = []string{
	"Owner", "Subject", "Separator", "Claim", "OutcomeOf", "PrincipalOf", "CauseOf",
}

// forbiddenExceptions are entries that carry a forbidden name and are not what
// the name forbids. External.Subject, ExternalLink.Subject and
// ExternalBinding.Subject are an external provider's own identifier for the
// person, the "sub" of an OIDC token, which has nothing to do with the subject
// space of a credential backend that an Identity.Subject would have read.
var forbiddenExceptions = []string{
	"field External.Subject", "field ExternalLink.Subject", "field ExternalBinding.Subject",
}

// surfaceOf type-checks the non-test files of the package in dir and renders its
// exported surface, one entry per declaration.
//
// A package-level name gives one entry by its kind. A named type, or an alias
// of one, gives in addition one entry per exported field of a struct and one
// per exported method of the method set of its pointer, which for an alias is
// the method set of the type it names. Embedding is flattened, because what a
// dependant can call is what the method set says.
func surfaceOf(dir string) ([]string, error) {
	bp, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range bp.GoFiles {
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, perr)
		}
		files = append(files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(bp.Name, fset, files, nil)
	if err != nil {
		return nil, fmt.Errorf("type-checking %s: %w", dir, err)
	}

	var out []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Const:
			out = append(out, "const "+name)
		case *types.Var:
			out = append(out, "var "+name)
		case *types.Func:
			out = append(out, "func "+name)
		case *types.TypeName:
			out = append(out, "type "+name)
			out = append(out, membersOf(name, o.Type())...)
		}
	}
	slices.Sort(out)
	return out, nil
}

// membersOf renders the exported fields and methods of a named type.
func membersOf(name string, typ types.Type) []string {
	typ = types.Unalias(typ)
	var out []string
	if st, ok := typ.Underlying().(*types.Struct); ok {
		for f := range st.Fields() {
			if f.Exported() {
				out = append(out, "field "+name+"."+f.Name())
			}
		}
	}
	recv := typ
	if _, isInterface := typ.Underlying().(*types.Interface); !isInterface {
		recv = types.NewPointer(typ)
	}
	ms := types.NewMethodSet(recv)
	for sel := range ms.Methods() {
		if sel.Obj().Exported() {
			out = append(out, "method "+name+"."+sel.Obj().Name())
		}
	}
	return out
}

// surfaceDiff reports, in both directions, how got and want differ: an entry in
// the source and not in the pinned list, and one the list names that the source
// no longer has.
func surfaceDiff(got, want []string) []string {
	var out []string
	for _, entry := range got {
		if !slices.Contains(want, entry) {
			out = append(out, fmt.Sprintf("the source exports %q, which the pinned surface does not list. "+
				"Add it to exportedSurface only after deciding it belongs on the surface", entry))
		}
	}
	for _, entry := range want {
		if !slices.Contains(got, entry) {
			out = append(out, fmt.Sprintf("the pinned surface lists %q, which the source no longer exports. "+
				"Removing an entry is a breaking change for every dependant", entry))
		}
	}
	return out
}

// forbiddenIn reports the entries whose name is one of forbiddenNames.
func forbiddenIn(entries []string) []string {
	var out []string
	for _, e := range entries {
		name := e[strings.LastIndexAny(e, ". ")+1:]
		if slices.Contains(forbiddenNames, name) && !slices.Contains(forbiddenExceptions, e) {
			out = append(out, e)
		}
	}
	return out
}

// rootSurface is the package's own surface, computed once: type-checking it
// type-checks the standard library packages it imports from source, which is
// the slow part, and two cases read the result.
var rootSurface = sync.OnceValues(func() ([]string, error) { return surfaceOf(".") })

// TestExportedSurfaceIsPinned compares what the type checker says the package
// exports with the list above, in both directions: a symbol added to the code
// and not to the list fails, and so does one removed from the code while the
// list still names it.
func TestExportedSurfaceIsPinned(t *testing.T) {
	got, err := rootSurface()
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(exportedSurface)
	slices.Sort(want)
	for _, msg := range surfaceDiff(got, want) {
		t.Error(msg)
	}
}

// TestTheAuthntestSurfaceIsPinned compares what the type checker says
// pkg/authn/authntest exports with its list, in both directions. The list
// is read from the source, so a symbol that is added without anyone deciding it
// belongs in the test builders, or one that is removed while its callers in other
// modules still stand, fails here.
func TestTheAuthntestSurfaceIsPinned(t *testing.T) {
	got, err := surfaceOf("authntest")
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(authntestSurface)
	slices.Sort(want)
	for _, msg := range surfaceDiff(got, want) {
		t.Error(msg)
	}
	for _, name := range []string{"func Anonymous", "func WithChallenge", "func ConfigReader", "func ConfigReaderAll"} {
		if !slices.Contains(got, name) {
			t.Errorf("the source does not export %q, which the design and the module's AGENTS.md list", name)
		}
	}
}

// TestTheSurfaceHoldsNoNameOfTheOwnershipModel holds the list itself, and the
// source behind it, away from the forbidden names. The pinned list
// is a hand-written file, so a name could be re-added to it by the same hand
// that re-added it to the code; the list and the checker's view are both asked.
func TestTheSurfaceHoldsNoNameOfTheOwnershipModel(t *testing.T) {
	for _, e := range forbiddenIn(exportedSurface) {
		t.Errorf("the pinned surface lists %q, a name the surface may not carry", e)
	}
	got, err := rootSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range forbiddenIn(got) {
		t.Errorf("the source exports %q, a name the surface may not carry", e)
	}
}

// writeSurfacePackage writes a package of the given files into a fresh
// directory and returns it.
func writeSurfacePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestTheSurfaceCheckSeesWhatItIsMeantToSee runs the checker over a package
// built to hold each shape the real one relies on, and over a violating one, so
// the pin cannot pass by seeing nothing.
//
// The shapes: an alias whose target carries methods and fields (the alias form
// the root uses for Identity and Principal), an embedded method promoted into
// the method set, a pointer-receiver method, an interface with its methods, an
// unexported type whose methods are not on the surface, a test file whose
// declarations are not on it, and the forbidden names.
func TestTheSurfaceCheckSeesWhatItIsMeantToSee(t *testing.T) {
	dir := writeSurfacePackage(t, map[string]string{
		"p.go": `package p

type body struct{ Open int; hidden int }

func (body) Read() {}
func (*body) Write() {}

type Embedded struct{}

func (Embedded) Promoted() {}

type Wrapper struct {
	Embedded
	Visible int
	invisible int
}

type Alias = body

type Shape = Wrapper

type Reader interface {
	Read()
	hidden()
}

type unexported struct{ Field int }

func (unexported) Method() {}

const Const = 1

var Var = 2

func Func() {}

func Owner() {}

type Claim struct{}

type Payload struct {
	AccountID string ` + "`json:\"account_id\"`" + `
	private   string
}

type Decider interface {
	Decide(Shape) bool
}

type Kind string

const KindA Kind = "a"
`,
		"p_test.go": "package p\n\nfunc InTestOnly() {}\n",
	})
	got, err := surfaceOf(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"const Const", "var Var", "func Func",
		"type Alias",
		"field Alias.Open",
		"method Alias.Read",
		"method Alias.Write", // a pointer receiver, which the method set of *T holds
		"field Shape.Visible",
		"method Shape.Promoted", // promoted through the embedded field
		"type Reader", "method Reader.Read",
		"field Payload.AccountID",  // a struct declared here rather than aliased
		"method Decider.Decide",    // an interface whose method takes a type of the package
		"type Kind", "const KindA", // a named string with a typed constant
	} {
		if !slices.Contains(got, want) {
			t.Errorf("the checker does not list %q; got %q", want, got)
		}
	}
	for _, unwanted := range []string{
		"field Alias.hidden", "method Reader.hidden", "field Shape.invisible",
		"type unexported", "method unexported.Method", "func InTestOnly", "field Payload.private",
	} {
		if slices.Contains(got, unwanted) {
			t.Errorf("the checker lists %q, which is not on the surface", unwanted)
		}
	}

	if bad := forbiddenIn(got); len(bad) != 2 || !slices.Contains(bad, "func Owner") || !slices.Contains(bad, "type Claim") {
		t.Errorf("the forbidden-name check finds %q, want func Owner and type Claim", bad)
	}
	if bad := forbiddenIn([]string{
		"method Identity.Subject", "const StaticPrefix", "var ErrClaimConflict", "field External.Subject",
	}); len(bad) != 1 || bad[0] != "method Identity.Subject" {
		t.Errorf("the forbidden-name check finds %q among a forbidden method, a constant, a longer name and "+
			"a provider's subject field, want the method alone", bad)
	}

	if diff := surfaceDiff(got, slices.Clone(got)); len(diff) != 0 {
		t.Errorf("a surface differs from itself: %q", diff)
	}
	extra := surfaceDiff(append(slices.Clone(got), "func Added"), got)
	missing := surfaceDiff(got, append(slices.Clone(got), "func Removed"))
	if len(extra) != 1 || !strings.Contains(extra[0], `"func Added"`) {
		t.Errorf("a symbol added to the source is reported as %q", extra)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], `"func Removed"`) {
		t.Errorf("a symbol removed from the source is reported as %q", missing)
	}
}

// claimsContributorFunc is ClaimsContributor's shape written as a function type:
// one method, Claims, taking a context and a Principal and returning a claim
// mapping. The assertion below makes that shape a compile-time fact, so an
// interface that grows another method, loses Claims or moves it off this
// signature no longer compiles.
type claimsContributorFunc func(context.Context, Principal) (map[string]any, error)

func (f claimsContributorFunc) Claims(ctx context.Context, p Principal) (map[string]any, error) {
	return f(ctx, p)
}

var _ ClaimsContributor = claimsContributorFunc(nil)

// TestTheClaimsContributorDocHoldsTheDesignClaimSet holds the reserved claim
// names in the ClaimsContributor doc to the design's closed set, name for name
// and in order. The set is written in the interface comment alone, so this reads
// it back out of the source: a name added, dropped, reordered or respelled there
// fails here.
func TestTheClaimsContributorDocHoldsTheDesignClaimSet(t *testing.T) {
	got := reservedClaimNamesFromDoc(t)
	want := []string{
		"iss", "sub", "aud", "exp", "nbf", "iat", "jti", "sid",
		"amr", "acr", "auth_time", "act", "imp", "imp_tenant", "client_id", "typ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the doc's reserved claim set is %q; the design's is %q", got, want)
	}
}

// reservedClaimNamesFromDoc returns the reserved claim names listed in the
// ClaimsContributor doc comment, in the order the design lists them. It reads
// the set back out of the source, so a name added, dropped, reordered or
// respelled in the comment changes what it returns.
func reservedClaimNamesFromDoc(t *testing.T) []string {
	t.Helper()
	const anchor = "The reserved claim names are a closed set: "
	norm := strings.Join(strings.Fields(claimsContributorDoc(t)), " ")
	start := strings.Index(norm, anchor)
	if start < 0 {
		t.Fatalf("the ClaimsContributor doc does not state the reserved claim set; it reads:\n%s", norm)
	}
	rest := norm[start+len(anchor):]
	end := strings.IndexByte(rest, '.')
	if end < 0 {
		t.Fatalf("the reserved claim set in the doc does not end with a period; it reads:\n%s", norm)
	}
	return strings.Split(rest[:end], ", ")
}

// TestTheClaimsContributorDocAndTheTokenModuleHoldOneSet forces the reserved
// claim set the ClaimsContributor doc promises and the set the token module
// enforces to be one set. The doc is the contract a host reads; the module's
// reservedClaims map, in another package, is what refuses a contributor claim at
// issue time. The two are written in two files and nothing but this test ties
// them, so a name dropped from reservedClaims while the doc still lists it — or
// the reverse — passes every other test and leaves the contract describing
// behaviour that is not there.
//
// The set is read from the token module's source rather than through an
// identifier: this test is in package authn, and importing the token module here
// would close an import cycle (the token module itself imports this package).
func TestTheClaimsContributorDocAndTheTokenModuleHoldOneSet(t *testing.T) {
	doc := reservedClaimNamesFromDoc(t)
	enforced := reservedClaimNamesFromToken(t)

	inDoc := make(map[string]struct{}, len(doc))
	for _, name := range doc {
		inDoc[name] = struct{}{}
	}
	inToken := make(map[string]struct{}, len(enforced))
	for _, name := range enforced {
		inToken[name] = struct{}{}
	}
	for _, name := range doc {
		if _, ok := inToken[name]; !ok {
			t.Errorf("the doc reserves %q but the token module does not enforce it", name)
		}
	}
	for _, name := range enforced {
		if _, ok := inDoc[name]; !ok {
			t.Errorf("the token module enforces %q but the doc does not list it", name)
		}
	}
	if len(doc) != len(enforced) {
		t.Errorf("the doc lists %d reserved names, the token module enforces %d", len(doc), len(enforced))
	}
}

// reservedClaimNamesFromToken reads the keys of the token module's reservedClaims
// map literal out of token/claims.go, the set the module actually refuses a
// contributor claim against. A rename of that map or a change of its shape fails
// the test rather than silently reading nothing.
func reservedClaimNamesFromToken(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("token", "claims.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing token/claims.go: %v", err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "reservedClaims" {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("the token module's reservedClaims has no value")
				}
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					t.Fatalf("the token module's reservedClaims is not a composite literal")
				}
				names := make([]string, 0, len(lit.Elts))
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						t.Fatalf("the token module's reservedClaims holds a non key-value element")
					}
					bl, ok := kv.Key.(*ast.BasicLit)
					if !ok || bl.Kind != token.STRING {
						t.Fatalf("the token module's reservedClaims holds a non-string key")
					}
					s, err := strconv.Unquote(bl.Value)
					if err != nil {
						t.Fatalf("unquoting the reserved claim %s: %v", bl.Value, err)
					}
					names = append(names, s)
				}
				return names
			}
		}
	}
	t.Fatal("token/claims.go declares no reservedClaims map")
	return nil
}

// claimsContributorDoc returns the doc comment of the ClaimsContributor
// declaration read from claims.go.
func claimsContributorDoc(t *testing.T) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "claims.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing claims.go: %v", err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "ClaimsContributor" {
				continue
			}
			if ts.Doc != nil {
				return ts.Doc.Text()
			}
			if gd.Doc != nil {
				return gd.Doc.Text()
			}
		}
	}
	t.Fatal("claims.go declares no ClaimsContributor with a doc comment")
	return ""
}
