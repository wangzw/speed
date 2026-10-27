package webauthn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/go-webauthn/webauthn/protocol"
	libwebauthn "github.com/go-webauthn/webauthn/webauthn"
)

// This file is the whole of this module's contact with the WebAuthn library.
// The library is at version 0.x and its types change between releases, so no
// type of it leaves this package: the factor and the login method speak the root
// package's Factor, Step and LoginMethod, and everything below translates. A
// library type in an exported signature would tie every caller to the version
// this module happens to pin.
//
// The ceremony data is kept in the library's own serialization — the value it
// returns from Begin and reads back at Finish — because a value it did not
// produce, reshaped on the way through storage, fails verification in ways that
// look like a client fault.

// ceremony is the module's private state between the steps of one ceremony: the
// library's serialized SessionData, plus the profile a registration is being
// made under. The profile is the module's own, so it is carried beside the
// library's bytes rather than inside them.
type ceremony struct {
	Profile string `json:"profile,omitempty"`
	Session []byte `json:"session"`
}

// The registration profiles a client may ask for. A passkey is a discoverable
// credential with user verification required, usable as a first factor or a
// second one; a security key is bound and not discoverable, and is a second
// factor only.
const (
	profilePasskey     = "passkey"
	profileSecurityKey = "security_key"
)

// fieldCredential is the field a client submits a WebAuthn response in, and
// fieldProfile the one an enrollment names its profile in.
const (
	fieldCredential = "credential"
	fieldProfile    = "profile"
)

// newRelyingParty builds the library's relying party from this module's
// configuration. The attestation preference is none, so no authenticator is
// asked to attest; the two timeouts are enforced server-side, which is the only
// setting under which the library records an expiry on a ceremony. User
// verification is preferred by default, which is what a security key used as a
// second factor asks for; a passkey registration overrides it to required, and
// so does the passkey login assertion.
func newRelyingParty(cfg resolved) (*libwebauthn.WebAuthn, error) {
	return libwebauthn.New(&libwebauthn.Config{
		RPID:                  cfg.rpID,
		RPDisplayName:         cfg.rpName,
		RPOrigins:             cfg.origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationPreferred,
		},
		Timeouts: libwebauthn.TimeoutsConfig{
			Login:        libwebauthn.TimeoutConfig{Enforce: true},
			Registration: libwebauthn.TimeoutConfig{Enforce: true},
		},
	})
}

// libraryCredential is the library's credential record under a name of this
// package's, so the factor and the login method can name a list of them without
// naming the library at every use.
type libraryCredential = libwebauthn.Credential

// user is the library's view of an account: the opaque handle, a name and a
// display name for the authenticator to show, and the credentials the account
// owns so the library can compare an assertion against them.
type user struct {
	id      []byte
	name    string
	display string
	creds   []libwebauthn.Credential
}

func (u user) WebAuthnID() []byte                            { return u.id }
func (u user) WebAuthnName() string                          { return u.name }
func (u user) WebAuthnDisplayName() string                   { return u.display }
func (u user) WebAuthnCredentials() []libwebauthn.Credential { return u.creds }

// handleBytesOf decodes a stored handle. A handle that is not base64url is a row
// this module did not write, which the caller treats as an unknown account.
func handleBytesOf(handle string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(handle)
	if err != nil || len(b) == 0 {
		return nil, errors.New("the user handle is not a stored value")
	}
	return b, nil
}

