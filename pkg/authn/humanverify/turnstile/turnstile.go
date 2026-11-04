// Package turnstile is the Cloudflare Turnstile provider of the
// human-verification gate: it posts the token the client obtained from the
// widget to the provider's siteverify endpoint and reads the answer.
//
// The answer is judged on three things: success, the hostname the widget ran
// on, and the action the widget was rendered with. A proof the provider accepts
// is accepted once.
//
// Single use has a local half as well as the provider's. Cloudflare refuses a
// token it has already spent with its duplicate error code, which this package
// reads as an invalid proof, but that refusal is a guarantee of the upstream
// service rather than of this one: a verifier that was mocked, proxied or
// replaced would hand the same token back twice. After siteverify accepts a
// proof, this provider therefore claims the token's digest in the key-value
// store with a set-if-absent, and a token whose digest is already claimed is
// refused. The token itself never reaches the store: the key is its SHA-256
// digest, so a store dump carries no credential. The claim expires after
// [ClaimTTL], which covers the whole time a Turnstile token is valid.
//
// A refused answer is told apart by the provider's own error codes rather than
// collapsed into one. The codes that name a problem with the proof -- missing,
// invalid, expired or already spent -- are an invalid proof. Every other code
// names a fault on the provider's side or in this deployment's configuration (a
// bad secret or widget key, an internal error) or is one this package does not
// recognise; those are an outage: the proof was not judged, so nothing is sent
// and the client is not told its proof was wrong.
//
// The secret never reaches a log line, an error text or a metric: the token is
// a credential and so is the secret, and the siteverify call carries both in
// its body alone.
package turnstile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ProviderName is the mechanism name this implementation reports to the seam.
const ProviderName = "turnstile"

// DefaultVerifyURL is the provider's siteverify endpoint, the address a
// deployment not naming another one posts proofs to.
const DefaultVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// DefaultTimeout is the whole time one verification may take: the connection,
// the request and the answer together.
const DefaultTimeout = 10 * time.Second

// The declared range of the whole-verification timeout.
const (
	TimeoutMin = time.Second
	TimeoutMax = 30 * time.Second
)

// maxTokenLen is the longest token this provider admits. A Turnstile token is
// a few hundred characters; the bound is far above one and catches a body sent
// in its place, which the seam answers with 400.
const maxTokenLen = 4096

// maxResponseBytes caps the answer read from siteverify. The provider's answer
// is a small JSON object; the cap is the same one the restricted client
// applies, repeated here so the guarantee holds under a client a test injects.
const maxResponseBytes = egress.MaxBodyBytes

// ClaimTTL is how long a spent token is remembered. A Turnstile token is valid
// for 300 seconds from the challenge it answers, so remembering the claim for
// that long covers the whole time it could be replayed here.
const ClaimTTL = 5 * time.Minute

// spentKeyPrefix prefixes the digest of a claimed token. The key is the digest
// and never the token, because a key is a value the store and its diagnostics
// may carry.
const spentKeyPrefix = "authn:human:turnstile:spent:"

// Options configures a [Provider].
type Options struct {
	// SiteKey is the public widget key the deployment registered with the
	// provider. It is what a client-side widget is rendered with, not a
	// credential.
	SiteKey string
	// Secret is the secret key. It authenticates the siteverify call and is a
	// credential.
	Secret string
	// VerifyURL is the siteverify endpoint. Empty takes [DefaultVerifyURL].
	VerifyURL string
	// Hostname is the hostname the widget must have run on, as the provider
	// reports it. A proof the provider accepted for another hostname is not
	// accepted here.
	Hostname string
	// Action is the action the widget is rendered with; the provider's answer
	// must name it. It is required, so a token obtained under one action is not
	// accepted for another.
	Action string
	// Timeout bounds one verification as a whole. Zero takes [DefaultTimeout].
	Timeout time.Duration
	// HTTP is the outbound client. Nil takes the restricted egress client,
	// which speaks https to a public address only. A client given here is used
	// as it stands, so a test may point it at a loopback server its own roots
	// trust.
	HTTP *http.Client
	// Store is where a claimed token's digest is kept, so a proof accepted once
	// is refused when it is presented again even if siteverify would accept it
	// a second time. It is required: without it the single-use property is
	// only the upstream service's promise, not this deployment's.
	Store kv.KV
}

