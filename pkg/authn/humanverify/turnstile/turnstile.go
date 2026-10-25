// Package turnstile is the Cloudflare Turnstile provider of the
// human-verification gate: it posts the token the client obtained from the
// widget to the provider's siteverify endpoint and reads the answer.
//
// The answer is judged on three things: success, the hostname the widget ran
// on, and, when the deployment names one, the action the widget was rendered
// with. A proof the provider accepts is accepted once; Cloudflare refuses a
// token it has already spent with its duplicate error code, which this package
// reads as an invalid proof, not as an outage.
//
// The secret never reaches a log line, an error text or a metric: the token is
// a credential and so is the secret, and the siteverify call carries both in
// its body alone.
package turnstile

import (
	"context"
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
	// Action, when non-empty, is the action the widget was rendered with; the
	// provider's answer must name it. Empty accepts any action.
	Action string
	// Timeout bounds one verification as a whole. Zero takes [DefaultTimeout].
	Timeout time.Duration
	// HTTP is the outbound client. Nil takes the restricted egress client,
	// which speaks https to a public address only. A client given here is used
	// as it stands, so a test may point it at a loopback server its own roots
	// trust.
	HTTP *http.Client
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
}

var _ authn.HumanVerifier = (*Provider)(nil)

// New builds the provider. The site key, the secret and the expected hostname
// are required: a deployment that turns the gate on without them cannot check
// a proof, and starting anyway would send messages on a check nobody supplied.
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
		action:    o.Action,
		timeout:   timeout,
		client:    client,
	}, nil
}

// Required reports true: this gate is a condition of sending.
func (p *Provider) Required() bool { return true }

// Provider reports the mechanism name.
func (p *Provider) Provider() string { return ProviderName }

// SiteKey reports the public widget key a client-side widget is rendered with.
func (p *Provider) SiteKey() string { return p.siteKey }

// Verify posts the proof to siteverify and judges the answer.
//
// A token is single-use: the provider refuses a token it has already spent with
// its duplicate code, which reads here as an invalid proof. An answer that is
// not a 2xx, not the provider's response, or accepted for another hostname or
// action is an outage: the proof was not judged, so nothing is sent.
func (p *Provider) Verify(ctx context.Context, proof authn.HumanProof) error {
	if proof.Token == "" {
		return fmt.Errorf("%w: turnstile: no proof was presented", authn.ErrHumanVerificationRequired)
	}
	if len(proof.Token) > maxTokenLen {
		return fmt.Errorf("%w: the proof is longer than %d bytes", authn.ErrInvalidArgument, maxTokenLen)
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
		return fmt.Errorf("%w: turnstile: the proof was refused", authn.ErrHumanVerificationRequired)
	}
	if !strings.EqualFold(strings.TrimSpace(answer.Hostname), p.hostname) {
		return fmt.Errorf("%w: turnstile: siteverify accepted a proof for hostname %q",
			authn.ErrUnavailable, answer.Hostname)
	}
	if p.action != "" && answer.Action != p.action {
		return fmt.Errorf("%w: turnstile: siteverify accepted a proof for action %q",
			authn.ErrUnavailable, answer.Action)
	}
	return nil
}

// siteverifyAnswer is the part of the provider's response this package judges.
// The codes are read for the log alone; nothing else in the answer is used.
type siteverifyAnswer struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}
