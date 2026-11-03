package captcha

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// store is an in-memory kv.KV with a clock a case advances, so an expiry is
// exercised without waiting for one.
type store struct {
	mu     sync.Mutex
	now    time.Time
	values map[string]value
}

type value struct {
	body    []byte
	expires time.Time
}

func newStore() *store {
	return &store{now: time.Unix(0, 0), values: map[string]value{}}
}

func (s *store) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func (s *store) live(key string) (value, bool) {
	v, ok := s.values[key]
	if !ok {
		return value{}, false
	}
	if !v.expires.IsZero() && !s.now.Before(v.expires) {
		delete(s.values, key)
		return value{}, false
	}
	return v, true
}

func (s *store) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.live(key)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v.body...), true, nil
}

func (s *store) Set(_ context.Context, key string, body []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := value{body: append([]byte(nil), body...)}
	if ttl > 0 {
		v.expires = s.now.Add(ttl)
	}
	s.values[key] = v
	return nil
}

func (s *store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func (s *store) IncrByFloat(context.Context, string, float64) (float64, error) {
	return 0, errors.New("not used")
}

func (s *store) IncrByFloatWithTTL(_ context.Context, key string, delta float64, _ time.Duration) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current float64
	if v, ok := s.live(key); ok {
		current, _ = strconv.ParseFloat(string(v.body), 64)
	}
	current += delta
	s.values[key] = value{body: []byte(strconv.FormatFloat(current, 'g', -1, 64))}
	return current, nil
}

func (s *store) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.live(key)
	if !ok {
		return false, nil
	}
	if !bytes.Equal(v.body, old) {
		return false, nil
	}
	s.values[key] = value{body: append([]byte(nil), newVal...), expires: v.expires}
	return true, nil
}

func (s *store) SetIfAbsent(_ context.Context, key string, body []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live(key); ok {
		return false, nil
	}
	v := value{body: append([]byte(nil), body...)}
	if ttl > 0 {
		v.expires = s.now.Add(ttl)
	}
	s.values[key] = v
	return true, nil
}

// newProvider builds a provider over a fresh store.
func newProvider(t *testing.T) (*Provider, *store) {
	t.Helper()
	s := newStore()
	p, err := New(s)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, s
}

// readRecord reads the stored record of a challenge.
func readRecord(t *testing.T, s *store, id string) record {
	t.Helper()
	raw, present, err := s.Get(t.Context(), challengeKey(id))
	if err != nil || !present {
		t.Fatalf("reading the challenge record: present %v, err %v", present, err)
	}
	rec, err := decodeRecord(raw)
	if err != nil {
		t.Fatalf("decoding the challenge record: %v", err)
	}
	return rec
}

// decodeRecord reads a stored record.
func decodeRecord(raw []byte) (record, error) {
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return record{}, err
	}
	return rec, nil
}

// source is the address the provider's tests issue challenges from.
const source = "203.0.113.7"

// issue draws a challenge of one form for the test source.
func issue(t *testing.T, p *Provider, form Form) Challenge {
	t.Helper()
	issued, err := p.Issue(t.Context(), IssueOptions{Form: form, RemoteIP: source})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return issued
}

// solveDigits recovers the answer of an image or audio challenge by trying the
// five-digit space against the stored digest. The answer space is small; the
// one-time deletion is what keeps that from being a way in.
func solveDigits(t *testing.T, s *store, id string) string {
	t.Helper()
	rec := readRecord(t, s, id)
	if len(rec.Digests) != 1 {
		t.Fatalf("the record carries %d digests, want one", len(rec.Digests))
	}
	for i := 0; i < 100000; i++ {
		candidate := fmt.Sprintf("%05d", i)
		if digest(rec.Salt, candidate) == rec.Digests[0] {
			return candidate
		}
	}
	t.Fatal("no five-digit answer matches the stored digest")
	return ""
}

// solveSlider recovers the cut position of a slider challenge from the stored
// digests. It returns the lowest accepted offset, which is an answer the server
// accepts.
func solveSlider(t *testing.T, s *store, id string) int {
	t.Helper()
	min, _ := sliderRange(t, s, id)
	return min
}

