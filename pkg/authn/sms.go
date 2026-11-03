package authn

import "context"

// SMSCodes is the state machine of SMS verification codes: the allowed-prefix
// table, the budgets, the challenge rows and the check of a code. The SMS module
// delivers it exclusively. The login engine, the multi-factor module's SMS
// factor and the account self-service routes take it, each as optional, and
// without it the SMS login, the SMS factor and the phone routes are not there.
//
// Sending a code and sending a message are two things: this interface knows no
// carrier, and the module that delivers it takes the carrier it sends through.
type SMSCodes interface {
	// Send checks the allowed-prefix table and every budget, writes the challenge
	// row, hands the sending to a bounded worker pool and returns at once, so the
	// time it takes does not depend on the carrier. The errors are
	// ErrUnsupportedRegion, ErrThrottled with its RetryAfter when a budget is spent
	// and ErrUnavailable when a dependency cannot answer. A proof the deployment's
	// gate refuses is ErrHumanVerificationRequired for a missing, invalid, expired
	// or already used one, and ErrHumanProofTooLong for one over the field's
	// bound; both are returned before any budget is spent. Deliver true and false
	// take one path and give one shape of answer, so the caller cannot tell a
	// suppressed row from a real one by the result.
	//
	// The caller owes a Purpose from the closed set, a normalized Phone and, for
	// mfa and enroll, an Account; breaking that is a defect of the caller and an
	// ordinary error, not one of the sentinels. The per-flow send limit is
	// checked by the caller before it calls.
	Send(ctx context.Context, in SendCode) (challengeID string, err error)
	// Verify counts one attempt on the challenge row, compares the code against
	// its keyed digest and, when it matches, marks the row consumed with a
	// conditional update. Every case that does not pass is ErrInvalidCredentials,
	// a suppressed row included, which still counts and pays one decoy
	// comparison. A pass says only that whoever holds the challenge identifier
	// presented the right code: it returns neither the number nor the account,
	// which the caller keeps in its own flow record.
	Verify(ctx context.Context, challengeID, code string) error
}

// PurposeSupport is the optional interface a sender of verification codes may
// publish to say which purposes it can carry. It changes no method of SMSCodes:
// a caller resolves SMSCodes, asserts it to this interface, and treats an
// implementation that does not publish it as supporting only the purposes every
// sender carries.
//
// The login, mfa and enroll purposes are always supported. The reset purpose —
// an anonymous password reset proving the holder of a number — depends on the
// carrier declaring the authn.code.reset template, so a deployment whose carrier
// does not declare it publishes no SMS reset channel while the other purposes
// keep working.
type PurposeSupport interface {
	// SupportsPurpose reports whether a code may be sent for purpose.
	SupportsPurpose(purpose string) bool
}
