// Package captcha is the self-hosted captcha provider of the human-verification
// gate. One provider draws three challenge forms: an image of digits, a slider
// puzzle and an audio reading of digits. The image and the audio are drawn by
// github.com/mojocn/base64Captcha; the slider is drawn with the standard
// library's image operations.
//
// Audio is the one form a client may ask for: it is the screen-reader
// alternative, and a request naming it gets it. The image and slider forms are
// the server's rotation choice, drawn from crypto/rand at every issuance and
// never from anything the request carries, so a client that names either of
// them (or names none) is answered with whichever form the rotation drew. The
// rotation needs no store key of its own.
//
// Every form keeps its challenge in the key-value store for five minutes. The
// record holds the answer as digests keyed by a random per-challenge salt rather
// than plain text, and the media the routes serve are stored beside it under the
// same expiry. Verification reads the record, compares the answer in constant
// time, spends the record with a compare-and-swap so two presentations of one
// proof cannot both pass, and then deletes the record and the media. A wrong
// answer spends the record too: a challenge is good for one attempt.
//
// Issuance is bounded per source over a one-minute fixed window, and a source
// past its bound is refused before a challenge is drawn or stored. The visual
// rotation has one counter (ten challenges a window); the accessibility
// alternative has a separate, stricter one (one challenge a window), so neither
// budget spends the other.
//
// The three forms are hardened against their own automation: the image driver
// gets a random size, a larger skew and a denser dot field; the slider draws a
// random background, a random piece size and per-pixel noise, and keeps its
// tolerance at a few pixels; the audio is post-processed into a randomly
// slowed or quickened reading with extra noise and a competing spoken stream.
// The form's answer, storage and one-time semantics are unchanged by the
// hardening.
//
// The proof is the challenge id and the answer joined by a dot. The id is
// base64url without padding and the answer is digits, or a decimal offset for
// the slider; neither part can contain a dot, so the last dot separates them.
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
)

// ProviderName is the mechanism name this implementation reports to the seam.
const ProviderName = "captcha"

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

// The storage keys and the lifetime every challenge and its media share. The
// keys are derived from the challenge id, which this provider draws, so a
// caller cannot choose a key.
const (
	keyPrefix           = "authn:human:captcha:"
	issueKeyPrefix      = "authn:human:captcha:issue:"
	audioIssueKeyPrefix = "authn:human:captcha:audio-issue:"
	challengeTTL        = 5 * time.Minute
	idBytes             = 16
	saltBytes           = 16
	maxProofLen         = 512
	maxAnswerBytes      = 64
	// issueLimit is how many visual challenges one source may hold in
	// IssueWindow. It is small on purpose: an anonymous caller is the reason the
	// counter exists, and a challenge that lapses is served by a new request once
	// the window expires.
	issueLimit = 10
	// audioIssueLimit is the visual forms' counterpart for the accessibility
	// alternative. Audio rendering is the most expensive form and it is requested
	// explicitly rather than rotated, so one source gets one audio challenge per
	// window; asking again is refused until the window expires.
	audioIssueLimit = 1
)

// IssueWindow is the fixed window the per-source issuance counter covers: the
// counter starts with the first challenge of a window and expires a minute
// later, so sustained traffic does not slide it forward. It is also the longest
// wait a route states when it refuses a source past the bound.
const IssueWindow = time.Minute

// spentMarker is what a challenge key holds after the challenge was spent. It
// is not a record, so a second presentation is refused before any answer is
// compared.
var spentMarker = []byte("spent")

// The parameter choices of the library-backed forms. The image's height, width,
// skew and dot count are drawn per challenge: the driver fixes its font, so
// varying the geometry is how the digit size changes, and the larger skew,
// denser dots and the driver's own wave distortion and strike-through line make
// the characters harder to segment. The digit count stays fixed so the answer
// space and its digests are unchanged.
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

// Errors a caller maps onto its own answers. ErrUnknownForm and
// ErrUnknownAudioLanguage are bad requests; ErrChallengeNotFound is an unknown,
// expired or already spent challenge, and a media route answers it with a 404;
// ErrIssuanceLimited is the retryable 429 of a source past its issuance bound.
var (
	// ErrUnknownForm reports a form this provider does not draw.
	ErrUnknownForm = errors.New("captcha: unknown challenge form")
	// ErrUnknownAudioLanguage reports a language the library pronounces no
	// digits in.
	ErrUnknownAudioLanguage = errors.New("captcha: the library pronounces no digits in this language")
	// ErrChallengeNotFound reports that no challenge is stored under the id.
	ErrChallengeNotFound = errors.New("captcha: the challenge is missing or expired")
	// ErrIssuanceLimited reports that one source has asked for as many
	// challenges as it may hold in the current window. It is not an outage:
	// retrying later works, and the answer is a 429.
	ErrIssuanceLimited = errors.New("captcha: too many challenges were issued to this source")
)