// sliderRange reads the lowest and highest offsets the stored digests accept.
func sliderRange(t *testing.T, s *store, id string) (min, max int) {
	t.Helper()
	rec := readRecord(t, s, id)
	min, max = -1, -1
	for offset := 0; offset <= sliderWidth-pieceSize; offset++ {
		if matches(rec.Digests, digest(rec.Salt, strconv.Itoa(offset))) {
			if min < 0 {
				min = offset
			}
			max = offset
		}
	}
	if min < 0 {
		t.Fatal("no offset matches a stored digest")
	}
	return min, max
}

// outsideSlider is one offset beyond an accepted range.
func outsideSlider(t *testing.T, s *store, id string) int {
	t.Helper()
	min, max := sliderRange(t, s, id)
	if max < sliderWidth-pieceSize {
		return max + 1
	}
	return min - 1
}

// proof is the token a client presents.
func proof(id, answer string) authn.HumanProof {
	return authn.HumanProof{Token: id + "." + answer}
}

// TestAGeneratedChallengeVerifiesExactlyOnce holds the one-time property for
// every form: the answer that was drawn is accepted once, and the same proof is
// refused when it is presented again.
func TestAGeneratedChallengeVerifiesExactlyOnce(t *testing.T) {
	for _, form := range []Form{FormImage, FormSlider, FormAudio} {
		t.Run(string(form), func(t *testing.T) {
			p, s := newProvider(t)
			issued := issue(t, p, form)
			answer := answerOf(t, s, issued)
			if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); err != nil {
				t.Fatalf("the drawn answer was refused: %v", err)
			}
			if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("the second presentation resolved with %v, want ErrHumanVerificationRequired", err)
			}
		})
	}
}

// TestAnExpiredChallengeIsRefused holds the five-minute lifetime.
func TestAnExpiredChallengeIsRefused(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormImage)
	answer := solveDigits(t, s, issued.ID)
	s.advance(challengeTTL + time.Second)
	if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an expired challenge resolved with %v, want ErrHumanVerificationRequired", err)
	}
}

// TestAWrongAnswerSpendsTheChallenge holds that a failed attempt is the only
// attempt: the correct answer no longer works once a wrong one was presented.
func TestAWrongAnswerSpendsTheChallenge(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormImage)
	answer := solveDigits(t, s, issued.ID)
	wrong := answer + "0"
	if err := p.Verify(t.Context(), proof(issued.ID, wrong), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a wrong answer resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if _, present, _ := s.Get(t.Context(), challengeKey(issued.ID)); present {
		t.Error("the challenge record survived a failed verification")
	}
	if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("the correct answer resolved with %v after a wrong one, want ErrHumanVerificationRequired", err)
	}
}

// TestEachFormAcceptsItsAnswerAndRefusesAnother holds both directions for every
// form. The slider accepts offsets within its tolerance and refuses one outside
// it.
func TestEachFormAcceptsItsAnswerAndRefusesAnother(t *testing.T) {
	for _, form := range []Form{FormImage, FormSlider, FormAudio} {
		t.Run(string(form), func(t *testing.T) {
			p, s := newProvider(t)

			right := issue(t, p, form)
			if verifyErr := p.Verify(t.Context(), proof(right.ID, answerOf(t, s, right)), authn.HumanScopePhone); verifyErr != nil {
				t.Fatalf("the right answer was refused: %v", verifyErr)
			}

			failed := issue(t, p, form)
			if verifyErr := p.Verify(t.Context(), proof(failed.ID, wrongAnswerOf(t, s, failed)), authn.HumanScopePhone); !errors.Is(verifyErr, authn.ErrHumanVerificationRequired) {
				t.Fatalf("the wrong answer resolved with %v, want ErrHumanVerificationRequired", verifyErr)
			}
		})
	}
}

