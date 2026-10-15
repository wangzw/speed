package inbox

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The routes, under the fixed base path. The set is closed: every action the
// inbox exposes is one of these four, and each is bound to the method and path
// the design fixes. None carries a permission gate of its own — who may read a
// recipient's inbox is the host's authorization decision — so the handler reads
// the caller's identity only to know whose inbox is being read.
const (
	basePath        = "/api/v1/notifications"
	pathMessages    = basePath + "/messages"
	pathUnreadCount = basePath + "/messages/unread-count"
	pathReadAll     = basePath + "/messages/read-all"
	pathMessageRead = basePath + "/messages/:messageId/read"
)

// route is one inbox route: the method and path it answers and the handler that
// serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// routes is the closed set of inbox routes, in the order they are mounted.
func (s *inboxStore) routes() []route {
	return []route{
		{http.MethodGet, pathMessages, s.handleList},
		{http.MethodGet, pathUnreadCount, s.handleUnreadCount},
		{http.MethodPost, pathReadAll, s.handleReadAll},
		{http.MethodPost, pathMessageRead, s.handleMessageRead},
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
