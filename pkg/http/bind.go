package http

import (
	"errors"
	nethttp "net/http"

	"github.com/gin-gonic/gin"
)

// BindJSON binds the request's body to dst as JSON and then lets dst check itself
// when it has a Validate method. It reports true on success; on failure it writes a
// Problem through Fail — 413 for a body over the limit, 422 for a failed Validate,
// 400 otherwise — and reports false.
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