// toLibrary reconstructs a credential the library can verify against from the
// columns this module keeps. The attestation format is "none", the only one this
// module accepts, which is what the exclusion and appID extension checks read.
func (r credentialRow) toLibrary() (libwebauthn.Credential, error) {
	id, err := base64.RawURLEncoding.DecodeString(r.CredentialID)
	if err != nil || len(id) == 0 {
		return libwebauthn.Credential{}, errors.New("the credential identifier is not a stored value")
	}
	pk, err := base64.RawURLEncoding.DecodeString(r.PublicKey)
	if err != nil || len(pk) == 0 {
		return libwebauthn.Credential{}, errors.New("the public key is not a stored value")
	}
	aaguid, err := base64.RawURLEncoding.DecodeString(r.AAGUID)
	if err != nil {
		return libwebauthn.Credential{}, errors.New("the aaguid is not a stored value")
	}
	var transports []protocol.AuthenticatorTransport
	for _, t := range r.transportList() {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	count, err := signCountOf(r.SignCount)
	if err != nil {
		return libwebauthn.Credential{}, err
	}
	raw := rawFlags(true, r.UserVerified, r.BackupEligible, r.BackupState)
	return libwebauthn.Credential{
		ID:                id,
		PublicKey:         pk,
		AttestationType:   r.AttestationType,
		AttestationFormat: "none",
		Transport:         transports,
		Flags:             libwebauthn.NewCredentialFlags(raw),
		Authenticator: libwebauthn.Authenticator{
			AAGUID:    aaguid,
			SignCount: count,
		},
	}, nil
}

// signCountOf is a stored signature counter as the 32-bit one the library reads.
// The counter is a uint32 on the wire, so a stored value outside that width is a
// row this module did not write, which the caller treats as a factor with no
// usable credential rather than wrapping it.
func signCountOf(n int64) (uint32, error) {
	if n < 0 || n > math.MaxUint32 {
		return 0, errors.New("the signature counter is not a stored value")
	}
	return uint32(n), nil
}

// rawFlags packs the four flag bits this module stores into the protocol's
// octet. User presence is always set on a credential this module holds: an
// assertion that did not carry it is refused by the library before it reaches
// here.
func rawFlags(up, uv, be, bs bool) protocol.AuthenticatorFlags {
	var f protocol.AuthenticatorFlags
	if up {
		f |= protocol.FlagUserPresent
	}
	if uv {
		f |= protocol.FlagUserVerified
	}
	if be {
		f |= protocol.FlagBackupEligible
	}
	if bs {
		f |= protocol.FlagBackupState
	}
	return f
}

// marshalCeremony serializes the library's session data and the profile into the
// bytes the module keeps between steps.
func marshalCeremony(profile string, session *libwebauthn.SessionData) ([]byte, error) {
	data, err := session.MarshalMsg(nil)
	if err != nil {
		return nil, fmt.Errorf("serializing the ceremony: %w", err)
	}
	return json.Marshal(ceremony{Profile: profile, Session: data})
}

// parseCeremony reads the module's state back. A value this module did not write
// is refused: the library's decoder trusts a length prefix, so only bytes the
// module produced are handed to it.
func parseCeremony(b []byte) (profile string, session libwebauthn.SessionData, err error) {
	var c ceremony
	if err = json.Unmarshal(b, &c); err != nil {
		return "", session, errors.New("the ceremony data is not one this module wrote")
	}
	if len(c.Session) == 0 {
		return "", session, errors.New("the ceremony data carries no session")
	}
	if _, err = session.UnmarshalMsg(c.Session); err != nil {
		return "", session, errors.New("the ceremony data is not one this module wrote")
	}
	return c.Profile, session, nil
}

// beginRegistration makes the creation options and the session for one
// registration. A passkey is required to be discoverable and to verify the user;
// a security key is neither, and is a second factor only. The account's existing
// credentials are excluded, so the same authenticator is not registered twice
// under one account.
func beginRegistration(wa *libwebauthn.WebAuthn, u user, passkey bool) (options json.RawMessage, state []byte, err error) {
	descriptors := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for _, c := range u.creds {
		descriptors = append(descriptors, c.Descriptor())
	}
	selection := protocol.AuthenticatorSelection{
		ResidentKey:        protocol.ResidentKeyRequirementPreferred,
		RequireResidentKey: protocol.ResidentKeyNotRequired(),
		UserVerification:   protocol.VerificationPreferred,
	}
	if passkey {
		selection = protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		}
	}
	opts := []libwebauthn.RegistrationOption{
		libwebauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		libwebauthn.WithExclusions(descriptors),
		libwebauthn.WithAuthenticatorSelection(selection),
	}
	creation, session, err := wa.BeginRegistration(u, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("starting a registration: %w", err)
	}
	options, err = json.Marshal(creation.Response)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the registration options: %w", err)
	}
	profile := profileSecurityKey
	if passkey {
		profile = profilePasskey
	}
	state, err = marshalCeremony(profile, session)
	if err != nil {
		return nil, nil, err
	}
	return options, state, nil
}