// Challenge is what Issue answers: the id a proof names, the form it takes and
// how long it stays valid.
type Challenge struct {
	ID        string
	Form      Form
	ExpiresIn time.Duration
}

// Media is one media object a challenge route serves.
type Media struct {
	ContentType string
	Body        []byte
}

// formRotation chooses the form of a challenge a client may not name.
type formRotation func() (Form, error)

// Provider is the self-hosted captcha verifier.
type Provider struct {
	store kv.KV
	// rotate draws the form of a challenge whose form the client did not
	// choose. Production sets randomForm; a test that needs a known form
	// injects its own through newProviderWithRotation.
	rotate formRotation
}

var _ authn.HumanVerifier = (*Provider)(nil)

// New builds the provider over a key-value store. The rotating forms are drawn
// from crypto/rand.
func New(store kv.KV) (*Provider, error) {
	return newProviderWithRotation(store, randomForm)
}

// newProviderWithRotation builds the provider with the given rotation source.
// It is the seam the package's own tests use to pin a form; production callers
// use New, whose rotation is random.
func newProviderWithRotation(store kv.KV, rotate formRotation) (*Provider, error) {
	if store == nil {
		return nil, errors.New("captcha: a key-value store is required")
	}
	if rotate == nil {
		return nil, errors.New("captcha: a form rotation is required")
	}
	return &Provider{store: store, rotate: rotate}, nil
}

// Required reports true: a captcha gate is a condition of sending.
func (p *Provider) Required(authn.HumanScope) bool { return true }

// Provider reports the mechanism name.
func (p *Provider) Provider(authn.HumanScope) string { return ProviderName }

// SiteKey is empty: a self-hosted captcha needs no widget key.
func (p *Provider) SiteKey(authn.HumanScope) string { return "" }

// IssueOptions is what a caller asks a challenge with.
type IssueOptions struct {
	// Form is the form the caller asks for. FormAudio is honoured: it is the
	// accessibility alternative, the one form a client may choose. FormImage,
	// FormSlider and the empty value are the server's rotation choice, so the
	// value is not consulted and the answer carries whichever of the two the
	// rotation drew. Any other value is ErrUnknownForm.
	Form Form
	// Lang is the language an audio challenge is spoken in, as a BCP 47 tag. It
	// is read only by FormAudio; an empty value is DefaultAudioLanguage. The
	// library pronounces English, Japanese, Russian and Chinese, so a tag whose
	// primary subtag is none of those is ErrUnknownAudioLanguage.
	Lang string
	// RemoteIP is the source the issuance counter is spent for. It is hashed
	// before it reaches the store, because the key is a value the store and its
	// diagnostics may carry.
	RemoteIP string
}

// Issue draws a challenge of the resolved form and stores its answer and media.
// Audio is the only form the caller may choose; the rotating forms are drawn by
// the provider. The source's issuance counter is spent before anything is
// drawn, so an address past its bound is refused and cannot fill the store.
func (p *Provider) Issue(ctx context.Context, opts IssueOptions) (Challenge, error) {
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
	encoded, err := json.Marshal(rec)
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: captcha: encoding the challenge: %w", authn.ErrUnavailable, err)
	}

	// The media is written first and the record last, so a challenge becomes
	// usable only once everything its client fetches is in place. A failed
	// write removes what this call already stored.
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

// Media reads one media object of a challenge. An id this provider did not draw
// is ErrChallengeNotFound, as is one whose media has expired or was already
// spent.
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

