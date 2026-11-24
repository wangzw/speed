package admission

import nethttp "net/http"

type outcome int

const (
	admitted outcome = iota
	admittedByPolicy
	notMember
	scopeMismatch
	stepUpMFA
	stepUpSSO
	noIdentity
	unavailable

	outcomeCount
)

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

// String returns the value of the outcome metric attribute.
func (o outcome) String() string { return names[o] }

func (o outcome) passes() bool { return o == admitted || o == admittedByPolicy }

// status is the status code a refusing outcome is answered with; 403 is never in the set.
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

type decision struct {
	outcome   outcome
	cause     error
	challenge string
}
