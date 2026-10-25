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

// challengePath is the route as the fragment writes it: relative to the
// module's API root, so the absolute path is /api/v1/auth/human/challenge.
const challengePath = "/auth/human/challenge"

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

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{
	challengeOperation,
}
