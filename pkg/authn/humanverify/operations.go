package humanverify

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn humanverify"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the module's API root,
// so the absolute paths are /api/v1/auth/human/challenge and
// /api/v1/auth/human/captcha/challenge.
const (
	challengePath        = "/auth/human/challenge"
	captchaChallengePath = "/auth/human/captcha/challenge"
)

// typePrefix starts the Problem.Type of every failure this module's handler
// writes, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// ChallengeResponse is the answer of the challenge route: the challenge a
// client solves, the difficulty its proof must reach, and how long the
// challenge stays valid.
type ChallengeResponse struct {
	_ struct{} `description:"A proof-of-work challenge, the work it takes to solve, and how long it stays valid."`

	Provider   string `json:"provider" required:"true" description:"The mechanism that issued the challenge: the constant pow."`
	Algorithm  string `json:"algorithm" required:"true" description:"The hash a proof is computed with: sha256."`
	Challenge  string `json:"challenge" required:"true" description:"The random challenge, base64url without padding. Solve it into a nonce, and present the two joined by a dot as the proof."`
	Difficulty int    `json:"difficulty" required:"true" description:"The number of leading zero bits the proof's hash must carry; higher values take exponentially more work."`
	ExpiresIn  int    `json:"expires_in" required:"true" description:"How long the challenge stays valid, in whole seconds. A proof that names an expired challenge is refused."`
}

// challengeOperation declares the public route that issues a challenge. Its
// handler builds the answer because the success body carries the difficulty and
// the lifetime, neither of which the declaration can compute.
var challengeOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.humanverify.challenge",
	http.MethodPost,
	challengePath,
	"Obtain a proof-of-work challenge",
	"Issues one human-verification challenge for the proof-of-work mechanism. The client solves the "+
		"challenge by finding a nonce whose SHA-256 hash, over the decoded challenge followed by the "+
		"nonce in big-endian, carries at least `difficulty` leading zero bits, then presents "+
		"`challenge.nonce` as the proof to the send it wants to make. A proof is single-use: the "+
		"challenge is spent when it is accepted. A source that asks for more challenges than its window "+
		"allows is answered 429, and a key-value store that cannot answer is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "A challenge, its difficulty and its lifetime",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(ChallengeResponse{})},
		},
		speedhttp.ProblemResponseOf(http.StatusTooManyRequests,
			"This source has asked for as many challenges as its window allows; retry later",
			typePrefix+mount.CodeThrottled),
		speedhttp.ProblemResponseOf(http.StatusServiceUnavailable,
			"The challenge could not be issued because the key-value store was unavailable",
			typePrefix+mount.CodeUnavailable),
	},
	speedhttp.Public(),
)

// captchaChallengeInput is the challenge route's input: the accessibility form
// the caller may ask for and, for that form, the language its digits are spoken
// in. The image and slider forms are not a caller's choice.
type captchaChallengeInput struct {
	Form string `query:"form" enum:"image,slider,audio" description:"The challenge form. Only audio is honoured: it draws the spoken reading of digits, the screen-reader alternative. The value image or slider, or an absent item, is ignored: the server rotates between those two forms from a cryptographic random source and answers whichever it drew. Any other value is a 400 bad-request."`
	Lang string `query:"lang" description:"The language the audio form's digits are spoken in, as a BCP 47 tag. The provider pronounces English, Japanese, Russian and Chinese; a tag naming another language is a 400. Absent or empty uses the request's active locale, falling back to English for a locale the provider does not pronounce. Ignored by the rotating forms."`
}

// captchaChallengeOperation declares the public route that issues a self-hosted
// captcha challenge. Its handler builds the answer because the success body
// carries the media paths the mounter resolves from its configured base, which
// the declaration cannot compute.
var captchaChallengeOperation = speedhttp.NewReplyOperation[captchaChallengeInput](
	"authn.humanverify.captchaChallenge",
	http.MethodPost,
	captchaChallengePath,
	"Obtain a captcha challenge",
	"Issues one human-verification challenge for the self-hosted captcha mechanism. "+
		"Only the `audio` form may be chosen: the `form` query item naming it draws a spoken "+
		"reading of digits as the screen-reader alternative. The server rotates between the "+
		"image and the slider form from a cryptographic random source, so `form=image`, "+
		"`form=slider` and an absent item are all answered with whichever of the two the "+
		"rotation drew; the value is not honoured, and any other value is a 400. The `lang` "+
		"query item names the language an audio challenge is spoken in; the provider "+
		"pronounces English, Japanese, Russian and Chinese, an unsupported language is a 400, "+
		"and an absent item uses the request's active locale. The answer names the challenge "+
		"id, the form, the instruction key a client words for that form, and the media paths "+
		"the form carries. A proof is the challenge id and the answer joined by a dot; it is "+
		"single-use and spends the challenge whether the answer matched or not. A source that "+
		"asks for more challenges than the window of the form it is requesting allows is "+
		"answered 429; the rotating forms keep a larger per-source bound, the audio form has "+
		"its own stricter one, and the two counters neither spend nor are spent by the other. "+
		"A key-value store that cannot answer is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "A captcha challenge, its form, its instruction key and its media paths",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(CaptchaChallengeResponse{})},
		},
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"The form or audio language is not one this provider draws",
			typePrefix+mount.CodeBadRequest),
		speedhttp.ProblemResponseOf(http.StatusTooManyRequests,
			"This source has asked for as many challenges as the window of the form it is requesting allows; "+
				"the audio form has its own, stricter per-source bound than the rotating forms. Retry later",
			typePrefix+mount.CodeThrottled),
		speedhttp.ProblemResponseOf(http.StatusServiceUnavailable,
			"The challenge could not be issued because the key-value store was unavailable",
			typePrefix+mount.CodeUnavailable),
	},
	speedhttp.Public(),
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{
	challengeOperation,
	captchaChallengeOperation,
}
