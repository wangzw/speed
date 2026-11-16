package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// notificationModuleName is the name the host registers this module under.
const notificationModuleName = "notification.access"

// notificationRoutes is the notification capability's route set, each as the
// entry point registered it: the HTTP method and the path, with gin's :param
// forms.
//
// The set is closed and fixed by the capability's design
// (docs/design/modules/notification), which is why the host writes it out here
// rather than deriving it: the route-declaration surface matches a route by its
// exact (method, path) pair and has no prefix or wildcard form, so a route the
// capability adds and this list omits is one the guard refuses on the closed
// default. The inbox routes are the four the inbox module mounts, the
// preferences routes the two reads and the write the preference matrix mounts,
// and the last is the stream.
var notificationRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/notifications/messages"},
	{http.MethodGet, "/api/v1/notifications/messages/unread-count"},
	{http.MethodPost, "/api/v1/notifications/messages/read-all"},
	{http.MethodPost, "/api/v1/notifications/messages/:message_id/read"},
	{http.MethodGet, "/api/v1/notifications/types"},
	{http.MethodGet, "/api/v1/notifications/preferences"},
	{http.MethodPut, "/api/v1/notifications/preferences/:type_key/:channel"},
	{http.MethodGet, "/api/v1/notifications/stream"},
}

// init registers the module. The import is the wiring, as for every module; the
// serve command's Include list is what puts it in a run.
func init() { core.ProcessRegistry.Register(notificationModule()) }

// notificationModule is the host's own declaration of the notification
// surface's access.
//
// The notification capability declares no permission point of its own
// (pkg/notification documents that the decision is the host's): its routes
// carry no permission gate, so nothing registers them and the authorization
// guard, whose default is closed, refuses each one 403. This module is the host
// making the decision the capability leaves open — it registers the routes open
// on the guard's declaration surface, so the guard admits a request that
// carries a credential and the handler answers for the caller.
//
// The routes are registered open rather than on a permission because they are
// scoped by the caller's identity and not by anyone's permission: the inbox and
// the stream read the caller's own account, and the preference matrix reads the
// recipient the host's identity and authorization side names. A permission gate
// would be the wrong shape — a per-user inbox is not something a caller is
// granted, it is something the caller is.
//
// The declaration is made in Init because that is the window the guard's
// declaration table is open in: the guard opens the table with the assembly and
// seals it, holding every requirement to the catalog, once Init has ended. The
// guard is taken up optionally — a run that names this module without the
// authorization layer has no table to write to and nothing to enforce, so the
// declaration is simply not made — and it is named on serve alone, the one
// command that stands the guard up.
func notificationModule() core.Module {
	return core.Module{
		Name: notificationModuleName,
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				if errors.Is(err, core.ErrMissingProvider) {
					return nil
				}
				return err
			}
			for _, r := range notificationRoutes {
				gate.Open(r.method, r.path)
			}
			return nil
		},
	}
}
