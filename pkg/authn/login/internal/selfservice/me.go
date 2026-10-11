package selfservice

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// profile is the answer of GET /me.
type profile struct {
	AccountID   string `json:"account_id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	MFARequired bool   `json:"mfa_required"`
	PasswordSet bool   `json:"password_set"`
	CreatedAt   string `json:"created_at"`

	Identifiers []identifierView `json:"identifiers"`
	Links       []linkView       `json:"links"`
}

// identifierView is one identifier of the account.
type identifierView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	VerifiedAt  string `json:"verified_at"`
	VerifiedVia string `json:"verified_via"`
}

// linkView is one external binding of the account: the identifier to unlink it
// by, the provider, the address the provider last declared, for display, and the
// two instants. It has no field for the subject, which is the provider's own
// name for the person and no part of what the account's holder is shown.
type linkView struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Email       string `json:"email,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`
}

// me answers GET /me with the caller's own profile. It is the one route that an
// impersonating identity may reach, and it then reads the impersonated account.
func (r *routes) me(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		mount.Unauthorized(c, r.endpoint, "")
		return
	}
	account, err := r.deps.Accounts.Get(ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return
	}
	identifiers, err := r.deps.Accounts.Identifiers(ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return
	}
	links, err := r.deps.Accounts.Externals(ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return
	}
	out := profile{
		AccountID:   string(account.ID),
		Kind:        string(account.Kind),
		DisplayName: account.DisplayName,
		MFARequired: account.MFARequired,
		PasswordSet: account.PasswordSet,
		CreatedAt:   instant(account.CreatedAt),
		Identifiers: make([]identifierView, 0, len(identifiers)),
		Links:       make([]linkView, 0, len(links)),
	}
	for _, i := range identifiers {
		out.Identifiers = append(out.Identifiers, identifierView{
			ID:          i.ID,
			Kind:        string(i.Kind),
			Value:       i.Value,
			VerifiedAt:  instant(i.VerifiedAt),
			VerifiedVia: i.VerifiedVia,
		})
	}
	for _, l := range links {
		out.Links = append(out.Links, linkView{
			ID:          l.ID,
			Provider:    l.Provider,
			Email:       l.Email,
			CreatedAt:   instant(l.CreatedAt),
			LastLoginAt: instant(l.LastLoginAt),
		})
	}
	c.JSON(http.StatusOK, out)
}

// instant is t as RFC 3339 in UTC, and the empty string for the zero time.
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
