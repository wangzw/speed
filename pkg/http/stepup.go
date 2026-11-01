package http

import "github.com/gin-gonic/gin"

// stepUpKey is the gin context key MarkStepUp writes the marker under.
const stepUpKey = "http:step-up"

// TypeStepUp is the problem type of the answer that asks a caller for a
// stronger or more recent authentication. Every path that writes the step-up
// 401 — the mount helpers, the gateway's middleware, the tenant admission
// layer and a typed handler's fault — writes this same type, so the response
// writer recognises the answer by it.
const TypeStepUp = "urn:speed:authn:insufficient_user_authentication"

// MarkStepUp records that this request is answered with a 401 that asks for a
// stronger or more recent authentication, not one that refused a credential.
//
// The two answers share a status and mean different things: a source that keeps
// failing authentication is spending guesses, while a source asked to step up
// has already authenticated and is being told to prove it again. A layer that
// charges a source by outcome has to tell them apart, and the status alone
// cannot. The marker is written only by a writer of the step-up answer and read
// only by such a layer; it means nothing on any other status.
//
// Fail writes it for every 401 that carries TypeStepUp, so a writer does not
// call this itself: the answer is recognised where it leaves the process, and
// a path added later is covered without knowing about this marker.
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