// Verify checks a proof and spends the challenge it names. The answer is
// compared against digests stored with the challenge, and the challenge is then
// spent with a compare-and-swap requiring the value that was read, so two
// concurrent presentations cannot both pass. The record and its media are
// deleted whether the answer matched or not: one challenge is one attempt.
func (p *Provider) Verify(ctx context.Context, proof authn.HumanProof, _ authn.HumanScope) error {
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
	if decodeErr := json.Unmarshal(stored, &rec); decodeErr != nil || !rec.Form.Valid() || rec.Salt == "" {
		// A record this package cannot read is spent rather than left for a
		// second attempt. A record with no salt is not one this provider drew,
		// and its digests cannot be checked.
		_, _ = p.store.CompareAndSwap(ctx, key, stored, spentMarker)
		p.discard(ctx, key)
		return fmt.Errorf("%w: captcha: the challenge is not usable", authn.ErrHumanVerificationRequired)
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

// record is what a challenge key holds: the form the challenge takes, the random
// salt its digests are keyed by, and the digests of the answers it accepts. An
// answer is never stored as text, and the slider stores one digest per offset
// within its tolerance. The salt is drawn per challenge and stored beside the
// digests, so a digest copied out of the record without its salt cannot be
// reversed over a five-digit answer space.
type record struct {
	Form    Form     `json:"form"`
	Salt    string   `json:"salt"`
	Digests []string `json:"digests"`
}

// resolveForm decides the form of one challenge. Audio is the only form a
// caller may choose, because it is the accessibility alternative; the image and
// slider forms, and the empty value, are the server's rotation choice, so the
// request's value is not consulted. Any other value is refused before the
// issuance counter is spent. A rotation that answers a form outside the two it
// rotates between is an outage rather than a challenge.
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

// randomForm draws the rotation form from crypto/rand: one random byte, even
// image and odd slider. It reads nothing the request carries. A byte-sized
// source divides evenly by two, so neither form is favoured.
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

// drawForm builds the record and the media of one form.
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

// drawDigits draws the image form with the library's digit driver, with the
// hardening parameters drawn per challenge.
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

// drawAudio draws the audio form with the library's audio driver in the
// resolved language.
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

// drawSlider draws the slider form with the standard library's image
// operations.
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

// itemBytes reads a library item into the bytes a route serves.
func itemBytes(item libcaptcha.Item) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := item.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// splitProof reads the id.answer form. The last dot separates the parts, the id
// has to be one this provider drew, and the answer has to be non-empty and
// short.
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

// normalizeAnswer writes the answer the way its form's digests were computed.
// The slider answer is a decimal offset; the other forms are digits.
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

// matches reports whether value is one of the recorded digests. Every digest is
// compared, without an early exit, so the time does not depend on where the
// match is.
func matches(digests []string, value string) bool {
	var found int
	for _, candidate := range digests {
		found |= subtle.ConstantTimeCompare([]byte(candidate), []byte(value))
	}
	return found == 1
}

// digest is the lowercase hexadecimal HMAC-SHA-256 of value under the
// challenge's random salt. The record keeps this rather than the answer, so a
// digest copied out of the record without the salt is not the bare SHA-256 of a
// five-digit answer and cannot be reversed by enumeration. The record carries
// the salt beside the digest; holding the whole record is what an enumerator
// needs, which is why the salt alone is not treated as a secret.
func digest(salt, value string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// newSalt draws the random salt a challenge's digests are keyed by.
func newSalt() (string, error) {
	raw := make([]byte, saltBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// AudioLanguage maps a BCP 47 language tag to the digit-reading language the
// library pronounces, reporting whether it pronounces that language. Only the
// primary subtag matters, so "zh-CN" is Chinese and "en-US" is English. The
// library carries English, Japanese, Russian and Chinese sounds.
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

// audioLanguage resolves the language an audio challenge is spoken in. An empty
// value is the default; anything else has to be one the library pronounces.
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

// countIssue spends one slot of the source's issuance window for the form. The
// accessibility alternative has a counter of its own, in a separate key and
// with a stricter bound, so audio requests neither spend nor are spent by the
// visual rotation's allowance.
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
	return nil
}

// newID draws a challenge id: sixteen random bytes, base64url without padding.
func newID() (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// validID reports whether id is one this provider could have drawn.
func validID(id string) bool {
	if len(id) != base64.RawURLEncoding.EncodedLen(idBytes) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(raw) == idBytes
}

// validMedia reports whether kind is one this provider stores.
func validMedia(kind MediaKind) bool {
	switch kind {
	case MediaImage, MediaBackground, MediaPiece, MediaAudio:
		return true
	default:
		return false
	}
}

// contentType is the media type of one stored object.
func contentType(kind MediaKind) string {
	if kind == MediaAudio {
		return "audio/wav"
	}
	return "image/png"
}

// challengeKey is where one challenge record is stored.
func challengeKey(id string) string { return keyPrefix + id + ":challenge" }

// issueKey is one source's visual issuance counter. The source is hashed
// because the key is a value the store and its diagnostics may carry.
func issueKey(remoteIP string) string {
	sum := sha256.Sum256([]byte(remoteIP))
	return issueKeyPrefix + hex.EncodeToString(sum[:])
}

// audioIssueKey is one source's audio issuance counter, kept apart from the
// visual one so the accessibility alternative has its own stricter budget.
func audioIssueKey(remoteIP string) string {
	sum := sha256.Sum256([]byte(remoteIP))
	return audioIssueKeyPrefix + hex.EncodeToString(sum[:])
}

// mediaKey is where one media object of a challenge is stored.
func mediaKey(id string, kind MediaKind) string {
	return keyPrefix + id + ":media:" + string(kind)
}

// purge removes a challenge's record and every media object beside it. It runs
// after the record was already spent, so a failed delete leaves an entry that
// can no longer be verified and expires on its own.
func (p *Provider) purge(ctx context.Context, id string) {
	p.discard(ctx, challengeKey(id),
		mediaKey(id, MediaImage), mediaKey(id, MediaBackground),
		mediaKey(id, MediaPiece), mediaKey(id, MediaAudio))
}

// discard deletes the keys, stopping at the first failure. The caller has
// nothing to do about a failed delete beyond letting the expiry collect the
// entry.
func (p *Provider) discard(ctx context.Context, keys ...string) {
	for _, key := range keys {
		if err := p.store.Delete(ctx, key); err != nil {
			return
		}
	}
}
