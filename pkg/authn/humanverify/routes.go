package humanverify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/humanverify/captcha"
	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
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

// The captcha media routes, relative to the capability's ordinary base. The
// challenge route is declared in operations.go; the media routes serve the
// objects it names by id.
const (
	captchaImagePath      = "/human/captcha/image/:id"
	captchaBackgroundPath = "/human/captcha/background/:id"
	captchaPiecePath      = "/human/captcha/piece/:id"
	captchaAudioPath      = "/human/captcha/audio/:id"
)

// CaptchaChallengeResponse is the answer of the captcha challenge route: the id
// a proof names, the form the challenge takes, the instruction a client shows
// for that form, and the paths of the media it fetches. Only the media of the
// chosen form are named.
//
// Instruction is a stable key rather than a sentence: the text a person reads is
// the client's language resource, not this module's.
type CaptchaChallengeResponse struct {
	_ struct{} `description:"A captcha challenge: its id, the form it takes, the instruction key a client words for that form, the media paths the form carries and how long it stays valid."`

	Provider      string `json:"provider" required:"true" description:"The mechanism that issued the challenge: the constant captcha."`
	Form          string `json:"form" required:"true" enum:"image,slider,audio" description:"The challenge form: image for a picture of digits, slider for a background and a piece to slide over it, audio for a spoken reading of digits."`
	Instruction   string `json:"instruction" required:"true" description:"A stable key naming the instruction a client shows for the form. The text itself is the client's language resource."`
	ID            string `json:"id" required:"true" description:"The challenge id a proof names, base64url without padding."`
	ImageURL      string `json:"image_url,omitempty" description:"The path of the digit picture of an image challenge."`
	BackgroundURL string `json:"background_url,omitempty" description:"The path of the background of a slider challenge."`
	PieceURL      string `json:"piece_url,omitempty" description:"The path of the piece a slider challenge slides over its background."`
	AudioURL      string `json:"audio_url,omitempty" description:"The path of the spoken digits of an audio challenge."`
	ExpiresIn     int    `json:"expires_in" required:"true" description:"How long the challenge stays valid, in whole seconds. A proof that names an expired challenge is refused."`
}

// mountCaptcha registers the captcha routes on the route mounter. The challenge
// route is public, like the proof-of-work challenge route, because a client
// obtains a proof before it has any identity to send. The media routes are
// public for the same reason and are addressed by the unguessable id the
// challenge route hands out.
func mountCaptcha(mounter mount.Mounter, provider *captcha.Provider) {
	mount.Declared(mounter, captchaChallengeOperation, mountRel(captchaChallengeOperation.Path),
		captchaChallenge(mounter, provider))
	mounter.Public(http.MethodGet, captchaImagePath, captchaMedia(provider, captcha.MediaImage))
	mounter.Public(http.MethodGet, captchaBackgroundPath, captchaMedia(provider, captcha.MediaBackground))
	mounter.Public(http.MethodGet, captchaPiecePath, captchaMedia(provider, captcha.MediaPiece))
	mounter.Public(http.MethodGet, captchaAudioPath, captchaMedia(provider, captcha.MediaAudio))
}

// captchaChallenge answers the challenge route. The form is named by the
// request's form query item; an empty value is the image form. The audio form's
// language is named by the lang query item, or taken from the request's active
// locale when the item is absent.
func captchaChallenge(mounter mount.Mounter, provider *captcha.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		form := captcha.Form(c.Query("form"))
		language := captcha.DefaultAudioLanguage
		if form == captcha.FormAudio {
			var ok bool
			language, ok = audioLanguage(c)
			if !ok {
				mount.Fail(c, mount.CodeBadRequest)
				return
			}
		}
		issued, err := provider.Issue(ctx, captcha.IssueOptions{
			Form:     form,
			Lang:     language,
			RemoteIP: c.ClientIP(),
		})
		switch {
		case errors.Is(err, captcha.ErrUnknownForm), errors.Is(err, captcha.ErrUnknownAudioLanguage):
			mount.Fail(c, mount.CodeBadRequest)
			return
		case errors.Is(err, captcha.ErrIssuanceLimited):
			mount.Throttled(c, 0)
			return
		case err != nil:
			mount.Unavailable(c)
			return
		}

		base := mounter.BaseIn("")
		answer := CaptchaChallengeResponse{
			Provider:    captcha.ProviderName,
			Form:        string(issued.Form),
			Instruction: captchaInstruction(issued.Form),
			ID:          issued.ID,
			ExpiresIn:   int((issued.ExpiresIn + time.Second - 1) / time.Second),
		}
		switch issued.Form {
		case captcha.FormImage:
			answer.ImageURL = mediaPath(base, captchaImagePath, issued.ID)
		case captcha.FormSlider:
			answer.BackgroundURL = mediaPath(base, captchaBackgroundPath, issued.ID)
			answer.PieceURL = mediaPath(base, captchaPiecePath, issued.ID)
		case captcha.FormAudio:
			answer.AudioURL = mediaPath(base, captchaAudioPath, issued.ID)
		}
		c.JSON(http.StatusOK, answer)
	}
}

// audioLanguage resolves the language an audio challenge is spoken in. A
// request that names one has to name a language the library pronounces; absent,
// the request's active locale decides, and a locale outside the library's set
// falls back to English.
func audioLanguage(c *gin.Context) (string, bool) {
	if requested := c.Query("lang"); requested != "" {
		return captcha.AudioLanguage(requested)
	}
	if locale, ok := i18n.FromContext(c.Request.Context()); ok {
		if language, ok := captcha.AudioLanguage(string(locale)); ok {
			return language, true
		}
	}
	return captcha.DefaultAudioLanguage, true
}

// captchaMedia serves one media object of a challenge. An id this provider did
// not draw, or one whose challenge has expired or been spent, is answered 404;
// a store that cannot answer is the outage 503.
func captchaMedia(provider *captcha.Provider, kind captcha.MediaKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		media, err := provider.Media(c.Request.Context(), c.Param("id"), kind)
		switch {
		case errors.Is(err, captcha.ErrChallengeNotFound):
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
			return
		case err != nil:
			mount.Unavailable(c)
			return
		}
		c.Data(http.StatusOK, media.ContentType, media.Body)
	}
}

// captchaInstruction is the stable key of the instruction a client shows for a
// form. The text itself belongs to the client's language resources.
func captchaInstruction(form captcha.Form) string {
	switch form {
	case captcha.FormSlider:
		return "slide-the-piece"
	case captcha.FormAudio:
		return "type-the-digits-you-hear"
	default:
		return "type-the-digits"
	}
}

// mediaPath writes the path one challenge's media is served at, on the base the
// mounter joined the route to.
func mediaPath(base, route, id string) string {
	return base + strings.Replace(route, ":id", url.PathEscape(id), 1)
}
