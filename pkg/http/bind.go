package http

import (
	"errors"
	nethttp "net/http"

	"github.com/gin-gonic/gin"
)

// BindJSON binds the request's body to dst as JSON, then lets dst check itself
// when it has a Validate method. It answers a failure itself and reports false;
// a body it read and dst accepted is reported true.
//
// The failures are told apart by what went wrong rather than answered with one
// status. A body past the endpoint's limit is a 413; one that is not a JSON
// value dst can hold — empty, malformed, a member of the wrong type — is a 400;
// one that binds but fails dst's own Validate is a 422. The first two say the
// bytes cannot be read as this shape, the third says the shape is right and a
// value in it is not allowed.
//
// Every refusal goes out through Fail, so it carries the entry point's error
// shape: the delivered ErrorResponder's, or the default problem details object
// with the failure's own text as its detail. Fail ends the chain, so a caller
// does not run on after a refusal it did not ask for.
//
// The body limit is the endpoint's max-body-bytes, imposed by the body limit
// layer for every request. A request that declares an over-limit length is
// refused by that layer before any handler runs and never reaches this; what
// reaches it is a body of unknown length, whose read ends with an
// *nethttp.MaxBytesError, and that is the 413 here.
func BindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		status := nethttp.StatusBadRequest
		var tooLarge *nethttp.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = nethttp.StatusRequestEntityTooLarge
		}
		Fail(c, Problem{Status: status, Detail: err.Error()})
		return false
	}
	if validator, ok := dst.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			Fail(c, Problem{Status: nethttp.StatusUnprocessableEntity, Detail: err.Error()})
			return false
		}
	}
	return true
}
