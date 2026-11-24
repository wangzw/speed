// Package guard is the step-up middleware: a handler in front of a route admits the request only when the gateway's identity meets a requirement, and otherwise answers the 401 that says it did not.
package guard

import (
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const challengeHeader = "WWW-Authenticate"

const typePrefix = "urn:speed:authn:"

const insufficientUserAuthentication = "insufficient_user_authentication"

const stepUpDescription = "A stronger or more recent authentication is required"

// Require returns a middleware that admits a request whose identity meets req and refuses otherwise; a request with no identity is refused too.
func Require(req authn.Requirement) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := authn.FromContext(c.Request.Context())
		if !ok {
			refuseAnonymous(c)
			return
		}
		if id.Satisfies(req, time.Now()) {
			c.Next()
			return
		}
		refuse(c, id, req)
	}
}

func refuseAnonymous(c *gin.Context) {
	c.Header(challengeHeader, `Bearer realm="", Cookie realm=""`)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusUnauthorized})
}

func refuse(c *gin.Context, id authn.Identity, req authn.Requirement) {
	var b strings.Builder
	b.WriteString(challengeScheme(id.Challenge()))
	b.WriteString(` error="`)
	b.WriteString(insufficientUserAuthentication)
	b.WriteString(`", error_description="`)
	b.WriteString(stepUpDescription)
	b.WriteString(`", acr_values="`)
	b.WriteString(strconv.Itoa(int(req.Assurance)))
	b.WriteByte('"')
	if req.MaxAge > 0 {
		b.WriteString(`, max_age="`)
		b.WriteString(strconv.FormatInt(seconds(req.MaxAge), 10))
		b.WriteByte('"')
	}
	c.Header(challengeHeader, b.String())
	speedhttp.Fail(c, speedhttp.Problem{
		Status: nethttp.StatusUnauthorized,
		Type:   typePrefix + insufficientUserAuthentication,
	})
}

func seconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}

func challengeScheme(scheme string) string {
	if scheme == "" {
		return "Bearer"
	}
	for i := 0; i < len(scheme); i++ {
		ch := scheme[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return "Bearer"
		}
	}
	return scheme
}
