package admission

import nethttp "net/http"

// outcome is what the layer concluded about one request. The set is closed and
// each member is one value of the metric attribute of the same name, so a
// dashboard or an alert written against a value stops matching when one is
// renamed. A value can be added and none is renamed.
type outcome int

const (
	// admitted lets the request through: it is not about a tenant, it is
	// allowed to be anonymous, its account is a member and meets the
	// tenant's requirements, or it is an impersonation or a service account
	// whose member check held.
	admitted outcome = iota
	// admittedByPolicy lets a non-member through because the permission
	// model said so.
	admittedByPolicy
	// notMember refuses a non-member: 404.
	notMember
	// scopeMismatch refuses an impersonation that is confined to another
	// tenant, or one that reached a route with no tenant: 404.
	scopeMismatch
	// stepUpMFA asks for multi-factor authentication: 401.
	stepUpMFA
	// stepUpSSO asks for a sign-in through the tenant's own identity
	// provider: 401.
	stepUpSSO
	// noIdentity refuses a request with no identity on a route that needs
	// one: 401.
	noIdentity
	// unavailable refuses a request the layer could not decide because
	// something it reads failed: 503.
	unavailable

	outcomeCount
)

// names are the values of the outcome attribute, indexed by outcome.
var names = [outcomeCount]string{
	admitted:         "admitted",
	admittedByPolicy: "admitted-by-policy",
	notMember:        "not-member",
	scopeMismatch:    "scope-mismatch",
	stepUpMFA:        "step-up-mfa",
	stepUpSSO:        "step-up-sso",
	noIdentity:       "no-identity",
	unavailable:      "unavailable",
}

// String returns the outcome's value in the metric attribute.
func (o outcome) String() string { return names[o] }

// passes reports whether the request goes on to the layers inside.
func (o outcome) passes() bool { return o == admitted || o == admittedByPolicy }

// status is the status code a refusing outcome is answered with. It is the
// closed set of this layer: 403 is not in it, because what an account may do
// once inside is the permission model's to answer.
func (o outcome) status() int {
	switch o {
	case notMember, scopeMismatch:
		return nethttp.StatusNotFound
	case stepUpMFA, stepUpSSO, noIdentity:
		return nethttp.StatusUnauthorized
	case unavailable:
		return nethttp.StatusServiceUnavailable
	}
	return nethttp.StatusOK
}

// decision is the conclusion about one request, with what the answer and the
// record need beyond the outcome.
type decision struct {
	outcome outcome
	// cause is the failure behind an unavailable outcome. It goes to the log
	// and never to the caller.
	cause error
	// challenge is the scheme of the credential the request authenticated
	// with, for a step-up answer's challenge.
	challenge string
}
