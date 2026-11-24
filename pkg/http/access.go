package http

// Access is the level of caller identity an operation's route admits. Its zero
// value is AccessRequired, so an operation that declares no Access admits only
// an identified caller.
type Access uint8

const (
	// AccessRequired admits only a request whose caller has been identified.
	AccessRequired Access = iota
	// AccessOptional admits a request with no credential as an anonymous
	// caller, and still refuses a credential that does not hold.
	AccessOptional
	// AccessPublic admits every request without a credential and reads none.
	AccessPublic
)

// Valid reports whether the value is one of the declared levels.
func (a Access) Valid() bool { return a <= AccessPublic }

// Public declares the operation public: every request reaches the handler without a
// credential.
func Public() OperationOption { return func(o *operationOptions) { o.access = AccessPublic } }

// Optional declares the operation's authentication optional: a request without a
// credential reaches the handler as an anonymous caller.
func Optional() OperationOption { return func(o *operationOptions) { o.access = AccessOptional } }

type operationOptions struct {
	access Access
}
