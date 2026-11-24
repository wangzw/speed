package humanverify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/captcha"
	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

func mountChallenge(mounter mount.Mounter, provider *pow.Provider) {
	mount.Declared(mounter, challengeOperation, mountRel(challengeOperation.Path),
		challengeOperation.Handle(func(ctx context.Context, in challengeInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
			scope, ok := parseScope(in.Scope)
			if !ok {
				return nil, badRequest()
			}
			return issueChallenge(ctx, provider, scope, meta)
		}))
}

func parseScope(raw string) (authn.HumanScope, bool) {
	scope := authn.HumanScope(raw)
	switch scope {
	case authn.HumanScopePhone, authn.HumanScopeOther:
		return scope, true
	default:
		return "", false
	}
}

func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + mount.CodeBadRequest}
}

func issueChallenge(ctx context.Context, provider *pow.Provider, scope authn.HumanScope, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	challenge, err := provider.Issue(ctx, pow.IssueOptions{Scope: scope, RemoteIP: meta.ClientIP})
	if err != nil {
		return nil, answerIssue(err)
	}
	return speedhttp.JSONReply(http.StatusOK, ChallengeResponse{
		Provider:   pow.ProviderName,
		Algorithm:  pow.ProofHash,
		Challenge:  challenge.Challenge,
		Difficulty: challenge.Difficulty,
		// Whole seconds, rounded up, so the client never reads a lifetime shorter than the one the challenge was stored with.
		ExpiresIn: int((challenge.ExpiresIn + time.Second - 1) / time.Second),
	}), nil
}

func answerIssue(err error) error {
	switch {
	case errors.Is(err, pow.ErrIssuanceLimited):
		return speedhttp.Fault{Status: http.StatusTooManyRequests, Type: typePrefix + mount.CodeThrottled}
	case errors.Is(err, pow.ErrUnknownScope):
		return badRequest()
	default:
		return speedhttp.Fault{Status: http.StatusServiceUnavailable, Type: typePrefix + mount.CodeUnavailable}
	}
}

func mountRel(declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, humanverifySpec.ResolvePath(declared))
	if err != nil {
		panic(ModuleName + ": " + err.Error())
	}
	return rel
}

const (
	captchaImagePath      = "/human/captcha/image/:id"
	captchaBackgroundPath = "/human/captcha/background/:id"
	captchaPiecePath      = "/human/captcha/piece/:id"
	captchaAudioPath      = "/human/captcha/audio/:id"
)

// CaptchaChallengeResponse is the answer of the captcha challenge route: the id a proof names, the form, the instruction key a client shows and the media paths; only the chosen form's media are named.
type CaptchaChallengeResponse struct {
	_ struct{} `description:"A captcha challenge: its id, the form it takes, the instruction key a client words for that form, the media paths the form carries and how long it stays valid."`

	// Provider is the mechanism that issued the challenge.
	Provider string `json:"provider" required:"true" description:"The mechanism that issued the challenge: the constant captcha."`
	// Form is the challenge form.
	Form string `json:"form" required:"true" enum:"image,slider,audio" description:"The challenge form: image for a picture of digits, slider for a background and a piece to slide over it, audio for a spoken reading of digits."`
	// Instruction is the key of the instruction a client shows for the form.
	Instruction string `json:"instruction" required:"true" description:"A stable key naming the instruction a client shows for the form. The text itself is the client's language resource."`
	// ID is the challenge identifier a proof names.
	ID string `json:"id" required:"true" description:"The challenge id a proof names, base64url without padding."`
	// ImageURL is the path of the digit picture of an image challenge.
	ImageURL string `json:"image_url,omitempty" description:"The path of the digit picture of an image challenge."`
	// BackgroundURL is the path of the background of a slider challenge.
	BackgroundURL string `json:"background_url,omitempty" description:"The path of the background of a slider challenge."`
	// PieceURL is the path of the piece a slider challenge slides over its background.
	PieceURL string `json:"piece_url,omitempty" description:"The path of the piece a slider challenge slides over its background."`
	// AudioURL is the path of the spoken digits of an audio challenge.
	AudioURL string `json:"audio_url,omitempty" description:"The path of the spoken digits of an audio challenge."`
	// ExpiresIn is how long the challenge stays valid, in whole seconds.
	ExpiresIn int `json:"expires_in" required:"true" description:"How long the challenge stays valid, in whole seconds. A proof that names an expired challenge is refused."`
}

func mountCaptcha(mounter mount.Mounter, provider *captcha.Provider) {
	mount.Declared(mounter, captchaChallengeOperation, mountRel(captchaChallengeOperation.Path),
		captchaChallenge(mounter, provider))
	mounter.Public(http.MethodGet, captchaImagePath, captchaMedia(provider, captcha.MediaImage))
	mounter.Public(http.MethodGet, captchaBackgroundPath, captchaMedia(provider, captcha.MediaBackground))
	mounter.Public(http.MethodGet, captchaPiecePath, captchaMedia(provider, captcha.MediaPiece))
	mounter.Public(http.MethodGet, captchaAudioPath, captchaMedia(provider, captcha.MediaAudio))
}

func captchaChallenge(mounter mount.Mounter, provider *captcha.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		scope, ok := parseScope(c.Query("scope"))
		if !ok {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
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
			Scope:    scope,
			RemoteIP: c.ClientIP(),
		})
		switch {
		case errors.Is(err, captcha.ErrUnknownScope),
			errors.Is(err, captcha.ErrUnknownForm),
			errors.Is(err, captcha.ErrUnknownAudioLanguage):
			mount.Fail(c, mount.CodeBadRequest)
			return
		case errors.Is(err, captcha.ErrIssuanceLimited):
			mount.Throttled(c, captcha.IssueWindow)
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

func mediaPath(base, route, id string) string {
	return base + strings.Replace(route, ":id", url.PathEscape(id), 1)
}