// Provider is the Cloudflare Turnstile verifier.
type Provider struct {
	siteKey   string
	secret    string
	verifyURL string
	hostname  string
	action    string
	timeout   time.Duration
	client    *http.Client
	store     kv.KV
}

var _ authn.HumanVerifier = (*Provider)(nil)

// New builds the provider. The site key, the secret, the expected hostname and
// the action are required: a deployment that turns the gate on without them
// cannot check a proof, and starting anyway would send messages on a check
// nobody supplied.
func New(o Options) (*Provider, error) {
	if strings.TrimSpace(o.SiteKey) == "" {
		return nil, errors.New("turnstile: a site key is required")
	}
	if strings.TrimSpace(o.Secret) == "" {
		return nil, errors.New("turnstile: a secret is required")
	}
	hostname := strings.TrimSpace(o.Hostname)
	if hostname == "" {
		return nil, errors.New("turnstile: an expected hostname is required")
	}
	action := strings.TrimSpace(o.Action)
	if action == "" {
		return nil, errors.New("turnstile: an action is required")
	}
	if o.Store == nil {
		return nil, errors.New("turnstile: a key-value store is required to claim spent tokens")
	}
	verifyURL := strings.TrimSpace(o.VerifyURL)
	if verifyURL == "" {
		verifyURL = DefaultVerifyURL
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := o.HTTP
	if client == nil {
		client = egress.New(egress.Options{})
		// The restricted client is wrapped from outside, so the call carries
		// the span of its context across the hop and leaves one outbound
		// record. The record names the endpoint, the status and the time and
		// carries neither the body — where the secret and the token travel —
		// nor the answer.
		client.Transport = observability.Transport(log.Transport(client.Transport))
	}
	return &Provider{
		siteKey:   o.SiteKey,
		secret:    o.Secret,
		verifyURL: verifyURL,
		hostname:  hostname,
		action:    action,
		timeout:   timeout,
		client:    client,
		store:     o.Store,
	}, nil
}

// Required reports true for every scope: this gate is a condition of sending
// wherever it is configured.
func (p *Provider) Required(authn.HumanScope) bool { return true }

// Provider reports the mechanism name for every scope.
func (p *Provider) Provider(authn.HumanScope) string { return ProviderName }

// SiteKey reports the public widget key a client-side widget is rendered with.
func (p *Provider) SiteKey(authn.HumanScope) string { return p.siteKey }

// Action reports the action a client-side widget must be rendered with, so the
// token it yields is bound to the action the provider's answer is checked
// against.
func (p *Provider) Action(authn.HumanScope) string { return p.action }

// Verify posts the proof to siteverify and judges the answer.
//
// A token is single-use. Cloudflare refuses a token it has already spent with
// its duplicate code, which reads here as an invalid proof, and after an
// accepted answer this provider claims the token's digest in the key-value
// store, so a replay is refused locally even when siteverify would accept it
// again. A refused answer whose codes all name a problem with the proof is an
// invalid proof too. An answer that is not a 2xx, not the provider's response,
// accepted for another hostname or action, or refused for a provider-side
// reason (its own internal error, a key or widget misconfiguration, a code this
// package does not know) is an outage: the proof was not judged, so nothing is
// sent. So is a store that cannot record the claim, because a proof nobody
// recorded could be presented again.
func (p *Provider) Verify(ctx context.Context, proof authn.HumanProof, _ authn.HumanScope) error {
	if proof.Token == "" {
		return fmt.Errorf("%w: turnstile: no proof was presented", authn.ErrHumanVerificationRequired)
	}
	if len(proof.Token) > maxTokenLen {
		return fmt.Errorf("%w: the proof is longer than %d bytes", authn.ErrHumanProofTooLong, maxTokenLen)
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	form := url.Values{}
	form.Set("secret", p.secret)
	form.Set("response", proof.Token)
	if proof.RemoteIP != "" {
		form.Set("remoteip", proof.RemoteIP)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.verifyURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: turnstile: building the siteverify request: %w", authn.ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: turnstile: the siteverify call failed: %w", authn.ErrUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: turnstile: siteverify answered status %d",
			authn.ErrUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: turnstile: reading the siteverify answer: %w", authn.ErrUnavailable, err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("%w: turnstile: the siteverify answer is larger than %d bytes",
			authn.ErrUnavailable, maxResponseBytes)
	}

	var answer siteverifyAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("%w: turnstile: siteverify answered a body that is not its response",
			authn.ErrUnavailable)
	}
	// The codes are the provider's own diagnosis of a refusal. They are logged
	// for an operator and never reach the client: the surface answers one code
	// that names human verification, whatever the provider's reason was.
	if len(answer.ErrorCodes) > 0 {
		log.FromContext(ctx).Warn("turnstile: siteverify answered with error codes",
			slog.String("error_codes", strings.Join(answer.ErrorCodes, ",")))
	}
	if !answer.Success {
		if refusedForProof(answer.ErrorCodes) {
			return fmt.Errorf("%w: turnstile: the proof was refused", authn.ErrHumanVerificationRequired)
		}
		// A refusal that names no proof problem is the provider failing to
		// judge the proof, or this deployment's configuration being wrong.
		// Both are an outage: sending on a verdict nobody cast is not an
		// option, and the client must not be told its proof was invalid.
		return fmt.Errorf("%w: turnstile: siteverify could not judge the proof", authn.ErrUnavailable)
	}
	if !strings.EqualFold(strings.TrimSpace(answer.Hostname), p.hostname) {
		return fmt.Errorf("%w: turnstile: siteverify accepted a proof for hostname %q",
			authn.ErrUnavailable, answer.Hostname)
	}
	if answer.Action != p.action {
		return fmt.Errorf("%w: turnstile: siteverify accepted a proof for action %q",
			authn.ErrUnavailable, answer.Action)
	}
	return p.claim(ctx, proof.Token)
}

