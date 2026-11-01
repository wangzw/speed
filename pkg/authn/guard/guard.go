// Package guard is the step-up middleware of the authentication capability: a
// handler mounted in front of a route's own handler admits the request only when
// the identity the gateway stamped on it meets a requirement, and otherwise ends
// it with the 401 that says the authentication was not strong or recent enough.
// A request that carries no identity at all is answered with the entry point's
// untyped 401 instead — no problem type, the same shape the authorization stage
// writes for that condition — so a layer charging a source by outcome does not
// exempt it as a handshake.
//
// Require returns the middleware. A host or a permission module mounts it on a
// route of its own, after the gateway, so that the identity is on the request
// already, and the requirement is what that route wants of a caller — a
// multi-factor level, a fresh one, or one proven here rather than declared by an
// IdP. A request that falls short is answered with RFC 9470's
// insufficient_user_authentication: the root package's Identity.Satisfies is the
// whole of the decision, and this package adds the answer and nothing to it.
//
// The package registers no module. It is a plain gin middleware, so its presence
// in an assembly is a call site and not a module name, and its absence is a route
// that asks for no step-up rather than a startup failure. It imports the root
// package, the HTTP entry point's failure shape and gin, and imports no factor
// module and no other package of this capability: it decides from the identity's
// methods, assurance and authentication time alone, never from a factor and never
// from a store, so no package of this capability imports it and it pulls
// nothing into an assembly.
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

// challengeHeader is the response header the 401 carries. RFC 9110 requires a
// 401 to name at least one challenge.
const challengeHeader = "WWW-Authenticate"

// typePrefix starts the Problem.Type of the refusal; the machine-readable code
// below follows it, the way every handler of this capability writes one.
const typePrefix = "urn:speed:authn:"

// insufficientUserAuthentication is the problem code of the refusal and the
// error code of its challenge. It and the two parameters below are RFC 9470's;
// the RFC defines them inside a Bearer challenge, and carrying them on a Cookie
// or a Basic carrier is this capability's convention and not the RFC's.
const insufficientUserAuthentication = "insufficient_user_authentication"

// stepUpDescription is the challenge's error_description. It states what the
// client has to do and names no requirement of the route and nothing of the
// account: which level and how fresh is in acr_values and max_age, and telling
// more would describe an account the caller holds no credential for.
const stepUpDescription = "A stronger or more recent authentication is required"

// Require returns a middleware that lets the request reach the next handler only
// when the identity the gateway put in its context meets req, and refuses
// otherwise.
//
// The identity is read from the request's context and the decision is
// Identity.Satisfies at the moment of the request. A request with no identity is
// refused with the entry point's untyped 401, the answer the gateway and the
// other layers write for the same condition: an absent identity is not a caller
// that met nothing, and this is the part of the middleware that stands on its
// own if it is ever mounted ahead of the gateway. Only an identity that is
// present and falls short of the requirement is answered with the typed step-up
// 401. The distinction matters to a layer that charges a source by outcome: the
// untyped 401 is a refused credential and is charged as one, while the typed
// answer is a handshake and is exempt. The request goes on untouched when the
// requirement holds.
//
// The middleware imports no store and takes no dependency, so it works in an
// assembly that took up nothing, and its failure direction is closed: it refuses
// unless the identity is there and satisfies the requirement.
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

// refuseAnonymous writes the 401 of a request that carries no identity at all.
// It is the entry point's untyped answer — the same shape the authorization
// stage writes for the same condition — with both carriers named in the
// challenge and no problem type. The middleware knows no endpoint name, so the
// realm is empty; what it owes a caller is a challenge it can act on. The absent
// type is what keeps this answer out of the step-up mark: a layer charging a
// source by outcome charges this 401 like the refused credential it is rather
// than exempting it as a handshake.
func refuseAnonymous(c *gin.Context) {
	c.Header(challengeHeader, `Bearer realm="", Cookie realm=""`)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusUnauthorized})
}

// refuse writes the step-up 401 for an identity that does not meet req. The
// challenge names the scheme the identity authenticated with — what a client
// re-shows to step up — and then the level the route wants as acr_values, the
// number of the assurance, and, when the requirement asks for freshness,
// max_age in whole seconds. A level only an IdP declared still counts when the
// requirement is not Local, because the decision is Identity.Satisfies's and
// not this package's. A request that carried no identity never reaches this
// function; refuseAnonymous answers it.
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

// seconds is d in whole seconds, rounded up, so that a max_age of any
// sub-second part is not written as zero.
func seconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}

// challengeScheme returns scheme when it is an RFC 9110 token, and Bearer
// otherwise. The gateway stamps Identity.Challenge from the credential's carrier,
// so an odd value is a zero identity or a defect, and neither may put a character
// into the field that ends it or starts another: a 401 with no scheme is one that
// asks for no credential, which RFC 9110 does not allow.
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
