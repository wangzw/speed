// Package captcha is the self-hosted captcha provider of the human-verification gate: it draws image, slider and audio challenges kept in the key-value store with digests and one-time spend, bounded per source and deployment-wide.
package captcha

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	libcaptcha "github.com/mojocn/base64Captcha"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
)

// ProviderName is the mechanism name this implementation reports to the seam.
const ProviderName = "captcha"

// Namespace is the settings namespace this provider reads its issuance limit
// from.
const Namespace = "authn.humanverify.captcha"

// The issuance-limit setting and the value it holds without a settings store.
const (
	// GlobalIssueLimitKey is how many challenges all sources together may be
	// issued in one issuance window.
	GlobalIssueLimitKey = "global-issue-limit"

	// DefaultGlobalIssueLimit is the whole-deployment bound.
	DefaultGlobalIssueLimit = 600
	GlobalIssueLimitMin     = 1
	GlobalIssueLimitMax     = 1000000
)

// Form is one challenge form this provider draws.
type Form string

const (
	// FormImage is a picture of digits.
	FormImage Form = "image"
	// FormSlider is a background with a piece cut out of it.
	FormSlider Form = "slider"
	// FormAudio is a spoken reading of digits, the screen-reader alternative.
	FormAudio Form = "audio"
)

// Valid reports whether f is one of the forms this provider draws.
func (f Form) Valid() bool {
	switch f {
	case FormImage, FormSlider, FormAudio:
		return true
	default:
		return false
	}
}

// MediaKind names one media object a challenge is served with.
type MediaKind string

const (
	// MediaImage is the picture of a FormImage challenge.
	MediaImage MediaKind = "image"
	// MediaBackground is the picture with the piece cut out of it.
	MediaBackground MediaKind = "background"
	// MediaPiece is the piece a client slides over the background.
	MediaPiece MediaKind = "piece"
	// MediaAudio is the audio of a FormAudio challenge.
	MediaAudio MediaKind = "audio"
)

const (
	keyPrefix           = "authn:human:captcha:"
	issueKeyPrefix      = "authn:human:captcha:issue:"
	audioIssueKeyPrefix = "authn:human:captcha:audio-issue:"
	challengeTTL        = 5 * time.Minute
	idBytes             = 16
	saltBytes           = 16
	maxProofLen         = 512
	maxAnswerBytes      = 64
	issueLimit          = 10
	audioIssueLimit     = 1
)

// IssueWindow is the fixed window the per-source issuance counter covers; it is also the longest wait a route states when it refuses a source.
const IssueWindow = time.Minute

var spentMarker = []byte("spent")

const (
	digitHeightMin = 56
	digitHeightMax = 72
	digitWidthMin  = 220
	digitWidthMax  = 280
	digitSkewMin   = 0.9
	digitSkewMax   = 1.4
	digitDotMin    = 110
	digitDotMax    = 170
	digitLength    = 5
	audioLength    = 5
)

// DefaultAudioLanguage is the language an audio challenge is spoken in when the
// request names none the provider pronounces.
const DefaultAudioLanguage = "en"

// Errors a caller maps onto its own answers: ErrUnknownForm and ErrUnknownAudioLanguage are bad requests, ErrChallengeNotFound is an unknown or spent challenge, and ErrIssuanceLimited is the retryable 429.
var (
	// ErrUnknownForm reports a form this provider does not draw.
	ErrUnknownForm = errors.New("captcha: unknown challenge form")
	// ErrUnknownAudioLanguage reports a language the library pronounces no
	// digits in.
	ErrUnknownAudioLanguage = errors.New("captcha: the library pronounces no digits in this language")
	// ErrChallengeNotFound reports that no challenge is stored under the id.
	ErrChallengeNotFound = errors.New("captcha: the challenge is missing or expired")
	// ErrIssuanceLimited reports that the per-source bound or the
	// whole-deployment bound of the current window has been reached. It is not
	// an outage: retrying later works, and the answer is a 429.
	ErrIssuanceLimited = errors.New("captcha: too many challenges were issued in this window")
	// ErrUnknownScope reports a surface that is not one of the two the gate
	// serves.
	ErrUnknownScope = errors.New("captcha: unknown human scope")
)