// claim records that the token was accepted, so a second presentation of the
// same token is refused here whatever siteverify would answer. The claim is a
// set-if-absent on the token's digest: the store arbitrates between concurrent
// presentations, so exactly one of them records the claim and is accepted. A
// claim that could not be recorded is an outage: accepting the proof without a
// record would leave it replayable.
func (p *Provider) claim(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	claimed, err := p.store.SetIfAbsent(ctx, spentKey(sum[:]), []byte("spent"), ClaimTTL)
	if err != nil {
		return fmt.Errorf("%w: turnstile: recording the spent token: %w", authn.ErrUnavailable, err)
	}
	if !claimed {
		return fmt.Errorf("%w: turnstile: the proof was already accepted", authn.ErrHumanVerificationRequired)
	}
	return nil
}

// spentKey is where a claimed token's digest is kept. The token is hashed so
// the key carries no credential.
func spentKey(digest []byte) string { return spentKeyPrefix + hex.EncodeToString(digest) }

// proofRefusalCodes are the siteverify codes that name the proof itself as the
// reason for a refusal: it was absent, it did not verify, or it had expired or
// already been spent. Every other code names a fault on the provider's side or
// in this deployment's configuration, and is read as an outage.
var proofRefusalCodes = map[string]bool{
	"missing-input-response": true,
	"invalid-input-response": true,
	"timeout-or-duplicate":   true,
}

// refusedForProof reports whether every code the provider gave names a problem
// with the proof. An answer with no codes at all, or one carrying any code this
// package does not know, is not judged as a proof refusal: the provider did not
// tell us the proof was wrong, so the only safe reading is that it could not
// judge it. A mixed answer is read the same way, because one provider-side code
// is enough to say the verdict is not about the proof.
func refusedForProof(codes []string) bool {
	if len(codes) == 0 {
		return false
	}
	for _, code := range codes {
		if !proofRefusalCodes[code] {
			return false
		}
	}
	return true
}

// siteverifyAnswer is the part of the provider's response this package judges.
// The codes are read for the log alone; nothing else in the answer is used.
type siteverifyAnswer struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}
