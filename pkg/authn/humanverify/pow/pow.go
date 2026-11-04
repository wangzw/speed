// Package pow is the self-hosted proof-of-work provider of the
// human-verification gate: it issues a random challenge, and a client proves
// its work by finding a nonce whose hash carries enough leading zero bits.
//
// The proof is the challenge and the nonce joined by a dot. Verify recomputes
// the hash from the challenge and the nonce, and spends the challenge in the
// key-value store in the same step, so a proof accepted once is refused when it
// is presented again.
//
// Issuance is bounded twice over a one-minute fixed window: a per-source counter
// stops one address from filling the store, and a global counter stops a set of
// addresses from multiplying the total work. Both bounds answer
// [ErrIssuanceLimited].
package pow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
)

// ProviderName is the mechanism name this implementation reports to the seam.
const ProviderName = "pow"

// ProofHash names the hash a proof is computed with, as the challenge answer
// publishes it. The name avoids a bare Algorithm, which the capabilities'
// metric scan resolves as a selector name.
const ProofHash = "sha256"

// Namespace is the settings namespace this provider reads its parameters from.
const Namespace = "authn.humanverify.pow"

// The setting items and the value each holds without a settings store.
const (
	// DifficultyKey is the number of leading zero bits a proof must reach.
	DifficultyKey = "difficulty"
	// ChallengeTTLKey is how long an issued challenge stays valid.
	ChallengeTTLKey = "challenge-ttl"
	// GlobalIssueLimitKey is how many challenges all sources together may be
	// issued in one issuance window.
	GlobalIssueLimitKey = "global-issue-limit"

	DefaultDifficulty   = 20
	DifficultyMin       = 18
	DifficultyMax       = 22
	DefaultChallengeTTL = 5 * time.Minute
	ChallengeTTLMin     = time.Minute
	ChallengeTTLMax     = 15 * time.Minute
	// DefaultGlobalIssueLimit is the whole-deployment bound: sixty distinct
	// sources each at the per-source bound of ten in one window. It is a small
	// deployment's ordinary traffic and far below the work a set of source
	// addresses could otherwise amplify it to.
	DefaultGlobalIssueLimit = 600
	GlobalIssueLimitMin     = 1
	GlobalIssueLimitMax     = 1000000
)

// Storage constants.
const (
	challengeKeyPrefix = "authn:human:pow:"
	issueKeyPrefix     = "authn:human:pow:issue:"
	challengeBytes     = 16
	// maxProofLen is the longest proof the provider admits. A valid proof is
	// 22 base64url characters, a dot and at most 20 decimal digits, so this
	// bound is far above one and catches a body sent in its place.
	maxProofLen = 512
	// issueLimit is how many challenges one source may hold in issueWindow.
	// It is small on purpose: an anonymous caller is the reason the counter
	// exists, and a challenge that lapses is served by a new request once the
	// window expires.
	issueLimit = 10
)

// issueWindow is the fixed window the per-source issuance counter covers: the
// counter starts with the first challenge of a window and expires a minute
// later, so sustained traffic does not slide it forward.
const issueWindow = time.Minute

// spentMarker is what a challenge key holds after the proof that named it was
// accepted. It is not a difficulty, so a second presentation is refused before
// any work is checked.
var spentMarker = []byte("spent")

// ErrIssuanceLimited reports that the per-source bound or the whole-deployment
// bound of the current window has been reached. It is not an outage: retrying
// later works, and the answer is a 429.
var ErrIssuanceLimited = errors.New("pow: too many challenges were issued in this window")

// ErrUnknownScope reports a surface that is not one of the two the gate
// serves.
var ErrUnknownScope = errors.New("pow: unknown human scope")

// IssueOptions is what a caller asks a challenge with.
type IssueOptions struct {
	// Scope is the surface the challenge is issued for. A proof is bound to
	// it: presenting it on the other surface is refused.
	Scope authn.HumanScope
	// RemoteIP is the source the issuance counter is spent for. It is hashed
	// before it reaches the store, because the key is a value the store and
	// its diagnostics may carry.
	RemoteIP string
}

// record is what a challenge key holds: the surface the challenge was issued
// for and the difficulty its proof must reach. The spent marker is not a
// record, so a second presentation is refused before any work is checked.
type record struct {
	Scope      authn.HumanScope `json:"scope"`
	Difficulty int              `json:"difficulty"`
}

// validScope reports whether scope is one of the two surfaces a challenge may
// be issued for.
func validScope(scope authn.HumanScope) bool {
	switch scope {
	case authn.HumanScopePhone, authn.HumanScopeOther:
		return true
	default:
		return false
	}
}

