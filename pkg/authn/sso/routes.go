package sso

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// discoverPath is the route the domain discovery answers on, joined to the
// mounter's base path.
const discoverPath = "/sso/discover"

// mountRoutes registers the discovery route through the mounter as a public one:
// it answers which sign-in entries exist for an email's domain and nothing about
// whether an account does.
func (p *product) mountRoutes() {
	p.mounter.Public(http.MethodPost, discoverPath, p.handleDiscover)
}

// discoverRequest is the request body. Only the email is read, and only its
// domain is used.
type discoverRequest struct {
	Email string `json:"email"`
}

// discoverResponse is the answer, always of the same shape: an empty providers
// list when nothing matches.
type discoverResponse struct {
	Providers []discoverProvider `json:"providers"`
}

// discoverProvider is one entry: what is public about a provider.
type discoverProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

// handleDiscover answers the sign-in entries for an email's domain. The local
// part is never looked at, so the answer does not leak whether an account
// exists; a request whose domain cannot be reduced to a domain is a 400, and one
// whose domain no provider has verified is the same empty list as one that has
// providers disabled.
func (p *product) handleDiscover(c *gin.Context) {
	var req discoverRequest
	if !mount.Decode(c, &req) {
		return
	}
	providers, err := p.Discover(c.Request.Context(), emailDomain(req.Email))
	if err != nil {
		if errors.Is(err, errInvalidDomain) {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		mount.Unavailable(c)
		return
	}
	out := discoverResponse{Providers: make([]discoverProvider, 0, len(providers))}
	for _, prov := range providers {
		out.Providers = append(out.Providers, discoverProvider{ID: prov.ID, Name: prov.Name, Protocol: prov.Protocol})
	}
	c.JSON(http.StatusOK, out)
}

// emailDomain is the domain of an email address: everything after the last at
// sign. An address with no at sign has no domain and returns the empty string,
// which the discovery refuses as not a domain.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}
