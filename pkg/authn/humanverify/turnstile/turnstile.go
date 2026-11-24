// Package turnstile is the Cloudflare Turnstile provider: it posts the client's token to siteverify and judges success, hostname and action. An accepted proof is claimed in the key-value store by token digest so a replay is refused locally.
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

const maxTokenLen = 4096

const maxResponseBytes = egress.MaxBodyBytes

// ClaimTTL is how long a spent token's digest is remembered, which covers the token's whole validity.
const ClaimTTL = 5 * time.Minute

const spentKeyPrefix = "authn:human:turnstile:spent:"

// Options configures a [Provider].
type Options struct {
	// SiteKey is the public widget key a client renders the widget with; it is not a credential.
	SiteKey string
	// Secret is the secret key. It authenticates the siteverify call and is a
	// credential.
	Secret string
	// VerifyURL is the siteverify endpoint. Empty takes [DefaultVerifyURL].
	VerifyURL string
	// Hostname is the hostname the widget must have run on.
	Hostname string
	// Action is the action the widget is rendered with, which the answer must name.
	Action string
	// Timeout bounds one verification as a whole. Zero takes [DefaultTimeout].
	Timeout time.Duration
	// HTTP is the outbound client; nil takes the restricted egress client.
	HTTP *http.Client
	// Store keeps a claimed token's digest so an accepted proof cannot be replayed; it is required.
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

// New builds the provider; the site key, secret, hostname and action are required.
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

// Action reports the action a client-side widget must be rendered with.
func (p *Provider) Action(authn.HumanScope) string { return p.action }

// Verify posts the proof to siteverify and judges the answer. An accepted token's digest is claimed in the store; a proof-problem code is an invalid proof, and every other failure, including an unrecordable claim, is an outage.
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
	// The provider's codes are logged for an operator and never reach the client, which answers one code naming human verification.
	if len(answer.ErrorCodes) > 0 {
		log.FromContext(ctx).Warn("turnstile: siteverify answered with error codes",
			slog.String("error_codes", strings.Join(answer.ErrorCodes, ",")))
	}
	if !answer.Success {
		if refusedForProof(answer.ErrorCodes) {
			return fmt.Errorf("%w: turnstile: the proof was refused", authn.ErrHumanVerificationRequired)
		}
		// A refusal that names no proof problem is the provider failing to judge the proof or a deployment misconfiguration, which is an outage.
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

func spentKey(digest []byte) string { return spentKeyPrefix + hex.EncodeToString(digest) }

var proofRefusalCodes = map[string]bool{
	"missing-input-response": true,
	"invalid-input-response": true,
	"timeout-or-duplicate":   true,
}

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

type siteverifyAnswer struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}
