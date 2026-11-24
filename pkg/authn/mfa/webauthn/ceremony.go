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

type ceremony struct {
	Profile string `json:"profile,omitempty"`
	Session []byte `json:"session"`
}

const (
	profilePasskey     = "passkey"
	profileSecurityKey = "security_key"
)

const (
	fieldCredential = "credential"
	fieldProfile    = "profile"
)

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

type libraryCredential = libwebauthn.Credential

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

func handleBytesOf(handle string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(handle)
	if err != nil || len(b) == 0 {
		return nil, errors.New("the user handle is not a stored value")
	}
	return b, nil
}

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

func signCountOf(n int64) (uint32, error) {
	if n < 0 || n > math.MaxUint32 {
		return 0, errors.New("the signature counter is not a stored value")
	}
	return uint32(n), nil
}

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

func marshalCeremony(profile string, session *libwebauthn.SessionData) ([]byte, error) {
	data, err := session.MarshalMsg(nil)
	if err != nil {
		return nil, fmt.Errorf("serializing the ceremony: %w", err)
	}
	return json.Marshal(ceremony{Profile: profile, Session: data})
}

// Only bytes this module wrote are handed to the library, whose decoder trusts a length prefix.
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

type assertionResult struct {
	credential   *libwebauthn.Credential
	userVerified bool
}

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