// Challenge is what Issue answers: the id a proof names, the form it takes and
// how long it stays valid.
type Challenge struct {
	// ID is the challenge identifier a proof names.
	ID string
	// Form is the form the challenge takes.
	Form Form
	// ExpiresIn is how long the challenge stays valid.
	ExpiresIn time.Duration
}

// Media is one media object a challenge route serves.
type Media struct {
	// ContentType is the media type the route answers with.
	ContentType string
	// Body is the media itself.
	Body []byte
}

type formRotation func() (Form, error)

// SettingReader is the part of the settings store this provider reads. It keeps
// the provider from depending on the whole store surface.
type SettingReader interface {
	// Get reads a setting of a layer into target.
	Get(layer settings.Layer, key string, target any) error
}

// Provider is the self-hosted captcha verifier.
type Provider struct {
	store    kv.KV
	settings SettingReader
	// rotate draws the form of a challenge whose form the client did not choose.
	rotate formRotation
}

var _ authn.HumanVerifier = (*Provider)(nil)

// New builds the provider over a key-value store and the settings reader; a nil reader uses the declared issuance limit.
func New(store kv.KV, reader SettingReader) (*Provider, error) {
	return newProviderWithRotation(store, reader, randomForm)
}

func newProviderWithRotation(store kv.KV, reader SettingReader, rotate formRotation) (*Provider, error) {
	if store == nil {
		return nil, errors.New("captcha: a key-value store is required")
	}
	if rotate == nil {
		return nil, errors.New("captcha: a form rotation is required")
	}
	return &Provider{store: store, settings: reader, rotate: rotate}, nil
}

// Required reports true: a captcha gate is a condition of sending.
func (p *Provider) Required(authn.HumanScope) bool { return true }

// Provider reports the mechanism name.
func (p *Provider) Provider(authn.HumanScope) string { return ProviderName }

// SiteKey is empty: a self-hosted captcha needs no widget key.
func (p *Provider) SiteKey(authn.HumanScope) string { return "" }

// Action is empty: a self-hosted captcha renders no action-bearing widget.
func (p *Provider) Action(authn.HumanScope) string { return "" }

// IssueOptions is what a caller asks a challenge with.
type IssueOptions struct {
	// Form is the form the caller asks for. FormAudio is honoured; every other value, including none, is the server's rotation choice, and an unknown form is ErrUnknownForm.
	Form Form
	// Lang is the BCP 47 language of an audio challenge; empty is DefaultAudioLanguage, and a primary subtag the library does not pronounce is ErrUnknownAudioLanguage.
	Lang string
	// Scope is the surface the challenge is issued for. A proof is bound to
	// it: presenting it on the other surface is refused.
	Scope authn.HumanScope
	// RemoteIP is the source the issuance counter is spent for; it is hashed before it reaches the store.
	RemoteIP string
}

// Issue draws a challenge of the resolved form, spends the source's issuance counter first, and stores the answer and media. The scope is stored with the challenge.
func (p *Provider) Issue(ctx context.Context, opts IssueOptions) (Challenge, error) {
	if !validScope(opts.Scope) {
		return Challenge{}, fmt.Errorf("%w: %q", ErrUnknownScope, opts.Scope)
	}
	form, err := p.resolveForm(opts.Form)
	if err != nil {
		return Challenge{}, err
	}
	language := DefaultAudioLanguage
	if form == FormAudio {
		resolved, langErr := audioLanguage(opts.Lang)
		if langErr != nil {
			return Challenge{}, langErr
		}
		language = resolved
	}
	if countErr := p.countIssue(ctx, form, opts.RemoteIP); countErr != nil {
		return Challenge{}, countErr
	}
	id, idErr := newID()
	if idErr != nil {
		return Challenge{}, fmt.Errorf("%w: captcha: drawing a challenge id: %w", authn.ErrUnavailable, idErr)
	}
	salt, err := newSalt()
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: captcha: drawing a challenge salt: %w", authn.ErrUnavailable, err)
	}

	rec, media, err := drawForm(form, salt, language)
	if err != nil {
		return Challenge{}, err
	}
	rec.Scope = opts.Scope
	encoded, err := json.Marshal(rec)
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: captcha: encoding the challenge: %w", authn.ErrUnavailable, err)
	}

	// The media is written before the record, so a challenge is usable only once everything it serves is in place; a failed write removes what this call stored.
	written := make([]string, 0, len(media))
	for kind, object := range media {
		key := mediaKey(id, kind)
		if err := p.store.Set(ctx, key, object.Body, challengeTTL); err != nil {
			p.discard(ctx, written...)
			return Challenge{}, fmt.Errorf("%w: captcha: storing the challenge media: %w", authn.ErrUnavailable, err)
		}
		written = append(written, key)
	}
	key := challengeKey(id)
	if err := p.store.Set(ctx, key, encoded, challengeTTL); err != nil {
		p.discard(ctx, append(written, key)...)
		return Challenge{}, fmt.Errorf("%w: captcha: storing the challenge: %w", authn.ErrUnavailable, err)
	}
	return Challenge{ID: id, Form: form, ExpiresIn: challengeTTL}, nil
}