// Challenge is what Issue answers: the random challenge a client solves, the
// difficulty its proof must reach, and how long it stays valid.
type Challenge struct {
	// Challenge is the random value, base64url without padding.
	Challenge string
	// Difficulty is the number of leading zero bits the hash must carry.
	Difficulty int
	// ExpiresIn is the lifetime the challenge was stored with.
	ExpiresIn time.Duration
}

// SettingReader is the part of the settings store this provider reads. It keeps
// the provider from depending on the whole store surface.
type SettingReader interface {
	Get(layer settings.Layer, key string, target any) error
}

// Provider is the self-hosted proof-of-work verifier.
type Provider struct {
	store    kv.KV
	settings SettingReader
}

// New builds the provider over a key-value store and the settings reader.
func New(store kv.KV, reader SettingReader) (*Provider, error) {
	if store == nil {
		return nil, errors.New("pow: a key-value store is required")
	}
	return &Provider{store: store, settings: reader}, nil
}

// Required reports true for every scope: a proof-of-work gate is a condition of
// sending wherever it is configured.
func (p *Provider) Required(authn.HumanScope) bool { return true }

// Provider reports the mechanism name for every scope.
func (p *Provider) Provider(authn.HumanScope) string { return ProviderName }

// SiteKey is empty for every scope: a self-hosted proof of work needs no widget
// key.
func (p *Provider) SiteKey(authn.HumanScope) string { return "" }

// Action is empty for every scope: a self-hosted proof of work renders no
// widget.
func (p *Provider) Action(authn.HumanScope) string { return "" }

// Issue returns a fresh challenge for one surface. The source's issuance
// counter is spent first, so an address that has asked for as many challenges
// as the window allows is refused before any random value is drawn or stored,
// and an anonymous caller cannot fill the store. The scope is stored with the
// challenge, so a proof issued for one surface cannot be presented on the
// other.
func (p *Provider) Issue(ctx context.Context, opts IssueOptions) (Challenge, error) {
	if !validScope(opts.Scope) {
		return Challenge{}, fmt.Errorf("%w: %q", ErrUnknownScope, opts.Scope)
	}
	if err := p.countIssue(ctx, opts.RemoteIP); err != nil {
		return Challenge{}, err
	}
	raw := make([]byte, challengeBytes)
	if _, err := rand.Read(raw); err != nil {
		return Challenge{}, fmt.Errorf("%w: drawing a challenge: %w", authn.ErrUnavailable, err)
	}
	challenge := base64.RawURLEncoding.EncodeToString(raw)
	difficulty := p.difficulty()
	ttl := p.challengeTTL()
	stored, err := json.Marshal(record{Scope: opts.Scope, Difficulty: difficulty})
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: encoding the challenge: %w", authn.ErrUnavailable, err)
	}
	if err := p.store.Set(ctx, challengeKey(challenge), stored, ttl); err != nil {
		return Challenge{}, fmt.Errorf("%w: storing the challenge: %w", authn.ErrUnavailable, err)
	}
	return Challenge{Challenge: challenge, Difficulty: difficulty, ExpiresIn: ttl}, nil
}

// Verify checks a proof and spends the challenge it names. The scope has to be
// the one the challenge was issued for: a proof fetched for one surface is
// refused on the other, and that refusal consumes nothing, so the client can
// still present it on the surface it was issued for. The challenge's stored
// difficulty is the threshold, so the parameters a deployment was running when
// the challenge was issued decide what the proof has to reach. The challenge is
// spent with a compare-and-swap that requires the value that was just read,
// which is what makes two concurrent presentations of one proof unable to both
// pass.
func (p *Provider) Verify(ctx context.Context, proof authn.HumanProof, scope authn.HumanScope) error {
	if proof.Token == "" {
		return fmt.Errorf("%w: no proof was presented", authn.ErrHumanVerificationRequired)
	}
	if len(proof.Token) > maxProofLen {
		return fmt.Errorf("%w: the proof is longer than %d bytes", authn.ErrHumanProofTooLong, maxProofLen)
	}
	challenge, nonce, ok := splitProof(proof.Token)
	if !ok {
		return fmt.Errorf("%w: the proof is not a challenge and a nonce", authn.ErrHumanVerificationRequired)
	}
	key := challengeKey(challenge)
	stored, present, err := p.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: reading the challenge: %w", authn.ErrUnavailable, err)
	}
	if !present {
		return fmt.Errorf("%w: the challenge is missing or expired", authn.ErrHumanVerificationRequired)
	}
	var rec record
	if decodeErr := json.Unmarshal(stored, &rec); decodeErr != nil || !validScope(rec.Scope) || !difficultyInDomain(rec.Difficulty) {
		// A spent marker lands here too: it is not a record, so the challenge
		// reads as one that cannot be checked again.
		return fmt.Errorf("%w: the challenge does not carry a usable record", authn.ErrHumanVerificationRequired)
	}
	if rec.Scope != scope {
		// The challenge is left unspent: it is still usable on the surface it
		// was issued for.
		return fmt.Errorf("%w: the challenge was issued for another surface", authn.ErrHumanVerificationRequired)
	}
	if !meetsDifficulty(challenge, nonce, rec.Difficulty) {
		return fmt.Errorf("%w: the proof does not reach the stored difficulty", authn.ErrHumanVerificationRequired)
	}
	spent, err := p.store.CompareAndSwap(ctx, key, stored, spentMarker)
	if err != nil {
		return fmt.Errorf("%w: spending the challenge: %w", authn.ErrUnavailable, err)
	}
	if !spent {
		return fmt.Errorf("%w: the challenge was already spent", authn.ErrHumanVerificationRequired)
	}
	return nil
}

