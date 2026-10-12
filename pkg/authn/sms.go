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
	// ErrUnsupportedRegion, ErrThrottled with its RetryAfter and ErrUnavailable.
	// Deliver true and false take one path and give one shape of answer, so the
	// caller cannot tell a suppressed row from a real one by the result.
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

// SMS is one message handed to a carrier. The code it carries and any carrier
// credential are written to no log, no error text and no metric attribute.
type SMS struct {
	// To is the destination, in E.164.
	To string
	// Template is this side's template key, one of SMSPurposeLogin,
	// SMSPurposeMFA and SMSPurposeEnroll; the carrier maps it to a template
	// number of its own.
	Template string
	// Params carries the substitutions, its key set fixed to code and
	// ttl_minutes.
	Params map[string]string
	// Locale is the language of the message, one of LocaleZH and LocaleEN.
	Locale string
	// DedupeID is a stable identifier, the challenge identifier; a carrier
	// must not send the same identifier twice.
	DedupeID string
}

// SendReceipt is what a carrier answers with once it has taken a message.
type SendReceipt struct {
	ProviderID string
}

// SMSSender is the sending seam a carrier module delivers. The module that
// delivers it answers its own name, calls one carrier and never retries inside
// the call, and classifies what comes back into ErrSMSThrottled, ErrSMSRejected
// or ErrSMSUncertain: an error that is neither of the first two, a timeout and a
// network error are all ErrSMSUncertain. What the SMS module does with a receipt
// and with those errors is its own concern; the seam only carries the send.
//
// A carrier module delivers this exclusively, so a command tree names one
// carrier and two at once fail in resolution. It is a required, exclusive
// dependency of the SMS module and not one of the standing seams a consumer
// takes from every provider at once.
type SMSSender interface {
	Name() string
	Send(ctx context.Context, m SMS) (SendReceipt, error)
}
