// Package http wires the i18n capability into an entry point's middleware
// chain. It registers a module named "i18n.http" that, in its Init, adds two
// layers to every endpoint the entry point declared: one that negotiates the
// request's language preferences into the request context, and one that fills
// in the language and time zone a signed-in user stored, for a request whose
// header named none.
//
// The layers are contributed from outside the entry point module rather than
// built into it. The entry point knows no capability — it orders the layers
// modules hand it by the stages and constraints they declare and never reads
// what they do — so settling a language belongs to this capability, registered
// through the surface the entry point offers every module.
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

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// Both dependencies are required. The router is the surface the layers are
// registered through; the negotiator holds the set of supported locales the
// header is matched against. Without either there is nothing to register and no
// set to match, so a run that takes this module in must supply them.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*i18n.Negotiator)(nil)},
		},

		// The layers are registered during Init, inside the writing window the
		// entry point opens in its Migrate callback and closes in its Start
		// callback. Every instance exists by Init, so both capabilities resolve
		// here.
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

// register adds both layers to every declared endpoint.
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

// negotiationLayer settles the request's locale from its language preferences.
//
// It is an observing layer, the outside of that stage, so that a refusal by any
// later layer — a guard's 429, the identify stage's 401 — is already rendered
// in the language the request asked for. It stands for i18n.LocaleResolver on
// the chain, so another module may order a layer against it with After or
// Before.
func negotiationLayer(negotiator i18n.Negotiator) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     "i18n:accept-language",
		Stage:    speedhttp.StageObserve,
		Position: speedhttp.PositionFirst,
		Provides: []core.Token{(*i18n.LocaleResolver)(nil)},
		Handler: func(c *gin.Context) {
			// A locale already in the context — one a host wrote before the
			// chain, or another layer wrote — is left alone.
			if _, ok := i18n.FromContext(c.Request.Context()); !ok {
				if locale, ok := negotiator.Negotiate(c.GetHeader("Accept-Language")); ok {
					c.Request = c.Request.WithContext(i18n.WithLocale(c.Request.Context(), locale))
				}
			}
			c.Next()
		},
	}
}

// profileLayer fills in the language and time zone a signed-in user stored.
//
// It runs in the identify stage, inside the gateway: the gateway stamps the
// identity onto the request context, and the stored preferences travel on that
// identity — the account's attribute set is merged into it when a session is
// verified, so this layer reads no store on the request path. It writes nothing
// the context already carries, so a language the header named stands.
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
