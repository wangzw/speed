package http

import (
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
type Router interface {
	// Endpoint returns a declared endpoint. It may be called in any stage.
	// An undeclared name is an error wrapping ErrUnknownEndpoint, because
	// the set of endpoints comes from configuration, which is data.
	Endpoint(name string) (Endpoint, error)
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

// Middleware is one layer together with what it declares about its own
// position.
//
// After and Before are hard constraints and decide the topological order;
// Order is a preference that only picks among the positions the constraints
// already allow. The two cannot contradict each other, because Order never
// selects outside the ready set.
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
	// Provides names the capabilities this layer stands for on the chain, so
	// that another layer's After or Before can point at it. Leaving it empty
	// is legal, at the cost that nobody can point at this layer.
	//
	// It is not checked against the Provides of the registrant's own module
	// descriptor, for the same reason it has to be declared here at all:
	// this module does not know who registered the layer. A layer may
	// therefore claim a capability its module does not deliver.
	Provides []core.Token
	// After places this layer inside the middleware of these capabilities.
	// A capability no other layer stands for drops the constraint, and the
	// dropped constraint is listed in the startup diagnostics with the cause
	// it dropped for: no layer on the endpoint represents the capability, or
	// only this one does, and that edge to itself is dropped too. A
	// capability nobody on the endpoint represents is written at Info,
	// because a module that is absent or does not guard the endpoint is a
	// legal configuration; one only this layer represents is written at
	// Warn, because no assembly makes that declaration place anything.
	After []core.Token
	// Before places this layer outside the middleware of these
	// capabilities, with the same treatment of an absent provider.
	Before []core.Token
	// Order is the preference where the constraints leave a choice; a
	// smaller value sits further out. Layers with equal Order and no
	// constraint between them keep their registration order.
	Order int
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
