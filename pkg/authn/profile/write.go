package profile

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
)

type patchInput struct {
	_ struct{} `description:"The profile write; the set of fields is closed and every one is optional. A body carrying no field succeeds and changes nothing."`

	DisplayName *string           `json:"display_name" description:"The new display name, at most 100 characters; absent leaves the current one. A longer value is refused 400 by the account module."`
	Attributes  map[string]string `json:"attributes" description:"The whole attribute set to store, as string keys and string values; a given one - the empty object included - replaces the current set, and an absent one leaves it alone. A set outside the account module's bounds is refused 400."`
	Locale      *string           `json:"locale" description:"A BCP 47 language tag stored under the i18n.locale attribute. An invalid tag is refused 400; the empty string clears the attribute and an absent field leaves it alone."`
	TimeZone    *string           `json:"time_zone" description:"An IANA time-zone name stored under the i18n.tz attribute. A name outside the IANA set is refused 400; the empty string clears the attribute and an absent field leaves it alone."`
	ColorMode   *string           `json:"color_mode" description:"The color-mode preference stored under the presentation.color-mode attribute: light, dark or system. Any other value is refused 400; the empty string clears the attribute and an absent field leaves it alone."`
}

func (p *product) patchEntry() gin.HandlerFunc {
	return p.guardPatch(patchDecoded(patchOperation.Handle(p.handlePatchMe)))
}

func patchDecoded(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isPatchJSONContentType(c.GetHeader("Content-Type")) {
			mount.Fail(c, mount.CodeUnsupportedMediaType)
			return
		}
		var raw []byte
		if c.Request.Body != nil {
			body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, mount.MaxBodyBytes))
			if err != nil {
				// A body over the capability's limit is a bad request; the endpoint's own
				// limit answers 413.
				mount.Fail(c, mount.CodeBadRequest)
				return
			}
			raw = body
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		var probe patchInput
		if !mount.Decode(c, &probe) {
			return
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			raw = []byte("{}")
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		next(c)
	}
}

func isPatchJSONContentType(value string) bool {
	media, params, err := mime.ParseMediaType(value)
	if err != nil || media != "application/json" {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}

func (p *product) guardPatch(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, ok := authn.FromContext(ctx)
		if !ok || id.Account() == "" {
			mount.Unauthorized(c, p.mounter.Endpoint(), "")
			return
		}
		if id.Session() == "" {
			mount.Unauthorized(c, p.mounter.Endpoint(), "")
			return
		}
		next(c)
	}
}

func (p *product) handlePatchMe(ctx context.Context, body patchInput) (AuthnProfilePatch, error) {
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		return AuthnProfilePatch{}, p.unauthorized()
	}
	if id.Session() == "" {
		return AuthnProfilePatch{}, p.unauthorized()
	}
	locale, hasLocale := trimmed(body.Locale)
	if hasLocale && !i18n.ValidLocaleTag(locale) {
		return AuthnProfilePatch{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	zone, hasZone := trimmed(body.TimeZone)
	if hasZone && !i18n.ValidTimeZone(zone) {
		return AuthnProfilePatch{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	colorMode, hasColorMode := trimmed(body.ColorMode)
	if hasColorMode && colorMode != "" && !ValidColorMode(colorMode) {
		return AuthnProfilePatch{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	account, err := p.accounts.Get(ctx, id.Account())
	if err != nil {
		return AuthnProfilePatch{}, p.writeFailure(err)
	}
	attributes := mergeAttributes(account.Attributes, body.Attributes, hasLocale, locale, hasZone, zone, hasColorMode, colorMode)
	displayName := account.DisplayName
	if body.DisplayName != nil {
		displayName = *body.DisplayName
		if err := p.accountAdm.SetDisplayName(ctx, id.Account(), displayName); err != nil {
			return AuthnProfilePatch{}, p.writeFailure(err)
		}
	}
	attributesWritten := body.Attributes != nil || hasLocale || hasZone || hasColorMode
	if attributesWritten {
		if err := p.accountAdm.SetAttributes(ctx, id.Account(), attributes); err != nil {
			return AuthnProfilePatch{}, p.writeFailure(err)
		}
	}
	if body.DisplayName != nil || attributesWritten {
		p.auditProfile(ctx, id.Account())
	}
	return patchViewOf(displayName, attributes), nil
}

func mergeAttributes(current, given map[string]string, hasLocale bool, locale string, hasZone bool, zone string, hasColorMode bool, colorMode string) map[string]string {
	var merged map[string]string
	if given != nil {
		merged = make(map[string]string, len(given)+3)
		for k, v := range given {
			merged[k] = v
		}
	} else {
		merged = make(map[string]string, len(current)+3)
		for k, v := range current {
			merged[k] = v
		}
	}
	if hasLocale {
		setOrClear(merged, i18n.LocaleAttributeKey, locale)
	}
	if hasZone {
		setOrClear(merged, i18n.TimeZoneAttributeKey, zone)
	}
	if hasColorMode {
		setOrClear(merged, ColorModeAttributeKey, colorMode)
	}
	return merged
}

func trimmed(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return strings.TrimSpace(*v), true
}

func setOrClear(attributes map[string]string, key, value string) {
	if value == "" {
		delete(attributes, key)
		return
	}
	attributes[key] = value
}
