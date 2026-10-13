package selfservice

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
)

// preferencesBody is the request of PATCH /me/preferences. A field left out is
// left unchanged; a field given as the empty string clears the stored value.
type preferencesBody struct {
	Locale   *string `json:"locale"`
	Timezone *string `json:"time_zone"`
}

// preferencesView is the answer: the two values now stored, each empty when none
// is.
type preferencesView struct {
	Locale   string `json:"locale"`
	Timezone string `json:"time_zone"`
}

// updatePreferences answers PATCH /me/preferences, storing the caller's language
// and time zone in its account's attribute set under the keys the i18n
// capability reads.
//
// It goes through the same guard as the other routes that write to the account:
// a fresh authentication is asked for and an impersonating identity is refused,
// so a preference is written only by the account's holder acting on its own
// behalf.
//
// The write is a read, merge and replace of the whole attribute set, so two
// concurrent changes to different keys can lose one of them; a preference is
// last-write-wins and the loss is bounded to the concurrent pair.
func (r *routes) updatePreferences(c *gin.Context) {
	who, ok := r.authorize(c, "")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var in preferencesBody
	if err := c.ShouldBindJSON(&in); err != nil {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	locale, hasLocale := trimmed(in.Locale)
	zone, hasZone := trimmed(in.Timezone)
	if hasLocale && !i18n.ValidLocaleTag(locale) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if hasZone && !i18n.ValidTimeZone(zone) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	account, err := r.deps.Accounts.Get(ctx, who.account)
	if err != nil {
		r.fail(c, err)
		return
	}
	attrs := make(map[string]string, len(account.Attributes)+2)
	for k, v := range account.Attributes {
		attrs[k] = v
	}
	if hasLocale {
		setOrClear(attrs, i18n.LocaleAttributeKey, locale)
	}
	if hasZone {
		setOrClear(attrs, i18n.TimeZoneAttributeKey, zone)
	}
	if err := r.deps.Admin.SetAttributes(ctx, who.account, attrs); err != nil {
		r.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, preferencesView{
		Locale:   attrs[i18n.LocaleAttributeKey],
		Timezone: attrs[i18n.TimeZoneAttributeKey],
	})
}

// trimmed returns the string a body field named, and whether the field was
// given at all. A pointer that is nil is a field left out; a pointer to a value
// is the value, trimmed of surrounding space.
func trimmed(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return strings.TrimSpace(*v), true
}

// setOrClear writes value under key, or removes the key when value is empty.
func setOrClear(attrs map[string]string, key, value string) {
	if value == "" {
		delete(attrs, key)
		return
	}
	attrs[key] = value
}
