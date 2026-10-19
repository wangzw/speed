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

// patchInput is the request of PATCH B/me. Every field is optional: a field left
// out is left as it is, and an unknown field is refused. display_name, locale
// and time_zone are pointers so that a field left out is told apart from one
// given empty; attributes is a map, so a given one — the empty map included —
// replaces the whole set and an absent one leaves it alone. The declaration
// reflects the type, so the descriptions below are the body's published schema.
type patchInput struct {
	_ struct{} `description:"The profile write; the set of fields is closed and every one is optional. A body carrying no field succeeds and changes nothing."`

	DisplayName *string           `json:"display_name" description:"The new display name, at most 100 characters; absent leaves the current one. A longer value is refused 400 by the account module."`
	Attributes  map[string]string `json:"attributes" description:"The whole attribute set to store, as string keys and string values; a given one - the empty object included - replaces the current set, and an absent one leaves it alone. A set outside the account module's bounds is refused 400."`
	Locale      *string           `json:"locale" description:"A BCP 47 language tag stored under the i18n.locale attribute. An invalid tag is refused 400; the empty string clears the attribute and an absent field leaves it alone."`
	TimeZone    *string           `json:"time_zone" description:"An IANA time-zone name stored under the i18n.tz attribute. A name outside the IANA set is refused 400; the empty string clears the attribute and an absent field leaves it alone."`
}

// patchEntry is the complete handler of the write route: the checks below, the
// capability's decode rule, then the declared handler that binds the body and
// answers. The entry point binds the declared input before it calls a handler,
// so the checks that the order of this route puts in front of the body — the
// identity, that it is not impersonating, that it has a login session, and the
// freshness the account's own factors call for — run in front of the bound
// handler instead.
func (p *product) patchEntry() gin.HandlerFunc {
	return p.guardPatch(patchDecoded(patchOperation.Handle(p.handlePatchMe)))
}

// patchDecoded applies the capability's decode rule and puts a body the entry
// point can bind back on the request: application/json with no parameter or
// with charset=utf-8 as its only one, a body no larger than mount.MaxBodyBytes,
// a single JSON value the declaration can hold, no member the declaration does
// not carry, and nothing after the value. An empty body is refused; a literal
// null is the zero value the old decoder read and is put back as the empty
// object that stands for it.
//
// The entry point binds the same body again after this check and reads it from
// the copy put back. Its own refusals carry the problem types the operation
// declares; this wrapper answers the capability's rule itself, including the
// 64 KiB capability limit, which the endpoint's larger limit does not express.
// A request over the endpoint's limit is the entry point's 413, which is a
// platform-wide answer; that is why the write route declares 413.
func patchDecoded(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The old decoder checked the media type before it read anything, so a
		// body of any size with the wrong type is the 415 first.
		if !isPatchJSONContentType(c.GetHeader("Content-Type")) {
			mount.Fail(c, mount.CodeUnsupportedMediaType)
			return
		}
		var raw []byte
		if c.Request.Body != nil {
			body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, mount.MaxBodyBytes))
			if err != nil {
				// A body over the capability's limit is a bad request, the same
				// answer a body the decoder cannot read gets; the endpoint's own
				// limit is the one that answers 413.
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

// isPatchJSONContentType reports whether a Content-Type value is
// application/json, with no parameter or with charset=utf-8 as its only one.
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

// guardPatch refuses a request the write route must not read a body for. An
// answer of its own is written through the capability's own writers, so a 401
// carries the endpoint's challenge and the step-up carries the requirement the
// account's factors ask for.
func (p *product) guardPatch(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, ok := authn.FromContext(ctx)
		if !ok || id.Account() == "" {
			mount.Unauthorized(c, p.mounter.Endpoint(), "")
			return
		}
		if id.Impersonating() {
			mount.Forbidden(c, mount.CodeImpersonationRestricted)
			return
		}
		if id.Session() == "" {
			mount.Unauthorized(c, p.mounter.Endpoint(), "")
			return
		}
		if !p.admitStrength(c, id) {
			return
		}
		next(c)
	}
}

// handlePatchMe answers PATCH B/me: it writes the caller's own display name, its
// whole attribute set, and the language and time zone named inside that set. The
// writable set is closed; the account's status, its identifiers and its external
// bindings are not writable here and no field of this body reaches them.
//
// The order is fixed: the checks guardPatch makes before the body is read, the
// body's shape and the two value domains, the account read, the writes, the
// best-effort audit row, the answer. The identity, impersonation and session
// conditions are checked again here as the fallback for a registration that did
// not go through patchEntry. The two writes are not one atomic change: when the
// display name lands and the attribute set does not, the display name's change is
// in force and the answer is the failure's.
func (p *product) handlePatchMe(ctx context.Context, body patchInput) (AuthnProfilePatch, error) {
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		return AuthnProfilePatch{}, p.unauthorized()
	}
	if id.Impersonating() {
		return AuthnProfilePatch{}, authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
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
	account, err := p.accounts.Get(ctx, id.Account())
	if err != nil {
		return AuthnProfilePatch{}, p.writeFailure(err)
	}
	attributes := mergeAttributes(account.Attributes, body.Attributes, hasLocale, locale, hasZone, zone)
	displayName := account.DisplayName
	if body.DisplayName != nil {
		displayName = *body.DisplayName
		if err := p.accountAdm.SetDisplayName(ctx, id.Account(), displayName); err != nil {
			return AuthnProfilePatch{}, p.writeFailure(err)
		}
	}
	attributesWritten := body.Attributes != nil || hasLocale || hasZone
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

// mergeAttributes composes the attribute set one SetAttributes writes: a given
// set replaces the whole of the current one, and a given locale or time zone
// then overwrites its key. An empty locale or time zone clears the key. The
// merge is "read the current, merge, write the whole back": two concurrent
// writes that change different keys can lose one of them, and the loss is
// bounded to the concurrent pair.
func mergeAttributes(current, given map[string]string, hasLocale bool, locale string, hasZone bool, zone string) map[string]string {
	var merged map[string]string
	if given != nil {
		merged = make(map[string]string, len(given)+2)
		for k, v := range given {
			merged[k] = v
		}
	} else {
		merged = make(map[string]string, len(current)+2)
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
	return merged
}

// trimmed returns the string a body field named, and whether the field was given
// at all. A pointer that is nil is a field left out; a pointer to a value is the
// value, trimmed of surrounding space.
func trimmed(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return strings.TrimSpace(*v), true
}

// setOrClear writes value under key, or removes the key when value is empty.
func setOrClear(attributes map[string]string, key, value string) {
	if value == "" {
		delete(attributes, key)
		return
	}
	attributes[key] = value
}
