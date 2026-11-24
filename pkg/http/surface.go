package http

import (
	"fmt"
	"math"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// Router is the capability this module delivers. It is taken up through the registry,
// written core.Resolve[http.Router](reg), and it hands out the endpoints configuration
// declared.
type Router interface {
	// Endpoint returns a declared endpoint; an undeclared name is an error.
	// It may be called in any stage.
	Endpoint(name string) (Endpoint, error)
	// Endpoints returns the name of every endpoint this assembly declared, in
	// ascending order. Each call returns a slice of its own, and it may be called
	// in any stage.
	Endpoints() []string
}

// Endpoint is the registration surface of one listening endpoint. Route and Use
// panic when called outside this module's registration window.
type Endpoint interface {
	// Route binds handlers to a method and a path on this endpoint. The path is
	// written in gin's grammar and in the form Context.FullPath reports; the
	// handlers run in order and the last one serves the request.
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

// Stage is the category a layer belongs to. The chain runs the stages in the order they
// are declared on this type, outermost first, so where the boundary between two
// categories falls is decided here and does not move when a module is added or removed.
type Stage uint8

const (
	// StageObserve sees every request and never refuses one or writes a
	// response: timing, tracing, binding fields into the context.
	StageObserve Stage = iota + 1
	// StageGuard protects the process without knowing who is calling: CORS, an address
	// limit, anything keyed on the request alone. A guard layer must not read the request
	// body, which is bounded only from the identify stage inwards.
	StageGuard
	// StageIdentify settles who the caller is and which tenant the request is for; its
	// refusals are 401, 404 and 503.
	StageIdentify
	// StageThrottle applies quotas to an identity or a tenant that is already settled.
	StageThrottle
	// StageRecord records what passed admission and never refuses a request.
	StageRecord
	// StageAuthorize decides on a settled identity; its refusals are recorded by the
	// layers inside it.
	StageAuthorize
)

// Order values are compared only within one stage, so a number in one stage says
// nothing about a layer in another; the boundary between stages is Stage's, and no
// value moves a layer across it.
const (
	// OrderOutermost is math.MinInt: no ordinary Order is smaller, so a layer carrying it
	// comes out at the outside of its stage.
	OrderOutermost = math.MinInt
	// OrderInnermost is math.MaxInt, the inside counterpart of OrderOutermost.
	OrderInnermost = math.MaxInt
)

func validStage(s Stage) bool { return s >= StageObserve && s <= StageAuthorize }

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

// Middleware is one layer together with what it declares about its own position.
type Middleware struct {
	// Name is for diagnostics only: the panic text and the chain listed at
	// startup. It is not an identity this module matches anything against.
	Name string
	// Stage is the category this layer belongs to, and the chain runs the stages in the
	// order they are declared on Stage. Stage is required: registration panics on a zero
	// or undeclared stage.
	Stage Stage
	// Order places the layer within its stage, below After and Before, which are hard
	// constraints there. A zero Order means no preference within its stage.
	Order int
	// Provides names the capabilities this layer stands for on the chain, so that another
	// layer's After or Before can point at it.
	Provides []core.Token
	// After places this layer inside the middleware of these capabilities, within this
	// layer's stage.
	After []core.Token
	// Before places this layer outside the middleware of these capabilities, within this
	// layer's stage, with the same treatment of an absent provider and of a provider in
	// another stage as After.
	Before []core.Token
	// Handler is the layer itself. It hands the request on with c.Next and
	// ends the chain with c.Abort, as any gin handler does. A nil Handler
	// panics at registration.
	Handler gin.HandlerFunc
}

// Spec is the resource type a module declares its own OpenAPI fragment as.
type Spec struct {
	// Endpoint names the listening endpoint whose API this fragment describes. A name that was never declared is not an error: a resource is a static declaration, and this module does not interpret what it describes.
	Endpoint string
	// Prefix is the path the endpoint serves its API under, written as bare segments:
	// "api", or "internal/api" for a nested prefix.
	Prefix string
	// Version is the API version segment the module serves, one bare segment: "v1".
	Version string
	// Scope is the management level the fragment's paths are published under, one segment:
	// tenants or system.
	Scope Scope
	// Document is the OpenAPI fragment, as JSON.
	Document []byte
}

// APIRoot is the path the fragment's paths are relative to: Prefix, Version and Scope
// joined into one absolute path, with an empty part contributing no segment.
func (s Spec) APIRoot() string {
	parts := make([]string, 0, 3)
	for _, part := range []string{s.Prefix, s.Version, s.Scope.scopeRoot()} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "/" + strings.Join(parts, "/")
}

// ResolvePath returns the absolute path of one path written in the fragment.
func (s Spec) ResolvePath(path string) string {
	root := s.APIRoot()
	if root == "" {
		return path
	}
	if path == "/" {
		return root
	}
	return root + path
}