// TestTheSliderAcceptsAnOffsetWithinItsTolerance holds the tolerance the server
// allows a client that lands near, but not on, the cut position.
func TestTheSliderAcceptsAnOffsetWithinItsTolerance(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormSlider)
	_, max := sliderRange(t, s, issued.ID)
	if verifyErr := p.Verify(t.Context(), proof(issued.ID, strconv.Itoa(max)), authn.HumanScopePhone); verifyErr != nil {
		t.Fatalf("the last accepted offset was refused: %v", verifyErr)
	}

	failed := issue(t, p, FormSlider)
	outside := outsideSlider(t, s, failed.ID)
	if verifyErr := p.Verify(t.Context(), proof(failed.ID, strconv.Itoa(outside)), authn.HumanScopePhone); !errors.Is(verifyErr, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an offset outside the tolerance resolved with %v, want ErrHumanVerificationRequired", verifyErr)
	}
}

// answerOf recovers the answer a challenge was drawn with.
func answerOf(t *testing.T, s *store, issued Challenge) string {
	t.Helper()
	switch issued.Form {
	case FormSlider:
		return strconv.Itoa(solveSlider(t, s, issued.ID))
	default:
		return solveDigits(t, s, issued.ID)
	}
}

// wrongAnswerOf builds an answer that is not accepted.
func wrongAnswerOf(t *testing.T, s *store, issued Challenge) string {
	t.Helper()
	switch issued.Form {
	case FormSlider:
		return strconv.Itoa(outsideSlider(t, s, issued.ID))
	default:
		// The drawn answer is five digits; the next one in the space is wrong.
		right, err := strconv.Atoi(solveDigits(t, s, issued.ID))
		if err != nil {
			t.Fatalf("the drawn answer is not a number: %v", err)
		}
		return fmt.Sprintf("%05d", (right+1)%100000)
	}
}

// TestMediaIsStoredForEachForm holds that every media object a challenge names
// is stored with a media type and the signature of its format, and that an
// object the form does not carry is absent.
func TestMediaIsStoredForEachForm(t *testing.T) {
	cases := []struct {
		form    Form
		present []MediaKind
		absent  []MediaKind
	}{
		{FormImage, []MediaKind{MediaImage}, []MediaKind{MediaBackground, MediaPiece, MediaAudio}},
		{FormSlider, []MediaKind{MediaBackground, MediaPiece}, []MediaKind{MediaImage, MediaAudio}},
		{FormAudio, []MediaKind{MediaAudio}, []MediaKind{MediaImage, MediaBackground, MediaPiece}},
	}
	for _, tc := range cases {
		t.Run(string(tc.form), func(t *testing.T) {
			p, _ := newProvider(t)
			issued := issue(t, p, tc.form)
			for _, kind := range tc.present {
				media, err := p.Media(t.Context(), issued.ID, kind)
				if err != nil {
					t.Fatalf("Media(%s): %v", kind, err)
				}
				if len(media.Body) == 0 {
					t.Errorf("Media(%s) is empty", kind)
				}
				switch kind {
				case MediaAudio:
					if media.ContentType != "audio/wav" || !bytes.HasPrefix(media.Body, []byte("RIFF")) {
						t.Errorf("the audio media is %q without a RIFF header", media.ContentType)
					}
				default:
					if media.ContentType != "image/png" || !bytes.HasPrefix(media.Body, []byte("\x89PNG\r\n\x1a\n")) {
						t.Errorf("the image media is %q without a PNG header", media.ContentType)
					}
				}
			}
			for _, kind := range tc.absent {
				if _, err := p.Media(t.Context(), issued.ID, kind); !errors.Is(err, ErrChallengeNotFound) {
					t.Errorf("Media(%s) resolved with %v, want ErrChallengeNotFound", kind, err)
				}
			}
		})
	}
}

// TestMediaRefusesUnknownIDs holds the route's other half: an id this provider
// did not draw, and one that is not even the right shape, are not found.
func TestMediaRefusesUnknownIDs(t *testing.T) {
	p, _ := newProvider(t)
	for _, id := range []string{"", "not-an-id", "aaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := p.Media(t.Context(), id, MediaImage); !errors.Is(err, ErrChallengeNotFound) {
			t.Errorf("Media(%q) resolved with %v, want ErrChallengeNotFound", id, err)
		}
	}
}

