package tenant

import "context"

// Kind is a tenant's kind. The set of values is closed: adding one changes
// every place that branches on it, and the vocabulary is part of the platform's
// public interface face.
//
// The kind is not recorded by the roster. A host that has a personal-tenant
// policy knows which tenants are personal and answers through KindResolver; the
// roster stores no column and decides nothing from one.
type Kind string

const (
	// KindBusiness is the zero value: an ordinary tenant. It is also the answer
	// an assembly with no kind source gives for every tenant.
	KindBusiness Kind = "business"
	// KindPersonal is a personal tenant: the single-person space an account
	// owns. Only a host's policy makes one, and only a host can name it.
	KindPersonal Kind = "personal"
)

// KindResolver answers one tenant's kind. A host implements it, and the caller
// tenant read passes the answer through. The kind is a read-only fact and takes
// no part in admission or authorization.
//
// It is optional where it is taken up: an assembly with no personal-tenant
// policy supplies none, and every tenant then reads as KindBusiness. An
// assembly that carries such a policy must provide one, which the policy
// module's own descriptor enforces by requiring this capability.
type KindResolver interface {
	// KindOf answers the kind of the tenant with this identifier. It is asked
	// once per tenant the caller tenant read lists, and its error is a failure
	// of the read rather than an answer: the caller answers 503 instead of
	// reading the row as KindBusiness.
	KindOf(ctx context.Context, id ID) (Kind, error)
}
