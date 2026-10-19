// Package stepup builds the WWW-Authenticate value of a step-up refusal for a
// typed handler.
//
// The mount package writes that answer for a gin handler (mount.StepUpRequired),
// but its value builder is not exported and a typed handler has no response
// writer to hand it. This package is the one reading of the rule inside
// authn.login: the engine and the account self-service routes both return the
// refusal as a fault, and the fault's header is this value.
package stepup

import (
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// description is the error_description of the challenge. It names no
// requirement of the route and nothing of the account.
const description = "A stronger or more recent authentication is required"

// Challenge is the WWW-Authenticate value of a step-up refusal: the scheme the
// identity authenticated with, then the error, its description, the level's
// number in acr_values and, when the requirement carries one, its MaxAge in
// whole seconds, rounded up.
func Challenge(id authn.Identity, r authn.Requirement) string {
	var b strings.Builder
	b.WriteString(scheme(id.Challenge()))
	b.WriteString(` error="insufficient_user_authentication", error_description="`)
	b.WriteString(description)
	b.WriteString(`", acr_values="`)
	b.WriteString(strconv.Itoa(int(r.Assurance)))
	b.WriteByte('"')
	if r.MaxAge > 0 {
		b.WriteString(`, max_age="`)
		b.WriteString(strconv.FormatInt(int64((r.MaxAge+time.Second-1)/time.Second), 10))
		b.WriteByte('"')
	}
	return b.String()
}

// Type is the problem type of the refusal.
func Type() string { return "urn:speed:authn:" + mount.CodeInsufficientUserAuthentication }

// scheme returns s when it is an RFC 9110 token, and Bearer otherwise. The
// gateway stamps Identity.Challenge, so an odd value is a zero identity or a
// defect, and neither may put a character into a header that ends the field.
func scheme(s string) string {
	if s == "" {
		return "Bearer"
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return "Bearer"
		}
	}
	return s
}