// TestTheRecordDoesNotCarryTheAnswerInPlainText holds the protection on the
// stored answer: the record carries a salted digest, not the solution and not
// the answer's bare SHA-256.
func TestTheRecordDoesNotCarryTheAnswerInPlainText(t *testing.T) {
	for _, form := range []Form{FormImage, FormAudio} {
		t.Run(string(form), func(t *testing.T) {
			p, s := newProvider(t)
			issued := issue(t, p, form)
			answer := solveDigits(t, s, issued.ID)
			rec := readRecord(t, s, issued.ID)
			raw, present, err := s.Get(t.Context(), challengeKey(issued.ID))
			if err != nil || !present {
				t.Fatalf("reading the record: present %v, err %v", present, err)
			}
			if bytes.Contains(raw, []byte(answer)) {
				t.Errorf("the record carries the answer %q in plain text: %s", answer, raw)
			}
			if !bytes.Contains(raw, []byte(digest(rec.Salt, answer))) {
				t.Errorf("the record does not carry the salted digest of the answer: %s", raw)
			}
			sum := sha256.Sum256([]byte(answer))
			if bytes.Contains(raw, []byte(hex.EncodeToString(sum[:]))) {
				t.Errorf("the record carries the unsalted SHA-256 of the answer: %s", raw)
			}
		})
	}
}

// TestAProofOverTheLimitIsRefused holds the field bound: a body sent in place
// of a proof is the too-long error, not a challenge lookup.
func TestAProofOverTheLimitIsRefused(t *testing.T) {
	p, _ := newProvider(t)
	long := make([]byte, maxProofLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := p.Verify(t.Context(), authn.HumanProof{Token: string(long)}, authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanProofTooLong) {
		t.Fatalf("an over-long proof resolved with %v, want ErrHumanProofTooLong", err)
	}
}

// TestAnUnknownFormIsRefused holds the closed set of forms.
func TestAnUnknownFormIsRefused(t *testing.T) {
	p, _ := newProvider(t)
	if _, err := p.Issue(t.Context(), IssueOptions{Form: Form("puzzle"), RemoteIP: source}); !errors.Is(err, ErrUnknownForm) {
		t.Fatalf("Issue with an unknown form resolved with %v, want ErrUnknownForm", err)
	}
}

// TestTwoProvidersShareOneChallenge holds the multi-replica property: a
// challenge issued by one provider over a store is verified by another.
func TestTwoProvidersShareOneChallenge(t *testing.T) {
	s := newStore()
	first, err := New(s)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := New(s)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	issued := issue(t, first, FormImage)
	answer := solveDigits(t, s, issued.ID)
	if err := second.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); err != nil {
		t.Fatalf("a second provider refused a shared challenge: %v", err)
	}
}

// TestOneProofCannotBeSpentTwiceConcurrently holds the compare-and-swap: of
// several presentations of one correct proof at once, exactly one is accepted.
func TestOneProofCannotBeSpentTwiceConcurrently(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormImage)
	presented := proof(issued.ID, solveDigits(t, s, issued.ID))

	const callers = 8
	var wg sync.WaitGroup
	results := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = p.Verify(context.Background(), presented, authn.HumanScopePhone)
		}(i)
	}
	wg.Wait()

	accepted := 0
	for _, err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d presentations were accepted, want exactly one", accepted)
	}
}

// TestTheIssuanceCapBoundsOneSource holds the anti-fill bound: one source may
// hold a limited number of challenges at a time, and a source that asks for
// more is refused while another source is not affected.
func TestTheIssuanceCapBoundsOneSource(t *testing.T) {
	p, _ := newProvider(t)
	for i := 0; i < issueLimit; i++ {
		if _, err := p.Issue(t.Context(), IssueOptions{Form: FormImage, RemoteIP: source}); err != nil {
			t.Fatalf("Issue %d: %v", i+1, err)
		}
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Form: FormImage, RemoteIP: source}); !errors.Is(err, ErrIssuanceLimited) {
		t.Fatalf("the cap resolved with %v, want ErrIssuanceLimited", err)
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Form: FormImage, RemoteIP: "203.0.113.8"}); err != nil {
		t.Fatalf("another source was refused with the first source's cap: %v", err)
	}
}

