package http

import "errors"

// The sentinel errors this module produces. A caller tells the classes apart with
// errors.Is, and the error text carries what is needed to locate the cause and states
// the fixing action.
var (
	// ErrUnknownEndpoint reports a lookup for an endpoint name that
	// configuration never declared. The text lists the names that do exist,
	// because that is the fixing action; there is no fallback to a default
	// endpoint.
	ErrUnknownEndpoint = errors.New("http: endpoint name was never declared")
	// ErrUnknownListener reports an endpoint that hangs on a listener name the
	// configuration never declared. The text names the endpoint, the listener
	// it named and the listeners that do exist, because picking one of them is
	// the fixing action.
	ErrUnknownListener = errors.New("http: listener name was never declared")
	// ErrInvalidConfig reports a value error read out of configuration: an unknown key, a
	// missing address, the reserved endpoint name, a duration an item refuses, a claim
	// list or one of its prefixes that is not usable.
	ErrInvalidConfig = errors.New("http: invalid configuration value")
	// ErrClaimConflict reports two endpoints on one listener claiming the same path
	// prefix, the root "/" included: the listener selects an endpoint by the longest
	// prefix a request falls under, so one prefix belongs to one endpoint.
	ErrClaimConflict = errors.New("http: two endpoints on one listener claim the same prefix")
	// ErrUnclaimedRoute reports a route the listener's dispatch would never hand to the
	// endpoint that registered it: its literal leading path falls under another endpoint's
	// claimed prefix, or under none while the endpoint claims no root.
	ErrUnclaimedRoute = errors.New("http: route lies outside its endpoint's claimed prefixes")
	// ErrRouteConflict reports a routing clash gin refuses to mount: two routes on one
	// endpoint, or a route together with the endpoint's layers beyond the number of
	// handlers one gin chain holds.
	ErrRouteConflict = errors.New("http: two routes on one endpoint conflict")
	// ErrMiddlewareCycle reports that the After and Before constraints of an endpoint's
	// middleware form a cycle, so no order satisfies them.
	ErrMiddlewareCycle = errors.New("http: middleware constraints form a cycle")
	// ErrStageConflict reports an After or Before that points across stages in the
	// direction the stage order already forbids: a layer in an earlier stage declares
	// itself after a capability a later stage stands for, or the reverse.
	ErrStageConflict = errors.New("http: middleware constraint contradicts the stage order")
	// ErrInvalidSpec reports a declared OpenAPI fragment whose shape is not usable: empty,
	// not UTF-8, not JSON, not an object at the top level, or an object without an openapi
	// field.
	ErrInvalidSpec = errors.New("http: OpenAPI fragment has an unusable shape")
	// ErrListen reports that a listener could not bind its address.
	ErrListen = errors.New("http: listener could not bind its address")
	// ErrDrainTimeout reports that a listener's in-flight requests had not finished when
	// its configured drain timeout expired.
	ErrDrainTimeout = errors.New("http: listener did not drain within its timeout")
)