// Media reads one media object of a challenge; an unknown, expired or spent id is ErrChallengeNotFound.
func (p *Provider) Media(ctx context.Context, id string, kind MediaKind) (Media, error) {
	if !validID(id) || !validMedia(kind) {
		return Media{}, ErrChallengeNotFound
	}
	body, present, err := p.store.Get(ctx, mediaKey(id, kind))
	if err != nil {
		return Media{}, fmt.Errorf("%w: captcha: reading the challenge media: %w", authn.ErrUnavailable, err)
	}
	if !present {
		return Media{}, ErrChallengeNotFound
	}
	return Media{ContentType: contentType(kind), Body: body}, nil
}

// Verify checks a proof, requires the challenge's scope, compares the answer in constant time, and spends the challenge with a compare-and-swap. The record and media are deleted whether the answer matched or not.
func (p *Provider) Verify(ctx context.Context, proof authn.HumanProof, scope authn.HumanScope) error {
	if proof.Token == "" {
		return fmt.Errorf("%w: captcha: no proof was presented", authn.ErrHumanVerificationRequired)
	}
	if len(proof.Token) > maxProofLen {
		return fmt.Errorf("%w: the proof is longer than %d bytes", authn.ErrHumanProofTooLong, maxProofLen)
	}
	id, answer, ok := splitProof(proof.Token)
	if !ok {
		return fmt.Errorf("%w: captcha: the proof is not an id and an answer", authn.ErrHumanVerificationRequired)
	}

	key := challengeKey(id)
	stored, present, err := p.store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: captcha: reading the challenge: %w", authn.ErrUnavailable, err)
	}
	if !present {
		return fmt.Errorf("%w: captcha: the challenge is missing or expired", authn.ErrHumanVerificationRequired)
	}

	var rec record
	if decodeErr := json.Unmarshal(stored, &rec); decodeErr != nil || !rec.Form.Valid() || !validScope(rec.Scope) || rec.Salt == "" {
		// A record this package cannot read is spent on the spot; its digests cannot be checked.
		_, _ = p.store.CompareAndSwap(ctx, key, stored, spentMarker)
		p.discard(ctx, key)
		return fmt.Errorf("%w: captcha: the challenge is not usable", authn.ErrHumanVerificationRequired)
	}
	if rec.Scope != scope {
		// The challenge is left unspent and still usable on the surface it was issued for.
		return fmt.Errorf("%w: captcha: the challenge was issued for another surface", authn.ErrHumanVerificationRequired)
	}

	matched := false
	if value, ok := normalizeAnswer(rec.Form, answer); ok {
		matched = matches(rec.Digests, digest(rec.Salt, value))
	}

	spent, err := p.store.CompareAndSwap(ctx, key, stored, spentMarker)
	if err != nil {
		return fmt.Errorf("%w: captcha: spending the challenge: %w", authn.ErrUnavailable, err)
	}
	if !spent {
		return fmt.Errorf("%w: captcha: the challenge was already spent", authn.ErrHumanVerificationRequired)
	}
	p.purge(ctx, id)
	if !matched {
		return fmt.Errorf("%w: captcha: the answer is wrong", authn.ErrHumanVerificationRequired)
	}
	return nil
}

