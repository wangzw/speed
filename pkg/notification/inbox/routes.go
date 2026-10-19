package inbox

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The routes, under the fixed base path. The set is closed: every action the
// inbox exposes is one of these four, and each is bound to the method and path
// the design fixes. None carries a permission gate of its own — who may read a
// recipient's inbox is the host's authorization decision — so the handler reads
// the caller's identity only to know whose inbox is being read.
//
// The absolute paths are the declared fragment paths resolved against the API
// root this module's Spec carries, so the declaration is the one place a route's
// path is written.
var (
	basePath        = inboxSpec.ResolvePath("/notifications")
	pathMessages    = inboxSpec.ResolvePath(pathMessagesFragment)
	pathUnreadCount = inboxSpec.ResolvePath(pathUnreadCountFragment)
	pathReadAll     = inboxSpec.ResolvePath(pathReadAllFragment)
	pathMessageRead = inboxSpec.ResolvePath(pathMessageReadFragment)
)

// route is one inbox route: the method and path it answers and the handler that
// serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// routes is the closed set of inbox routes, in the order they are mounted. Each
// handler is bound from the operation declared for it, so it receives the
// declared input and writes the declared answer.
func (s *inboxStore) routes() []route {
	return []route{
		{http.MethodGet, pathMessages, s.guardList(listMessagesOperation.Handle(s.listMessages))},
		{http.MethodGet, pathUnreadCount, unreadCountOperation.Handle(s.unreadCount)},
		{http.MethodPost, pathReadAll, markAllMessagesReadOperation.Handle(s.markAllRead)},
		{http.MethodPost, pathMessageRead, markMessageReadOperation.Handle(s.markMessageRead)},
	}
}

// guardList holds the one check the declared input cannot make: a limit or an
// offset that is not a number. The entry point binds an int field from the
// request and answers its own problem when the text is not a number, while this
// module's contract answers that refusal with the module's bad-request problem
// type. The check therefore stays at the mount, in front of the handler.
//
// The identity is read first, which keeps the route's original order: a request
// with neither an identity nor a numeric page bound is still answered 401.
func (s *inboxStore) guardList(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if _, ok := authn.FromContext(ctx); !ok {
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
			return
		}
		for _, name := range []string{"limit", "offset"} {
			raw := c.Query(name)
			if raw == "" {
				continue
			}
			if _, err := strconv.Atoi(raw); err != nil {
				speedhttp.Fail(c, s.refusal(ctx, http.StatusBadRequest, badRequestCode).Problem())
				return
			}
		}
		next(c)
	}
}

// mount resolves every configured endpoint on the entry point and registers the
// routes on each. Every name is looked up before any route is registered, so a
// name the entry point does not declare fails the startup naming that endpoint
// rather than leaving a partial mounting behind.
func (s *inboxStore) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(s.endpoints))
	for _, name := range s.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: an undeclared endpoint in %s: %q: %w", ModuleName, endpointsKey, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range s.routes() {
			tgt.endpoint.Route(r.method, r.path, r.handler)
		}
	}
	return nil
}
