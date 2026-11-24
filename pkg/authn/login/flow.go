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
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
)

const (
	stateFirst      = "first"
	stateProcessing = "processing"
	stateSignup     = "signup"
	stateSecond     = "second"
	stateEnroll     = "enroll"
	stateReprove    = "reprove"
	stateReady      = "ready"
	stateDone       = "done"
	stateFailed     = "failed"
)

const (
	stepAnswer   = "answer"
	stepFactor   = "factor"
	stepEnroll   = "enroll"
	stepComplete = "complete"
)

const maxAnswers = 8

const (
	maxPrivateBytes     = 8 << 10
	maxStartFieldBytes  = 512
	maxAnswerFieldBytes = 8 << 10
	maxInputFields      = 8
)

const recordVersion = 1

const flowKeyPrefix = "authn:flow:"

type record struct {
	Version     int              `json:"v"`
	Purpose     string           `json:"purpose"`
	Method      string           `json:"method,omitempty"`
	Provider    string           `json:"provider,omitempty"`
	State       string           `json:"state"`
	Seq         int              `json:"seq"`
	Binding     string           `json:"binding"`
	Account     string           `json:"account,omitempty"`
	Proof       *model.Proof     `json:"proof,omitempty"`
	Pending     *resolve.Pending `json:"pending,omitempty"`
	MFA         mfaState         `json:"mfa"`
	MethodStep  string           `json:"method_step,omitempty"`
	MethodState []byte           `json:"method_state,omitempty"`
	// Merged is read only to refuse a record left by the retired automatic merge; this build never writes it true.
	Merged   bool   `json:"merged,omitempty"`
	Delivery string `json:"delivery,omitempty"`
	ReturnTo string `json:"return_to,omitempty"`
	Session  string `json:"session,omitempty"`
	// RequireMulti is fixed at start so a registration's own factor does not raise the bar its own completion clears.
	RequireMulti  bool      `json:"require_multi,omitempty"`
	Answers       int       `json:"answers"`
	Sends         int       `json:"sends"`
	Locale        string    `json:"locale,omitempty"`
	Actor         string    `json:"actor,omitempty"`
	Impersonation string    `json:"impersonation,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type mfaState struct {
	Required     bool         `json:"required,omitempty"`
	Offered      []factorView `json:"offered,omitempty"`
	ChallengeRef string       `json:"challenge_ref,omitempty"`
	Attempts     int          `json:"attempts,omitempty"`
	Passed       *passState   `json:"passed,omitempty"`
	Enrolled     string       `json:"enrolled,omitempty"`
	EnrollMulti  bool         `json:"enroll_multi,omitempty"`
}

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

func newFlowToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%s: drawing a flow token: %w", ModuleName, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func flowKey(token string) string { return flowKeyPrefix + tokenDigest(token) }

func bindingMatches(presented, stored string) bool {
	want := tokenDigest(presented)
	if len(want) != len(stored) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(stored)) == 1
}

func errFlowInvalid(why string) error {
	return fmt.Errorf("%w: %s", authn.ErrFlowInvalid, why)
}

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
	if rec.Merged {
		return record{}, fmt.Errorf("the record is from the retired automatic merge")
	}
	return rec, nil
}

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

func (p *product) deleteFlow(ctx context.Context, token string) error {
	if err := p.kv.Delete(ctx, flowKey(token)); err != nil {
		return fmt.Errorf("%s: removing a flow record: %w", ModuleName, err)
	}
	return nil
}

func (p *product) method(name string) (authn.LoginMethod, bool) {
	m, ok := p.methods[name]
	return m, ok
}

func flowProvider(method string, input map[string]string) string {
	if enterpriseSSOMethod(method) {
		if p := input["provider"]; p != "" {
			return p
		}
	}
	return method
}

func enterpriseSSOMethod(method string) bool {
	switch method {
	case model.SSOProtocolOIDC, model.SSOProtocolSAML:
		return true
	}
	return false
}

func (p *product) deliveryAllowed(mode string) bool {
	for _, m := range p.settings.deliveryModes() {
		if m == mode {
			return true
		}
	}
	return false
}
