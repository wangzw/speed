package model

// The purposes a verification code may be sent for. The set is closed.
const (
	SMSPurposeLogin  = "login"
	SMSPurposeMFA    = "mfa"
	SMSPurposeEnroll = "enroll"
	SMSPurposeReset  = "reset"
)

// SendCode is what asking for a verification code takes.
type SendCode struct {
	Purpose string    // one of the SMSPurpose constants
	Phone   string    // E.164, already normalized
	Account AccountID // required for mfa and enroll; empty for login and reset, which are not counted by it
	Locale  string    // zh-CN or en-US, set by the caller; empty reads zh-CN
	Deliver bool      // false: write a suppressed row and count it against every budget but the deployment-wide one, enqueue and send nothing
	// Source is the request's client address. There is no source budget and no
	// source lock when it is empty: an empty source means the caller has no
	// trustworthy origin, and the budget is fail-open rather than counting every
	// such send under one key.
	Source string
	// Proof is the human-verification proof the request presented, if the
	// deployment requires one. The code engine checks it before it spends any
	// budget and consumes it once: a proof a caller already checked would be
	// consumed twice, so the check belongs here and nowhere on the caller's
	// side. Empty means the request presented nothing.
	Proof string
}
