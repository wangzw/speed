// Package google carries the Google login method: the first factor that proves
// an external identity by an OpenID Connect authorization-code flow against
// Google.
//
// The method answers with a Step and does nothing else. In Begin it draws the
// state, the PKCE verifier and the nonce, keeps them in its private bytes as
// the flow record's method state, and answers the authorization URL. In Answer
// it checks that the redirect came back with the state it drew, exchanges the
// authorization code for an identity token, verifies the token through go-oidc,
// and checks the nonce, the authorized party and the hosted domain itself. It
// touches no HTTP route, session, token or flow record apart from the outbound
// requests it makes: each of those — discovery, the key set, the token exchange
// — leaves one record through the outbound logging transport, naming the
// method, host, path, status, outcome and time and never the query, where an
// authorization code, a token and a state travel. The engine logs the cause of
// an answer, and no cause here names an authorization code, a token, a state or
// a nonce.
//
// Every outbound request the method makes goes through the restricted egress
// client, so a discovery document, a key set and a token endpoint can only be
// reached over https at a public address, and each call is bounded by the
// deadline of the context the engine hands in. Discovery is lazy and cached:
// the provider is discovered on the first call that needs it, a failure ends
// that call with an outage and is not cached, and the method answers an outage
// until the discovery succeeds.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine. It delivers the login method capability non-exclusively, so it
// stands beside the other methods.
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.google"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and a published name
// never changes.
const methodName = "google"

// providerName is the external provider this method keys identities under. It is
// the method's own name, in the namespace of the external bindings.
const providerName = "google"

// defaultIssuer is the OpenID Connect issuer the provider is discovered at when
// the configuration names none.
const defaultIssuer = "https://accounts.google.com"

// callbackPath is where the engine mounts the redirect the provider sends the
// user agent back to; the redirect URI is built from the public address and the
// base path, and the provider's own callback path is the method's name, so the
// redirect a different provider holds cannot be aimed here.
const callbackPath = "/callback/" + methodName

// diagnosticPrefix starts every line the authentication capability writes to the
// startup diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// product must satisfy the capability it delivers.
var _ authn.LoginMethod = (*product)(nil)

// Module is the descriptor a host registers.
//
// The method requires no functional capability of another module: the provider's
// discovery document, key set and token endpoint are reached over the restricted
// egress client, which this module builds itself, and the configuration reader is
// resolved where it is used and is not declared. The logging capability is taken
// up as an optional requirement, because the client secret is registered as a
// literal redaction rule and an assembly with no logging has nothing to register
// it with. The login method capability is delivered without being claimed, which
// is what lets several methods stand side by side. The stance is StateEnabled and
// never StateAuto: the module is taken in at run level and has no way to stand
// itself down.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},
	}
}

// product is the module's instance. It delivers the login method.
type product struct {
	config   resolved
	client   *http.Client
	provider *providerCache
}

// newProduct reads this module's configuration and the gateway's public address,
// registers the client secret with the logging capability and builds the
// restricted egress client the provider is reached through.
func newProduct(reg *core.Registry) (*product, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	if err := registerSecret(reg, cfg.clientSecret); err != nil {
		return nil, err
	}
	client := egress.New(egress.Options{AllowLoopback: cfg.allowLoopback, RootCAs: cfg.rootCAs})
	// The restricted client is wrapped from outside, so a discovery, key-set or
	// token request carries the span of the call's context across the hop to the
	// provider; internal/egress offers no constructor of its own for it. The
	// client is wrapped for the outbound record as well, inside the span
	// injection: every request this method makes leaves one record naming the
	// endpoint, the status and the time and carrying neither the query — where
	// an authorization code, a token and a state travel — nor a body.
	client.Transport = observability.Transport(log.Transport(client.Transport))
	return newMethod(cfg, client), nil
}

// registerSecret registers the client secret as a literal redaction rule, so it
// is masked wherever it turns up in a log line. An assembly with no logging has
// nothing to register it with and registers nothing; a registration after a
// logger has bound an attribute would not reach what was bound, so this runs in
// New, before any logger this module would take.
func registerSecret(reg *core.Registry, secret string) error {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the client secret "+
			"with: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+"."+clientSecretItem, secret)
	return nil
}

// newMethod assembles the method from a resolved configuration and the client
// its outbound requests go through. The provider cache keeps the client and the
// issuer, so discovery goes through the same restricted client as every other
// call.
func newMethod(cfg resolved, client *http.Client) *product {
	return &product{
		config:   cfg,
		client:   client,
		provider: &providerCache{issuer: cfg.issuer, client: client},
	}
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured": the
// client identifier, the secret and the gateway's public address are all
// required.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the client credentials and the redirect target come from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
