package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

const notificationModuleName = "notification.access"

// notificationRoutes is the notification capability's route set, each as the
// entry point registered it: the HTTP method and the path, with gin's :param
// forms.
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

func init() { core.ProcessRegistry.Register(notificationModule()) }

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