type record struct {
	Scope   authn.HumanScope `json:"scope"`
	Form    Form             `json:"form"`
	Salt    string           `json:"salt"`
	Digests []string         `json:"digests"`
}

func validScope(scope authn.HumanScope) bool {
	switch scope {
	case authn.HumanScopePhone, authn.HumanScopeOther:
		return true
	default:
		return false
	}
}

func (p *Provider) resolveForm(requested Form) (Form, error) {
	switch requested {
	case FormAudio:
		return FormAudio, nil
	case "", FormImage, FormSlider:
		form, err := p.rotate()
		if err != nil {
			return "", fmt.Errorf("%w: captcha: choosing the challenge form: %w", authn.ErrUnavailable, err)
		}
		switch form {
		case FormImage, FormSlider:
			return form, nil
		default:
			return "", fmt.Errorf("%w: captcha: the rotation chose %q", authn.ErrUnavailable, form)
		}
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownForm, requested)
	}
}

func randomForm() (Form, error) {
	var raw [1]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	if raw[0]&1 == 0 {
		return FormImage, nil
	}
	return FormSlider, nil
}

func drawForm(form Form, salt, language string) (record, map[MediaKind]Media, error) {
	switch form {
	case FormImage:
		return drawDigits(salt)
	case FormAudio:
		return drawAudio(salt, language)
	case FormSlider:
		return drawSlider(salt)
	default:
		return record{}, nil, fmt.Errorf("%w: %q", ErrUnknownForm, form)
	}
}

func drawDigits(salt string) (record, map[MediaKind]Media, error) {
	height, err := randomInt(digitHeightMin, digitHeightMax)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: choosing the image height: %w", authn.ErrUnavailable, err)
	}
	width, err := randomInt(digitWidthMin, digitWidthMax)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: choosing the image width: %w", authn.ErrUnavailable, err)
	}
	skew, err := randomFloat(digitSkewMin, digitSkewMax)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: choosing the digit skew: %w", authn.ErrUnavailable, err)
	}
	dots, err := randomInt(digitDotMin, digitDotMax)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: choosing the dot count: %w", authn.ErrUnavailable, err)
	}
	driver := libcaptcha.NewDriverDigit(height, width, digitLength, skew, dots)
	_, question, answer := driver.GenerateIdQuestionAnswer()
	item, err := driver.DrawCaptcha(question)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: drawing the image: %w", authn.ErrUnavailable, err)
	}
	body, err := itemBytes(item)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: encoding the image: %w", authn.ErrUnavailable, err)
	}
	return record{Form: FormImage, Salt: salt, Digests: []string{digest(salt, answer)}},
		map[MediaKind]Media{MediaImage: {ContentType: "image/png", Body: body}}, nil
}

func drawAudio(salt, language string) (record, map[MediaKind]Media, error) {
	driver := libcaptcha.NewDriverAudio(audioLength, language)
	_, question, answer := driver.GenerateIdQuestionAnswer()
	item, err := driver.DrawCaptcha(question)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: drawing the audio: %w", authn.ErrUnavailable, err)
	}
	body, err := itemBytes(item)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: encoding the audio: %w", authn.ErrUnavailable, err)
	}
	body, err = hardenAudio(body, language)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: hardening the audio: %w", authn.ErrUnavailable, err)
	}
	return record{Form: FormAudio, Salt: salt, Digests: []string{digest(salt, answer)}},
		map[MediaKind]Media{MediaAudio: {ContentType: "audio/wav", Body: body}}, nil
}

func drawSlider(salt string) (record, map[MediaKind]Media, error) {
	images, err := newSlider(salt)
	if err != nil {
		return record{}, nil, fmt.Errorf("%w: captcha: drawing the slider: %w", authn.ErrUnavailable, err)
	}
	return record{Form: FormSlider, Salt: salt, Digests: images.digests},
		map[MediaKind]Media{
			MediaBackground: {ContentType: "image/png", Body: images.background},
			MediaPiece:      {ContentType: "image/png", Body: images.piece},
		}, nil
}

