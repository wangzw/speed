package humanverify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// mountChallenge registers the public challenge route on the route mounter the
// authentication capability mounts its ordinary routes through. The route is
// registered from the operation that declares it, so the handler's input and
// answer types, the fragment and the route are one source.
func mountChallenge(mounter mount.Mounter, provider *pow.Provider) {
	mount.Declared(mounter, challengeOperation, mountRel(challengeOperation.Path),
		challengeOperation.Handle(func(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
			return issueChallenge(ctx, provider, meta)
		}))
}

// issueChallenge answers the challenge route with the challenge the provider
// draws for the request's source address.
func issueChallenge(ctx context.Context, provider *pow.Provider, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	challenge, err := provider.Issue(ctx, meta.ClientIP)
	if err != nil {
		return nil, answerIssue(err)
	}
	return speedhttp.JSONReply(http.StatusOK, ChallengeResponse{
		Provider:   pow.ProviderName,
		Algorithm:  pow.ProofHash,
		Challenge:  challenge.Challenge,
		Difficulty: challenge.Difficulty,
		// Whole seconds, rounded up, so the client never reads a lifetime
		// shorter than the one the challenge was stored with.
		ExpiresIn: int((challenge.ExpiresIn + time.Second - 1) / time.Second),
	}), nil
}

// answerIssue maps an issuance failure onto the route's declared answers. The
// per-source cap is a retryable refusal; everything else is the outage that
// sends no challenge.
func answerIssue(err error) error {
	if errors.Is(err, pow.ErrIssuanceLimited) {
		return speedhttp.Fault{Status: http.StatusTooManyRequests, Type: typePrefix + mount.CodeThrottled}
	}
	return speedhttp.Fault{Status: http.StatusServiceUnavailable, Type: typePrefix + mount.CodeUnavailable}
}

// mountRel turns the declared route into the relative path the mounter joins to
// its base. The declaration writes the route against the module's API root, and
// the fragment is a static declaration fixed before configuration, so the part
// of it the capability owns is the default mount base less that root. Removing
// it leaves the relative path the mounter takes, whatever base the deployment
// configured: a configured base moves the route without moving the contract. A
// declared route that does not begin with the owned part is a declaration
// defect, not something to mount at a guessed path.
func mountRel(declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, humanverifySpec.ResolvePath(declared))
	if err != nil {
		panic(ModuleName + ": " + err.Error())
	}
	return rel
}
