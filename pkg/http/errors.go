package http

import "errors"

// The sentinel errors this module produces. A caller tells the classes apart
// with errors.Is, and the error text carries what is needed to locate the
// cause and states the fixing action. Wrapping is always with %w, so the
// underlying net error chain of a bind failure stays intact.
//
// The table is closed over the failures this module judges for itself: a newly
// identified class either joins an existing sentinel or adds one here.
// Failures that merely travel through this module are not in it — a handler
// and a middleware define their own, and this module invents no names for
// them.
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
	// ErrInvalidConfig reports a value error read out of configuration: an
	// unknown key, a missing address, the reserved endpoint name, a duration
	// an item refuses, a claim list or one of its prefixes that is not usable.
	// The fixing action is always to correct or add that item, so the text
	// names the entry and the complete key path, together with the value that
	// was refused.
	//
	// Cross-entry references and conflicts have sentinels of their own:
	// ErrUnknownListener for an endpoint naming a listener no configuration
	// declares, and ErrClaimConflict for two endpoints claiming one prefix.
	ErrInvalidConfig = errors.New("http: invalid configuration value")
	// ErrClaimConflict reports two endpoints on one listener claiming the same
	// path prefix, the root "/" included: the listener selects an endpoint by
	// the longest prefix a request falls under, so one prefix belongs to one
	// endpoint. Two endpoints that both omit prefixes both claim "/", which is
	// the same conflict. The text names the listener, both endpoints and the
	// prefix.
	ErrClaimConflict = errors.New("http: two endpoints on one listener claim the same prefix")
	// ErrUnclaimedRoute reports a route the listener's dispatch would never
	// hand to the endpoint that registered it: its literal leading path falls
	// under another endpoint's claimed prefix, or under none while the
	// endpoint claims no root. Selection reads configuration only, so such a
	// route is mounted but unreachable. The text names the listener, the
	// route, the endpoint that registered it and its claims, and the endpoint
	// and prefix that would answer it, or says that no endpoint would.
	ErrUnclaimedRoute = errors.New("http: route lies outside its endpoint's claimed prefixes")
	// ErrRouteConflict reports a routing clash gin refuses to mount: two routes
	// on one endpoint, or a route together with the endpoint's layers beyond
	// the number of handlers one gin chain holds. It surfaces while the chains
	// are assembled in Serve, not at registration: a registration records a
	// declaration, and conflict is a property of the mounted set.
	//
	// The text names both routes, each as its method and path, and the endpoint
	// or the listener they are on, and not the registrants: the registration
	// surface does not know who called it, the same reason a layer has to
	// declare its own Provides. A method and a path are literals in the
	// source, so naming them locates the call. When the refusal cannot be
	// attributed to a pair, the text says so instead of naming one route as
	// though that were the verdict.
	ErrRouteConflict = errors.New("http: two routes on one endpoint conflict")
	// ErrMiddlewareCycle reports that the After and Before constraints of
	// an endpoint's middleware form a cycle, so no order satisfies them.
	// The text names every member of the cycle along with its own
	// declarations.
	ErrMiddlewareCycle = errors.New("http: middleware constraints form a cycle")
	// ErrStageConflict reports an After or Before that points across stages in
	// the direction the stage order already forbids: a layer in an earlier
	// stage declares itself after a capability a later stage stands for, or the
	// reverse. The stage order is the framework's, so this is not a position
	// the caller can be given quietly — the constraint asks for the opposite of
	// where the stages put the two layers.
	//
	// The text names both layers, both stages and the declaration, because the
	// fix is one of two edits — move a layer to a stage that matches the
	// constraint, or drop the constraint, which the stage boundary now covers
	// on its own — and the reader can only choose with both positions in front
	// of them.
	ErrStageConflict = errors.New("http: middleware constraint contradicts the stage order")
	// ErrInvalidSpec reports a declared OpenAPI fragment whose shape is not
	// usable: empty, not UTF-8, not JSON, not an object at the top level, or
	// an object without an openapi field. The content beyond that shape is
	// not checked here.
	ErrInvalidSpec = errors.New("http: OpenAPI fragment has an unusable shape")
	// ErrListen reports that a listener could not bind its address. It
	// wraps the underlying net error, so a caller can still reach for
	// syscall.EADDRINUSE through it.
	ErrListen = errors.New("http: listener could not bind its address")
	// ErrDrainTimeout reports that a listener's in-flight requests had not
	// finished when its configured drain timeout expired. The connections
	// still open at that moment are cut.
	ErrDrainTimeout = errors.New("http: listener did not drain within its timeout")
)
