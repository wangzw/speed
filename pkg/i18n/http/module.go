// Package http wires the i18n capability into an entry point's middleware
// chain. It registers the module named "i18n.http", which in Init adds a
// negotiation layer and a profile layer to every declared endpoint.
package http

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// ModuleName is this module's name in the registry.
const ModuleName = "i18n.http"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor. Both dependencies are required.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*i18n.Negotiator)(nil)},
		},

		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return fmt.Errorf("i18n.http: taking up the router to register the locale layers: %w", err)
			}
			negotiator, err := core.Resolve[i18n.Negotiator](reg)
			if err != nil {
				return fmt.Errorf("i18n.http: taking up the negotiator that matches a request's "+
					"language preferences against the supported locales: %w", err)
			}
			return register(router, negotiator)
		},
	}
}

func register(router speedhttp.Router, negotiator i18n.Negotiator) error {
	for _, name := range router.Endpoints() {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("i18n.http: endpoint %q is declared but not accessible: %w", name, err)
		}
		endpoint.Use(negotiationLayer(negotiator))
		endpoint.Use(profileLayer())
	}
	return nil
}

func negotiationLayer(negotiator i18n.Negotiator) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     "i18n:accept-language",
		Stage:    speedhttp.StageObserve,
		Order:    -100,
		Provides: []core.Token{(*i18n.LocaleResolver)(nil)},
		Handler: func(c *gin.Context) {
			if _, ok := i18n.FromContext(c.Request.Context()); !ok {
				if locale, ok := negotiator.Negotiate(c.GetHeader("Accept-Language")); ok {
					c.Request = c.Request.WithContext(i18n.WithLocale(c.Request.Context(), locale))
				}
			}
			c.Next()
		},
	}
}

func profileLayer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "i18n:profile-locale",
		Stage: speedhttp.StageIdentify,
		After: []core.Token{(*authn.Gate)(nil)},
		Handler: func(c *gin.Context) {
			ctx := c.Request.Context()
			identity, ok := authn.FromContext(ctx)
			if !ok {
				c.Next()
				return
			}
			if _, has := i18n.FromContext(ctx); !has {
				if locale, ok := identity.Attribute(i18n.LocaleAttributeKey); ok {
					ctx = i18n.WithLocale(ctx, i18n.Locale(locale))
				}
			}
			if _, has := i18n.TimeZoneFromContext(ctx); !has {
				if zone, ok := identity.Attribute(i18n.TimeZoneAttributeKey); ok {
					ctx = i18n.WithTimeZone(ctx, zone)
				}
			}
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		},
	}
}
