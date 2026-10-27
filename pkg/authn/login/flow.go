package login

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// The states of the flow machine. The set is closed: first is the first factor,
// processing is the moment a callback's answer is being handled, second is the
// second factor, enroll is a registration, ready is a flow whose steps are done,
// and done and failed are the two tombstones.
const (
	stateFirst      = "first"
	stateProcessing = "processing"
	stateSecond     = "second"
	stateEnroll     = "enroll"
	stateReady      = "ready"
	stateDone       = "done"
	stateFailed     = "failed"
)

// The steps a client's answer may name, the closed set of the answer route's
// step field.
const (
	stepAnswer   = "answer"
	stepFactor   = "factor"
	stepEnroll   = "enroll"
	stepComplete = "complete"
)

// The maximum number of answers one flow accepts.
const maxAnswers = 8

// The greatest a method's private state may be, the greatest a start field's
// value may be, the greatest an answer field's value may be, and the most fields
// an input may carry. The surface states the private-state and start-field
// bounds; the answer bound is the engine's own, and it is larger because an
// answer may carry a WebAuthn credential.
const (
	maxPrivateBytes = 8 << 10
	// maxStartFieldBytes bounds one field of a start request's input.
	maxStartFieldBytes = 512
	// maxAnswerFieldBytes bounds one field of a client's answer. A WebAuthn
	// credential's JSON runs past a kilobyte, over the start bound; this stays
	// far below the request body's own limit.
	maxAnswerFieldBytes = 8 << 10
	maxInputFields      = 8
)

// recordVersion is the format version this build writes and the only one it
// reads. A record carrying another version is refused, so that a rollout that
// changes the format does not read an old record as a new one.
const recordVersion = 1

// flowKeyPrefix begins every flow record's key. The key-value store is not
// namespaced, so a key carries this capability's prefix.
const flowKeyPrefix = "authn:flow:"