// countIssue spends one slot of the source's issuance window and one slot of
// the whole deployment's. The source's bound is checked first, so a source that
// has exhausted its own allowance does not spend the shared one. Both counters
// cover the same fixed window; the global one is what stops a set of source
// addresses from multiplying the total challenge work.
func (p *Provider) countIssue(ctx context.Context, remoteIP string) error {
	issued, err := p.store.IncrByFloatWithTTL(ctx, issueKey(remoteIP), 1, issueWindow)
	if err != nil {
		return fmt.Errorf("%w: counting the challenges issued to a source: %w", authn.ErrUnavailable, err)
	}
	if issued > issueLimit {
		return ErrIssuanceLimited
	}
	total, err := p.store.IncrByFloatWithTTL(ctx, globalIssueKey(), 1, issueWindow)
	if err != nil {
		return fmt.Errorf("%w: counting the challenges issued in the window: %w", authn.ErrUnavailable, err)
	}
	if total > float64(p.globalIssueLimit()) {
		return ErrIssuanceLimited
	}
	return nil
}

// splitProof reads the proof's challenge.nonce form. The last dot is the
// separator, the challenge has to be base64url, and the nonce has to be a
// decimal unsigned integer.
func splitProof(token string) (challenge string, nonce uint64, ok bool) {
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", 0, false
	}
	challenge = token[:dot]
	if _, err := base64.RawURLEncoding.DecodeString(challenge); err != nil {
		return "", 0, false
	}
	nonce, err := strconv.ParseUint(token[dot+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return challenge, nonce, true
}

// meetsDifficulty reports whether the nonce's work reaches difficulty.
func meetsDifficulty(challenge string, nonce uint64, difficulty int) bool {
	raw, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil {
		return false
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], nonce)
	h := sha256.New()
	h.Write(raw)
	h.Write(encoded[:])
	return leadingZeroBits(h.Sum(nil)) >= difficulty
}

// leadingZeroBits counts the zero bits a hash begins with.
func leadingZeroBits(sum []byte) int {
	count := 0
	for _, b := range sum {
		if b != 0 {
			return count + bits.LeadingZeros8(b)
		}
		count += 8
	}
	return count
}

// challengeKey is where one challenge is stored: the digest of its text, so the
// key is a fixed width and the challenge's value is not itself the key.
func challengeKey(challenge string) string {
	sum := sha256.Sum256([]byte(challenge))
	return challengeKeyPrefix + hexEncode(sum[:])
}

// issueKey is one source's issuance counter. The source is hashed for the same
// reason: the key is a value the store and its diagnostics may carry.
func issueKey(remoteIP string) string {
	sum := sha256.Sum256([]byte(remoteIP))
	return issueKeyPrefix + hexEncode(sum[:])
}

// globalIssueKey is the counter shared by every source. It is a fixed name, not
// a digest, and cannot collide with a source key, whose suffix is hexadecimal.
func globalIssueKey() string { return issueKeyPrefix + "global" }

// hexEncode is the lowercase hexadecimal form of raw.
func hexEncode(raw []byte) string {
	const digits = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(raw) * 2)
	for _, c := range raw {
		b.WriteByte(digits[c>>4])
		b.WriteByte(digits[c&0x0f])
	}
	return b.String()
}
