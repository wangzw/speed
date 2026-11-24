package token

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The key set's public cache is the one response without no-store: the body
// holds no secret and changes only with the keys loaded at startup, so its
// lifetime is the wait a key rotation counts on.
const (
	jwksContentType  = "application/jwk-set+json"
	jwksCacheControl = "public, max-age=300"
)

const unavailableType = "urn:speed:authn:" + mount.CodeUnavailable

// JWKS returns the public keys that verify the tokens, as a JWK set. Every key is
// the public half with its kid, its algorithm and the signature use; no private
// field is included.
func (p *product) JWKS(_ context.Context) ([]byte, error) {
	return marshalJWKS(p.keys.entries)
}

func (p *product) mountJWKS() {
	mount.Declared(p.mounter, jwksOperation, jwksPath, jwksOperation.Handle(p.handleJWKS))
}

// handleJWKS writes the key set with the media type and public cache the
// contract declares, over the wrapper's no-store. The body is produced from the
// startup key set and cannot fail for a request.
func (p *product) handleJWKS(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	body, err := p.JWKS(ctx)
	if err != nil {
		p.logger.Error(diagnosticPrefix+ModuleName+": writing the key set", slog.Any("error", err))
		return nil, speedhttp.Fault{
			Status: http.StatusServiceUnavailable,
			Type:   unavailableType,
		}
	}
	return speedhttp.BytesReply(http.StatusOK, jwksContentType, body), nil
}
