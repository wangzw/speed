package profile

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
)

// patchBody is the request of PATCH B/me. Every field is optional: a field left
// out is left as it is, and an unknown field is refused. display_name, locale
// and time_zone are pointers so that a field left out is told apart from one
// given empty; attributes is a map, so a given one — the empty map included —
// replaces the whole set and an absent one leaves it alone.
type patchBody struct {
	DisplayName *string           `json:"display_name"`
	Attributes  map[string]string `json:"attributes"`
	Locale      *string           `json:"locale"`
	TimeZone    *string           `json:"time_zone"`
}

// handlePatchMe answers PATCH B/me: it writes the caller's own display name, its
// whole attribute set, and the language and time zone named inside that set. The
// writable set is closed; the account's status, its identifiers and its external
// bindings are not writable here and no field of this body reaches them.
//
// The order is fixed: the identity, that it is not impersonating, that it has a
// login session, the freshness the account's factors call for, the body's shape
// and the two value domains, the account read, the writes, the best-effort audit
// row, the answer. The two writes are not one atomic change: when the display
// name lands and the attribute set does not, the display name's change is in
// force and the answer is the failure's.
func (p *product) handlePatchMe(c *gin.Context) {
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
	var body patchBody
	if !mount.Decode(c, &body) {
		return
	}
	locale, hasLocale := trimmed(body.Locale)
	if hasLocale && !i18n.ValidLocaleTag(locale) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	zone, hasZone := trimmed(body.TimeZone)
	if hasZone && !i18n.ValidTimeZone(zone) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	account, err := p.accounts.Get(ctx, id.Account())
	if err != nil {
		p.writeFailure(c, err)
		return
	}
	attributes := mergeAttributes(account.Attributes, body.Attributes, hasLocale, locale, hasZone, zone)
	displayName := account.DisplayName
	if body.DisplayName != nil {
		displayName = *body.DisplayName
		if err := p.accountAdm.SetDisplayName(ctx, id.Account(), displayName); err != nil {
			p.writeFailure(c, err)
			return
		}
	}
	attributesWritten := body.Attributes != nil || hasLocale || hasZone
	if attributesWritten {
		if err := p.accountAdm.SetAttributes(ctx, id.Account(), attributes); err != nil {
			p.writeFailure(c, err)
			return
		}
	}
	if body.DisplayName != nil || attributesWritten {
		p.auditProfile(ctx, id.Account())
	}
	c.JSON(http.StatusOK, patchViewOf(displayName, attributes))
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
