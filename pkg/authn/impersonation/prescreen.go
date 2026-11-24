package impersonation

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func prescreenStart(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var probe startInput
		if !mount.Decode(c, &probe) {
			return
		}
		// Re-encode rather than replay: encoding/json matches member names
		// without regard to case and the entry point's binder does not.
		encoded, err := json.Marshal(probe)
		if err != nil {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(encoded))
		c.Request.ContentLength = int64(len(encoded))
		next(c)
	}
}