func itemBytes(item libcaptcha.Item) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := item.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func splitProof(token string) (id, answer string, ok bool) {
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", "", false
	}
	id = token[:dot]
	answer = token[dot+1:]
	if !validID(id) || len(answer) > maxAnswerBytes {
		return "", "", false
	}
	return id, answer, true
}

func normalizeAnswer(form Form, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	switch form {
	case FormSlider:
		offset, err := strconv.Atoi(answer)
		if err != nil || offset < 0 {
			return "", false
		}
		return strconv.Itoa(offset), true
	case FormImage, FormAudio:
		if answer == "" {
			return "", false
		}
		return answer, true
	default:
		return "", false
	}
}

func matches(digests []string, value string) bool {
	var found int
	for _, candidate := range digests {
		found |= subtle.ConstantTimeCompare([]byte(candidate), []byte(value))
	}
	return found == 1
}

func digest(salt, value string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func newSalt() (string, error) {
	raw := make([]byte, saltBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// AudioLanguage maps a BCP 47 tag to the digit-reading language the library pronounces; only the primary subtag matters.
func AudioLanguage(tag string) (string, bool) {
	primary := strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(primary, "-_"); i >= 0 {
		primary = primary[:i]
	}
	switch primary {
	case "en", "ja", "ru", "zh":
		return primary, true
	default:
		return "", false
	}
}

func audioLanguage(lang string) (string, error) {
	if lang == "" {
		return DefaultAudioLanguage, nil
	}
	code, ok := AudioLanguage(lang)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownAudioLanguage, lang)
	}
	return code, nil
}

func (p *Provider) countIssue(ctx context.Context, form Form, remoteIP string) error {
	key, limit := issueKey(remoteIP), issueLimit
	if form == FormAudio {
		key, limit = audioIssueKey(remoteIP), audioIssueLimit
	}
	issued, err := p.store.IncrByFloatWithTTL(ctx, key, 1, IssueWindow)
	if err != nil {
		return fmt.Errorf("%w: captcha: counting the challenges issued to a source: %w", authn.ErrUnavailable, err)
	}
	if issued > float64(limit) {
		return ErrIssuanceLimited
	}
	total, err := p.store.IncrByFloatWithTTL(ctx, globalIssueKey(), 1, IssueWindow)
	if err != nil {
		return fmt.Errorf("%w: captcha: counting the challenges issued in the window: %w", authn.ErrUnavailable, err)
	}
	if total > float64(p.globalIssueLimit()) {
		return ErrIssuanceLimited
	}
	return nil
}

func newID() (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validID(id string) bool {
	if len(id) != base64.RawURLEncoding.EncodedLen(idBytes) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(raw) == idBytes
}

func validMedia(kind MediaKind) bool {
	switch kind {
	case MediaImage, MediaBackground, MediaPiece, MediaAudio:
		return true
	default:
		return false
	}
}

func contentType(kind MediaKind) string {
	if kind == MediaAudio {
		return "audio/wav"
	}
	return "image/png"
}

func challengeKey(id string) string { return keyPrefix + id + ":challenge" }

func issueKey(remoteIP string) string {
	sum := sha256.Sum256([]byte(remoteIP))
	return issueKeyPrefix + hex.EncodeToString(sum[:])
}

func audioIssueKey(remoteIP string) string {
	sum := sha256.Sum256([]byte(remoteIP))
	return audioIssueKeyPrefix + hex.EncodeToString(sum[:])
}

func globalIssueKey() string { return issueKeyPrefix + "global" }

func mediaKey(id string, kind MediaKind) string {
	return keyPrefix + id + ":media:" + string(kind)
}

func (p *Provider) purge(ctx context.Context, id string) {
	p.discard(ctx, challengeKey(id),
		mediaKey(id, MediaImage), mediaKey(id, MediaBackground),
		mediaKey(id, MediaPiece), mediaKey(id, MediaAudio))
}

func (p *Provider) discard(ctx context.Context, keys ...string) {
	for _, key := range keys {
		if err := p.store.Delete(ctx, key); err != nil {
			return
		}
	}
}
