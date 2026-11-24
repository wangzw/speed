package tenant

import "context"

// Kind is a tenant's kind. The set of values is closed.
type Kind string

const (
	// KindBusiness is the zero value: an ordinary tenant, and the answer when no
	// kind source is supplied.
	KindBusiness Kind = "business"
	// KindPersonal is the single-person space an account owns.
	KindPersonal Kind = "personal"
)

// KindResolver answers one tenant's kind. It is optional: without one, every
// tenant reads as KindBusiness.
type KindResolver interface {
	// KindOf answers the kind of the tenant with this identifier. Its error is a
	// failure of the read, not an answer.
	KindOf(ctx context.Context, id ID) (Kind, error)
}
