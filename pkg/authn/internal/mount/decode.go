package mount

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes is the most a JSON request body of this capability may be: 64
// KiB. The SAML assertion consumer is the one route with a larger limit, and it
// reads a form and not JSON.
const MaxBodyBytes = 64 << 10

// Decode reads the request body into dst and says whether it did. On false the
// problem is already written, with the chain ended, and the handler returns.
//
// Only application/json is taken, with no parameter or with charset=utf-8, and
// any other type is a 415 unsupported-media-type. A body over MaxBodyBytes, one
// with a field dst does not have, one that is empty or not JSON, and one with
// anything after the value other than white space are a 400 bad-request. The
// answer names no cause, and the body is never read past the limit.
func Decode(c *gin.Context, dst any) bool {
	if !isJSON(c.GetHeader("Content-Type")) {
		Fail(c, CodeUnsupportedMediaType)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		Fail(c, CodeBadRequest)
		return false
	}
	// Anything but the end of the body after the value is trailing data, and a
	// second value in it must not be taken for the end of the first.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		Fail(c, CodeBadRequest)
		return false
	}
	return true
}

// isJSON reports whether a Content-Type value is application/json, with no
// parameter or with charset=utf-8 as its only one.
func isJSON(value string) bool {
	media, params, err := mime.ParseMediaType(value)
	if err != nil || media != "application/json" {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}