// TestTheStoredDigestIsSalted holds that the stored digest is not the bare
// SHA-256 of the answer: a leaked digest alone must not let an offline
// enumerator reverse a five-digit answer.
func TestTheStoredDigestIsSalted(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormImage)
	answer := solveDigits(t, s, issued.ID)
	raw, present, err := s.Get(t.Context(), challengeKey(issued.ID))
	if err != nil || !present {
		t.Fatalf("reading the record: present %v, err %v", present, err)
	}
	sum := sha256.Sum256([]byte(answer))
	if bytes.Contains(raw, []byte(hex.EncodeToString(sum[:]))) {
		t.Errorf("the record carries the unsalted SHA-256 of the answer: %s", raw)
	}
}

// TestTheSaltIsDrawnPerChallenge holds that two challenges do not share a salt,
// so one digest table does not answer for another challenge.
func TestTheSaltIsDrawnPerChallenge(t *testing.T) {
	p, s := newProvider(t)
	first := issue(t, p, FormImage)
	second := issue(t, p, FormImage)
	firstRecord := readRecord(t, s, first.ID)
	secondRecord := readRecord(t, s, second.ID)
	if firstRecord.Salt == "" || secondRecord.Salt == "" {
		t.Fatalf("a challenge record carries no salt: %q and %q", firstRecord.Salt, secondRecord.Salt)
	}
	if firstRecord.Salt == secondRecord.Salt {
		t.Errorf("two challenges share the salt %q", firstRecord.Salt)
	}
	if digest(firstRecord.Salt, "12345") == digest(secondRecord.Salt, "12345") {
		t.Error("one answer digest is the same under two challenges' salts")
	}
}

// TestARecordWithoutItsSaltIsRefused holds that a record this provider did not
// draw, which carries no salt, is not usable rather than checked unsalted.
func TestARecordWithoutItsSaltIsRefused(t *testing.T) {
	p, s := newProvider(t)
	issued := issue(t, p, FormImage)
	answer := solveDigits(t, s, issued.ID)
	rec := readRecord(t, s, issued.ID)
	rec.Salt = ""
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}
	if err := s.Set(t.Context(), challengeKey(issued.ID), raw, challengeTTL); err != nil {
		t.Fatalf("rewriting the record: %v", err)
	}
	if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a record with no salt resolved with %v, want ErrHumanVerificationRequired", err)
	}
}

// TestAnUnsupportedAudioLanguageIsRefused holds that the provider does not
// silently draw an unsupported language in English.
func TestAnUnsupportedAudioLanguageIsRefused(t *testing.T) {
	p, _ := newProvider(t)
	if _, err := p.Issue(t.Context(), IssueOptions{Form: FormAudio, Lang: "klingon", RemoteIP: source}); !errors.Is(err, ErrUnknownAudioLanguage) {
		t.Fatalf("an unsupported audio language resolved with %v, want ErrUnknownAudioLanguage", err)
	}
}

// TestASupportedAudioLanguageIsAccepted holds that the primary subtag of a BCP
// 47 tag selects the language the library pronounces.
func TestASupportedAudioLanguageIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		tag  string
		want string
	}{
		{"zh-CN", "zh"},
		{"en-US", "en"},
		{"ja", "ja"},
		{"ru_RU", "ru"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			got, ok := AudioLanguage(tc.tag)
			if !ok || got != tc.want {
				t.Errorf("AudioLanguage(%q) = %q, %v; want %q, true", tc.tag, got, ok, tc.want)
			}
		})
	}
	p, _ := newProvider(t)
	if _, err := p.Issue(t.Context(), IssueOptions{Form: FormAudio, Lang: "zh-CN", RemoteIP: source}); err != nil {
		t.Fatalf("Issue in Chinese: %v", err)
	}
}

// TestNewRequiresAStore holds the constructor's precondition.
func TestNewRequiresAStore(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New accepted no store")
	}
}
