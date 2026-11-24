package tenant

import "context"

// Kind is a tenant's kind. The set of values is closed and part of the
// platform's public interface face. The roster records no kind; a host answers
// it through KindResolver.
type Kind string

const (
	// KindBusiness is the zero value: an ordinary tenant. It is also the answer
	// an assembly with no kind source gives for every tenant.
	KindBusiness Kind = "business"
	// KindPersonal is a personal tenant: the single-person space an account
	// owns. Only a host's policy makes one, and only a host can name it.
	KindPersonal Kind = "personal"
)

// KindResolver answers one tenant's kind; a host implements it and the caller
// tenant read passes the answer through. The kind takes no part in admission or
// authorization. It is optional: without one every tenant reads as KindBusiness.
type KindResolver interface {
	// KindOf answers the kind of the tenant with this identifier. An error is a
	// failure of the read, not an answer: the caller answers 503 rather than
	// reading the row as KindBusiness.
	KindOf(ctx context.Context, id ID) (Kind, error)
}
