package http

import "github.com/gin-gonic/gin"

const stepUpKey = "http:step-up"

// TypeStepUp is the problem type of the answer that asks a caller for a stronger or
// more recent authentication.
const TypeStepUp = "urn:speed:authn:insufficient_user_authentication"

// MarkStepUp records that this request is answered with a 401 that asks for a stronger
// or more recent authentication, not one that refused a credential.
func MarkStepUp(c *gin.Context) {
	c.Set(stepUpKey, true)
}

// IsStepUp reports whether the answer written for this request was marked by
// MarkStepUp.
func IsStepUp(c *gin.Context) bool {
	marked, ok := c.Get(stepUpKey)
	if !ok {
		return false
	}
	set, _ := marked.(bool)
	return set
}
