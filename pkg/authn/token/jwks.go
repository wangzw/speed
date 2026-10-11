package token

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// jwksPath is the route the public key set is published on, joined to the
// mounter's base path.
const jwksPath = "/jwks.json"

// The headers of the key set. Its public cache is the one response of the
// capability without no-store: the body holds no secret and changes only with the
// keys loaded at startup, so a public cache is free, and its lifetime is the
// wait a key rotation counts on before it switches the signing key.
const (
	jwksContentType  = "application/jwk-set+json"
	jwksCacheControl = "public, max-age=300"
)

// JWKS returns the public keys that verify the tokens, as a JWK set. Every key is
// the public half with its kid, its algorithm and the signature use; no private
// field is included.
func (p *product) JWKS(_ context.Context) ([]byte, error) {
	return marshalJWKS(p.keys.entries)
}

// mountJWKS registers the key set on its public route. It is the only route this
// piece of the module mounts; the token and revocation endpoints are registered
// by the piece that builds them.
func (p *product) mountJWKS() {
	p.mounter.Public(http.MethodGet, jwksPath, p.handleJWKS)
}

// handleJWKS writes the key set with its own cache headers, over the no-store the
// mounter's wrapper put on the response, as the design says the key set is the
// one response allowed a public cache. The body is produced from the startup key
// set and cannot fail for a request.
func (p *product) handleJWKS(c *gin.Context) {
	body, err := p.JWKS(c.Request.Context())
	if err != nil {
		p.logger.Error(diagnosticPrefix+ModuleName+": writing the key set", slog.Any("error", err))
		mount.Unavailable(c)
		return
	}
	c.Header("Cache-Control", jwksCacheControl)
	c.Data(http.StatusOK, jwksContentType, body)
}
