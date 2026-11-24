package profile

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const typePrefix = "urn:speed:authn:"

func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (p *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(p.mounter.Endpoint()),
		}},
	}
}

func (p *product) readFailure(err error) error {
	if errors.Is(err, authn.ErrAccountNotFound) {
		return p.unauthorized()
	}
	return p.cause("reading the profile", err)
}

func (p *product) writeFailure(err error) error {
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return p.unauthorized()
	case errors.Is(err, authn.ErrInvalidArgument):
		return authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	default:
		return p.cause("updating the profile", err)
	}
}

func (p *product) cause(op string, err error) error {
	p.logCause(op, err)
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

func (p *product) logCause(op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
}
