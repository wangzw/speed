package impersonation

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// prescreenStart applies the body rule the start route carried before it was
// declared as an operation, before the entry point binds the declared input:
// application/json with no parameter or with charset=utf-8 as its only one, a
// body no larger than mount.MaxBodyBytes, one JSON value the declaration can
// hold, no member the declaration does not carry, and nothing after the value.
// The entry point's binder bounds the body by the endpoint's limit and matches
// members by exact name, so this route applies the capability's own rule itself;
// a refusal is answered with the capability's problem code and reaches no
// handler.
//
// The decoded value is put back as JSON under the declared member names.
// encoding/json matches a member to a field without regard to case, so a body
// such as {"Account":"..."} was bound before the route was declared; the entry
// point's binder looks members up by exact name, and re-encoding what the
// capability's decoder accepted keeps that body bindable. The handler still
// binds the input from this body, so the value it sees is the one the
// capability's rule accepted.
func prescreenStart(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var probe startInput
		if !mount.Decode(c, &probe) {
			return
		}
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