// record is one login flow, the key-value store's value. Its JSON field names
// are the design's, and it is read back with unknown fields refused.
type record struct {
	Version     int          `json:"v"`
	Purpose     string       `json:"purpose"`
	Method      string       `json:"method,omitempty"`
	Provider    string       `json:"provider,omitempty"`
	State       string       `json:"state"`
	Seq         int          `json:"seq"`
	Binding     string       `json:"binding"`
	Account     string       `json:"account,omitempty"`
	Proof       *model.Proof `json:"proof,omitempty"`
	MFA         mfaState     `json:"mfa"`
	MethodStep  string       `json:"method_step,omitempty"`
	MethodState []byte       `json:"method_state,omitempty"`
	Merged      bool         `json:"merged,omitempty"`
	Delivery    string       `json:"delivery,omitempty"`
	ReturnTo    string       `json:"return_to,omitempty"`
	Session     string       `json:"session,omitempty"`
	// RequireMulti records, for a link or an enrollment, that the account held
	// an active factor when the flow started: the completion re-checks the
	// freshness the start fixed, so a registration's own factor -- made active
	// during its last step -- does not raise the bar its own completion clears.
	RequireMulti bool   `json:"require_multi,omitempty"`
	Answers      int    `json:"answers"`
	Sends        int    `json:"sends"`
	Locale       string `json:"locale,omitempty"`
	// Actor, Impersonation and Scope record how an impersonating identity
	// started the flow: the operator, the grant the session named and the grant's
	// tenant. Account stays the effective account. They are empty for a flow the
	// account's own holder started.
	Actor         string    `json:"actor,omitempty"`
	Impersonation string    `json:"impersonation,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// mfaState is the second-factor part of a flow. offered is the usable factors
// the step-up flow is to show, each with the label the client displays; passed
// is the conclusion of the second factor that held; enrolled is the identifier
// of a factor a registration has made, for the completion to enable.
type mfaState struct {
	Required     bool         `json:"required,omitempty"`
	Offered      []factorView `json:"offered,omitempty"`
	ChallengeRef string       `json:"challenge_ref,omitempty"`
	Attempts     int          `json:"attempts,omitempty"`
	Passed       *passState   `json:"passed,omitempty"`
	Enrolled     string       `json:"enrolled,omitempty"`
}

// passState is the conclusion of a second factor that held, kept so that the
// assurance can be composed at the completion.
type passState struct {
	Methods      []string `json:"methods"`
	UserVerified bool     `json:"user_verified,omitempty"`
}

func passOfPassed(p authn.Pass) *passState {
	methods := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		methods[i] = string(m)
	}
	return &passState{Methods: methods, UserVerified: p.UserVerified}
}

func (s *passState) toPass() *authn.Pass {
	if s == nil {
		return nil
	}
	methods := make([]authn.Method, len(s.Methods))
	for i, m := range s.Methods {
		methods[i] = authn.Method(m)
	}
	return &authn.Pass{Methods: methods, UserVerified: s.UserVerified}
}

// newFlowToken draws a 256-bit flow token and returns it as the string the
// cookie or the answer body carries. The value is a secret.
func newFlowToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%s: drawing a flow token: %w", ModuleName, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// tokenDigest is the hexadecimal SHA-256 of a flow token. It is both the
// record's key suffix and its binding value: the token is the binding secret, so
// proving possession of it is proving the binding.
func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// flowKey is the key-value key of a flow token.
func flowKey(token string) string { return flowKeyPrefix + tokenDigest(token) }

// bindingMatches reports, in constant time, whether a presented token's digest
// equals a record's stored binding.
func bindingMatches(presented, stored string) bool {
	want := tokenDigest(presented)
	if len(want) != len(stored) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(stored)) == 1
}

// errFlowInvalid wraps the sentinel of a flow that is expired, used, bound to
// someone else or consumed by a concurrent request.
func errFlowInvalid(why string) error {
	return fmt.Errorf("%w: %s", authn.ErrFlowInvalid, why)
}

// loadFlow reads a flow record by the presented token and checks its binding.
// The returned bytes are what a later compare-and-swap must match.
func (p *product) loadFlow(ctx context.Context, token string) (record, []byte, error) {
	key := flowKey(token)
	raw, present, err := p.kv.Get(ctx, key)
	if err != nil {
		return record{}, nil, fmt.Errorf("%s: reading a flow record: %w", ModuleName, err)
	}
	if !present {
		return record{}, nil, errFlowInvalid("no such flow")
	}
	rec, err := decodeRecord(raw)
	if err != nil {
		return record{}, nil, errFlowInvalid("the record cannot be read")
	}
	if !bindingMatches(token, rec.Binding) {
		return record{}, nil, errFlowInvalid("the binding does not match")
	}
	return rec, raw, nil
}

// decodeRecord reads a flow record and refuses one of another format version or
// with an unknown field.
func decodeRecord(raw []byte) (record, error) {
	var rec record
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return record{}, err
	}
	if rec.Version != recordVersion {
		return record{}, fmt.Errorf("unknown record version %d", rec.Version)
	}
	return rec, nil
}

// storeFlow writes a new flow record, creating it with the flow's time to live,
// and returns the bytes it wrote, which a later compare-and-swap matches. It is
// only used to create.
func (p *product) storeFlow(ctx context.Context, token string, rec record, ttl time.Duration) ([]byte, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding a flow record: %w", ModuleName, err)
	}
	if err := p.kv.Set(ctx, flowKey(token), raw, ttl); err != nil {
		return nil, fmt.Errorf("%s: writing a flow record: %w", ModuleName, err)
	}
	return raw, nil
}

// advance swaps a flow record from the bytes it was read as to the record built
// from it. A false result means another writer moved the record first, which the
// caller answers as an invalid flow and never retries.
func (p *product) advance(ctx context.Context, token string, old []byte, rec record) (bool, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return false, fmt.Errorf("%s: encoding a flow record: %w", ModuleName, err)
	}
	swapped, err := p.kv.CompareAndSwap(ctx, flowKey(token), old, raw)
	if err != nil {
		return false, fmt.Errorf("%s: swapping a flow record: %w", ModuleName, err)
	}
	return swapped, nil
}

// deleteFlow removes a flow record. It is used to abandon a flow and is
// idempotent.
func (p *product) deleteFlow(ctx context.Context, token string) error {
	if err := p.kv.Delete(ctx, flowKey(token)); err != nil {
		return fmt.Errorf("%s: removing a flow record: %w", ModuleName, err)
	}
	return nil
}

// usableMethod reports whether a method name is one the engine took in and is
// not turned off. It is the answer to both "does this method exist" and "may it
// be used".
func (p *product) method(name string) (authn.LoginMethod, bool) {
	m, ok := p.methods[name]
	return m, ok
}

// flowProvider is the value a flow records as the provider its callback path
// must carry. An enterprise SSO method is dispatched at a provider identifier
// the start input names, so its record takes that value; every other method's
// callback path is built from its own name, so its record takes the method name
// and the start input cannot change it.
func flowProvider(method string, input map[string]string) string {
	if enterpriseSSOMethod(method) {
		if p := input["provider"]; p != "" {
			return p
		}
	}
	return method
}

// enterpriseSSOMethod reports whether a method is an enterprise SSO protocol
// method, whose callback path carries the IdP identifier a start names rather
// than the method's own name.
func enterpriseSSOMethod(method string) bool {
	switch method {
	case model.SSOProtocolOIDC, model.SSOProtocolSAML:
		return true
	}
	return false
}

// deliveryAllowed reports whether a delivery mode is one the deployment offers.
func (p *product) deliveryAllowed(mode string) bool {
	for _, m := range p.settings.deliveryModes() {
		if m == mode {
			return true
		}
	}
	return false
}
