package http

import (
	"fmt"
	"math"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// Router is the capability this module delivers. It is taken up through the
// registry, written core.Resolve[http.Router](reg), and it hands out the
// endpoints configuration declared.
//
// The module delivers it exclusively. Two entry point modules running at once
// would leave a registrant unable to tell which one it registered with, and
// "take them all out" means nothing for a registration, so the conflict is
// named during resolution rather than showing up at run time as a route nobody
// can reach.
//
// The name "*" is reserved: configuration cannot declare an endpoint by it, so
// that a list of endpoint names can write it to mean every declared endpoint
// without colliding with one.
type Router interface {
	// Endpoint returns a declared endpoint. It may be called in any stage.
	// An undeclared name is an error wrapping ErrUnknownEndpoint, because
	// the set of endpoints comes from configuration, which is data.
	Endpoint(name string) (Endpoint, error)
	// Endpoints returns the name of every endpoint this assembly declared, in
	// ascending order. Each call returns a slice of its own, so a caller may
	// change it without affecting the Router. It may be called in any stage.
	Endpoints() []string
}

// Endpoint is the registration surface of one listening endpoint.
//
// Route and Use accept writes while this module's registration gate is open; a
// write outside that window panics. The gate opens in the module's Migrate
// callback and closes in its Start callback, so the window contains the whole
// Init stage. They return nothing: the only way a registration can fail is a
// programming error at the call site, and handing back an error a registrant
// is free to drop would leave a route silently unbound. A conflict between two
// routes is not of that kind — it can only be judged once the routes are
// mounted in Serve, and it is reported there as a startup failure.
//
// Reads are not behind that gate: Accepting may be called in any stage.
type Endpoint interface {
	// Route binds handlers to a method and a path on this endpoint. The path
	// is written in gin's grammar — ":name" for a parameter, "*name" for a
	// catch-all — and has to be in the form gin keeps it in, which is the
	// form Context.FullPath reports: it begins with a slash, carries no
	// repeated slash and no "." or ".." segment, and keeps a trailing slash
	// as written. The handlers run in order and the last one serves the
	// request, so a route can carry a chain of its own beside the layers
	// registered with Use.
	Route(method, path string, handlers ...gin.HandlerFunc)
	// Use registers a middleware layer on this endpoint. The layer applies
	// to every request on the endpoint, including one that matches no route
	// and one whose method the matched path does not allow.
	Use(mw Middleware)
	// Accepting reports whether this endpoint is still taking requests. It
	// returns false before the address is bound and after Stop, and it is
	// safe to call from any goroutine at any moment.
	Accepting() bool
}

// Stage is the category a layer belongs to. The chain runs the stages in the
// order they are declared on this type, outermost first, so where the boundary
// between two categories falls is decided here and does not move when a module
// is added or removed.
//
// It exists because a whole-category constraint is the one thing a stated order
// cannot carry. "Every layer that observes a request sits outside every layer
// that can refuse one" holds for layers this module has never heard of, and
// under a numeric order a host layer registered with a smaller number would
// slip inside without a signal. The stages are the general HTTP pipeline kinds
// — observing, protecting, identifying, throttling, recording, authorizing —
// and not concrete capabilities, which is why they are fixed here rather than
// named by whichever module chooses one: a set a module could extend would be
// back to the problem the fixed set solves, and these categories have changed
// rarely enough that a new one is a change to this module and an ADR.
//
// The zero value is no stage. Stage is required, and registration refuses a
// layer that leaves it out. There is deliberately no default: a default is
// where a layer that forgot to declare itself lands, in a category nobody
// chose, and for a layer whose job is to refuse a request the safe answer is
// not the one a wrong guess would pick.
type Stage uint8

const (
	// StageObserve sees every request and never refuses one or writes a
	// response: timing, tracing, binding fields into the context.
	StageObserve Stage = iota + 1
	// StageGuard protects the process without knowing who is calling: CORS, an
	// address limit, anything keyed on the request alone. A guard layer refuses
	// on what it can see without reading the request body — an address, a
	// header, a CORS preflight — and must not read the body, because the body is
	// bounded only from the identify stage inwards. Bounding it in front of the
	// guard stage would answer an oversize request before the address limit had
	// counted it, and would put the body limit outside CORS, leaving the 413 a
	// browser cannot read.
	StageGuard
	// StageIdentify settles who the caller is and which tenant the request is
	// for; its refusals are 401, 404 and 503.
	StageIdentify
	// StageThrottle applies quotas to an identity or a tenant that is already
	// settled.
	StageThrottle
	// StageRecord records what passed admission and never refuses a request.
	StageRecord
	// StageAuthorize decides on a settled identity; its refusals are recorded
	// by the layers inside it.
	StageAuthorize
)

// Order values are compared only within one stage, so a number in one stage
// says nothing about a layer in another; the boundary between stages is
// Stage's, and no value moves a layer across it. Smaller numbers come out
// further out, so a layer that leaves Order at its zero value falls between the
// layers that asked for the outside of the stage and the ones that asked for
// the inside.
//
// Order is the fine setting below Stage's coarse one, and the two cannot
// contradict each other: Stage fixes the category and where the category sits
// in the chain, and Order only moves a layer among the layers whose stage is
// already settled. It is for the few places where a layer asks for the outside
// of its own stage without naming any other layer — CORS has to sit outside the
// address limit in the same stage, so that a 429 carries its headers.
//
// Equal values carry no order between them: two layers that share one are
// separated by registration order, which is stable within a single assembly but
// is not something a layer may rely on, and nothing is reported or fails when
// they are equal. The extreme values have names below, so a layer that must not
// be overtaken takes one without inventing a number.
const (
	// OrderOutermost is math.MinInt: no ordinary Order is smaller, so a layer
	// carrying it comes out at the outside of its stage. More than one layer may
	// take it, and two that do are then separated by registration order like any
	// equal values. It is not a guarantee of being the outermost layer — the
	// stage boundary and the constraints still decide that.
	OrderOutermost = math.MinInt
	// OrderInnermost is math.MaxInt, the inside counterpart of OrderOutermost.
	OrderInnermost = math.MaxInt
)

// validStage reports whether a registration named one of the declared stages.
// The zero value is not one of them: it says the registrant did not choose.
func validStage(s Stage) bool { return s >= StageObserve && s <= StageAuthorize }

// stageName renders a stage for a diagnostic — the ordering's failure text and
// the chain listing — so the reader meets the category by the name it is
// declared under rather than by its number.
func stageName(s Stage) string {
	switch s {
	case StageObserve:
		return "observe"
	case StageGuard:
		return "guard"
	case StageIdentify:
		return "identify"
	case StageThrottle:
		return "throttle"
	case StageRecord:
		return "record"
	case StageAuthorize:
		return "authorize"
	default:
		return fmt.Sprintf("stage(%d)", uint8(s))
	}
}

// Middleware is one layer together with what it declares about its own
// position.
//
// The position is settled in two steps that cannot contradict each other.
// Stage says which category the layer belongs to, and the chain runs the
// categories in their declared order; the stage boundary is the framework's, so
// it holds for layers registered by modules this one has never seen. After and
// Before are hard constraints within a stage and decide the order there, and
// Order is a setting that only picks among the places those constraints leave
// open. A cross-stage After or Before therefore holds only when it agrees with
// the stage order: the boundary is not something an edge can move, and one that
// points against it is refused at startup rather than letting the layer slip to
// the wrong side in silence.
//
// Provides is what makes those constraints land: After and Before name
// capabilities, and which layer on the chain stands for a capability can only
// be said by that layer itself. The registration surface cannot supply it —
// Endpoint carries no registrant identity, core offers no "the module
// currently in Init" query, and a registration may be made from a goroutine,
// so the call stack says nothing either.
type Middleware struct {
	// Name is for diagnostics only: the panic text and the chain listed at
	// startup. It is not an identity this module matches anything against.
	Name string
	// Stage is the category this layer belongs to, and the chain runs the
	// stages in the order they are declared on Stage. It is required:
	// registration panics on the zero value, and on any value that is not a
	// declared stage, because a layer that has not said which category it is in
	// has no position this module can give it. There is deliberately no default
	// — filed under one, a layer that forgot to declare itself would run in a
	// category nobody chose, and for a layer whose job is to refuse a request
	// the safe answer is not the one a wrong guess would pick.
	Stage Stage
	// Order places the layer within its stage, below After and Before, which
	// are hard constraints there. Smaller numbers come out further out and the
	// zero value means no preference — the layer falls between the ones that
	// asked for the outside of the stage and the ones that asked for the inside.
	// It is compared only among the layers of one stage; a value in one stage
	// says nothing about a layer in another. Equal values carry no order between
	// them and are separated by registration order, and nothing is reported or
	// fails when two layers share one. OrderOutermost and OrderInnermost are the
	// extreme values, for a layer that must not be overtaken by an ordinary one.
	// An ordinary value is best spaced out — tens apart, say — so a later layer
	// can slot between two of them without reusing a value, since a reused value
	// carries no order.
	Order int
	// Provides names the capabilities this layer stands for on the chain, so
	// that another layer's After or Before can point at it. Leaving it empty
	// is legal, at the cost that nobody can point at this layer.
	//
	// It is not checked against the Provides of the registrant's own module
	// descriptor, for the same reason it has to be declared here at all:
	// this module does not know who registered the layer. A layer may
	// therefore claim a capability its module does not deliver.
	Provides []core.Token
	// After places this layer inside the middleware of these capabilities,
	// within this layer's stage. A capability a layer in another stage stands
	// for draws no edge: that constraint holds only when the stage order
	// already places this layer inside such a provider, and registration fails
	// when it does not. A capability no other layer stands for drops the
	// constraint, and the dropped constraint is listed in the startup
	// diagnostics with the cause it dropped for: no layer on the endpoint
	// represents the capability, or only this one does, and that edge to itself
	// is dropped too. A capability nobody on the endpoint represents is written
	// at Info, because a module that is absent or does not guard the endpoint is
	// a legal configuration; one only this layer represents is written at
	// Warn, because no assembly makes that declaration place anything.
	After []core.Token
	// Before places this layer outside the middleware of these
	// capabilities, within this layer's stage, with the same treatment of an
	// absent provider and of a provider in another stage as After.
	Before []core.Token
	// Handler is the layer itself. It hands the request on with c.Next and
	// ends the chain with c.Abort, as any gin handler does. A nil Handler
	// panics at registration.
	Handler gin.HandlerFunc
}

// Spec is the resource type a module declares its own OpenAPI fragment as.
// This module stores it and checks its shape; it takes no part in routing.
//
// A reader takes every fragment with core.Resources[http.Spec](reg), each one
// carrying the name of the module that declared it, so a merge conflict can
// name its sources. Merging the fragments and publishing the result belong to
// the reader, not here.
type Spec struct {
	// Endpoint names the listening endpoint whose API this fragment
	// describes. A name that was never declared is not an error: a resource
	// is a static declaration, and this module does not interpret what it
	// describes.
	Endpoint string
	// Document is the OpenAPI fragment, as JSON. A module usually reads its
	// own openapi.json in with //go:embed.
	Document []byte
}
