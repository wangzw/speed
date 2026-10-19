package mount

import (
	"bytes"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GuardBody applies the capability's request-body rules before the entry point
// binds a route's declared body: the media type must be application/json, the
// before hook may refuse the request before any of the body is read, and the
// body is read under MaxBodyBytes. The bytes read are put back, so the entry
// point binds the same body afterwards with its own unknown-member check.
//
// before reports whether the request may go on. A hook that refuses writes its
// own answer and returns false; nil is a route with no check of its own.
//
// A body over MaxBodyBytes, and a body that cannot be read, are answered 400
// bad-request: the design groups a body over the capability's limit with one
// that does not parse, and does not give the limit a code of its own. The
// endpoint's own limit is what answers 413, before a route runs.
func GuardBody(before func(*gin.Context) bool, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isJSON(c.GetHeader("Content-Type")) {
			Fail(c, CodeUnsupportedMediaType)
			return
		}
		if before != nil && !before(c) {
			return
		}
		if c.Request.Body == nil {
			next(c)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes))
		if err != nil {
			Fail(c, CodeBadRequest)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		next(c)
	}
}
