package inbox

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var (
	basePath        = inboxSpec.ResolvePath("/notifications")
	pathMessages    = inboxSpec.ResolvePath(pathMessagesFragment)
	pathUnreadCount = inboxSpec.ResolvePath(pathUnreadCountFragment)
	pathReadAll     = inboxSpec.ResolvePath(pathReadAllFragment)
	pathMessageRead = inboxSpec.ResolvePath(pathMessageReadFragment)
)

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
