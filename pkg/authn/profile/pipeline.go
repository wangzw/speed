package profile

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// typePrefix starts the Problem.Type of every failure this capability's handlers
// write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// unauthorized is the 401 a handler writes itself: the gateway's own answer for
// a route that requires a credential, with the endpoint's challenge in the
// header the declared 401 response carries.
func (p *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(p.mounter.Endpoint()),
		}},
	}
}

// readFailure maps a failure a read of the profile met. The identity's own
// account gone — a tombstone, or a row that cannot be read — is the caller's
// identity no longer answering for an account, which is the same 401 an absent
// identity gets; anything else is a 503 whose cause stays in the log.
func (p *product) readFailure(err error) error {
	if errors.Is(err, authn.ErrAccountNotFound) {
		return p.unauthorized()
	}
	return p.cause("reading the profile", err)
}

// writeFailure maps a failure a write of the profile met. The identity's own
// account gone is the 401 a read of it gets; a value the account module refuses
// on shape or on its attribute bounds is a 400; anything else is a 503 whose
// cause stays in the log.
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

// cause logs a failure and returns the 503 fault, never telling the caller which
// dependency failed.
func (p *product) cause(op string, err error) error {
	p.logCause(op, err)
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

// logCause writes the Error line of a dependency failure. The cause belongs in
// the log and never in an answer.
func (p *product) logCause(op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
}