// finishRegistration verifies a client's registration response and returns the
// profile the registration was begun under and the credential to store. The
// response bytes are the client's, so only a bounded one is parsed.
func finishRegistration(wa *libwebauthn.WebAuthn, u user, state []byte, body []byte) (profile string, cred *libwebauthn.Credential, err error) {
	profile, session, err := parseCeremony(state)
	if err != nil {
		return "", nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return "", nil, fmt.Errorf("reading the registration response: %w", err)
	}
	cred, err = wa.CreateCredential(u, session, parsed)
	if err != nil {
		return "", nil, fmt.Errorf("checking the registration response: %w", err)
	}
	return profile, cred, nil
}

// beginAssertion makes the assertion options and the session that asks a user to
// authenticate with a credential they already hold. The allowed credentials are
// the ones passed, so an assertion is accepted only from one of them. User
// verification keeps the relying party's default of preferred: a second factor
// may verify the user but is not required to.
func beginAssertion(wa *libwebauthn.WebAuthn, u user, allowed []libwebauthn.Credential) (options json.RawMessage, state []byte, err error) {
	descriptors := make([]protocol.CredentialDescriptor, 0, len(allowed))
	for _, c := range allowed {
		descriptors = append(descriptors, c.Descriptor())
	}
	assertion, session, err := wa.BeginLogin(u, libwebauthn.WithAllowedCredentials(descriptors))
	if err != nil {
		return nil, nil, fmt.Errorf("starting an assertion: %w", err)
	}
	options, err = json.Marshal(assertion.Response)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the assertion options: %w", err)
	}
	state, err = marshalCeremony("", session)
	if err != nil {
		return nil, nil, err
	}
	return options, state, nil
}

// beginDiscoverableAssertion makes the assertion options for a passkey login,
// where the client chooses the credential and no relying party-side account is
// named: the handle it returns is what finds the account at the finish. The
// assertion requires user verification, so a passkey proves two factors at once
// and an assertion without it is refused by the library.
func beginDiscoverableAssertion(wa *libwebauthn.WebAuthn) (options json.RawMessage, state []byte, err error) {
	assertion, session, err := wa.BeginDiscoverableLogin(
		libwebauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, nil, fmt.Errorf("starting a passkey login: %w", err)
	}
	options, err = json.Marshal(assertion.Response)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the passkey options: %w", err)
	}
	state, err = marshalCeremony("", session)
	if err != nil {
		return nil, nil, err
	}
	return options, state, nil
}

// assertionResult is what a verified assertion yields: the credential the
// library advanced, and whether the ceremony itself verified the user, which is
// the fact the module reports and not the credential's latched record.
type assertionResult struct {
	credential   *libwebauthn.Credential
	userVerified bool
}

// finishAssertion verifies a client's assertion response against the user and
// the stored session. parsed carries the ceremony's own user-verification bit,
// which the credential's record does not.
func finishAssertion(wa *libwebauthn.WebAuthn, u user, state []byte, body []byte) (assertionResult, error) {
	_, session, err := parseCeremony(state)
	if err != nil {
		return assertionResult{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return assertionResult{}, fmt.Errorf("reading the assertion: %w", err)
	}
	cred, err := wa.ValidateLogin(u, session, parsed)
	if err != nil {
		return assertionResult{}, fmt.Errorf("checking the assertion: %w", err)
	}
	return assertionResult{credential: cred, userVerified: parsed.Response.AuthenticatorData.Flags.HasUserVerified()}, nil
}

// finishDiscoverableAssertion verifies a passkey login's assertion. The handler
// resolves the account from the handle the client returned; the user it returns
// is the account the assertion is checked against.
func finishDiscoverableAssertion(wa *libwebauthn.WebAuthn, handler libwebauthn.DiscoverableUserHandler, state []byte, body []byte) (u libwebauthn.User, res assertionResult, err error) {
	_, session, err := parseCeremony(state)
	if err != nil {
		return nil, assertionResult{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return nil, assertionResult{}, fmt.Errorf("reading the assertion: %w", err)
	}
	u, cred, err := wa.ValidatePasskeyLogin(handler, session, parsed)
	if err != nil {
		return nil, assertionResult{}, fmt.Errorf("checking the assertion: %w", err)
	}
	return u, assertionResult{credential: cred, userVerified: parsed.Response.AuthenticatorData.Flags.HasUserVerified()}, nil
}
