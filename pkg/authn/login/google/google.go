// Package google carries the Google login method: an OpenID Connect authorization-code flow
// against Google, delivered as one authn.LoginMethod that touches no route, session, token or
// flow record apart from its outbound requests.
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

// methodName is the name this method reports; a published name never changes.
const methodName = "google"

const providerName = "google"

const defaultIssuer = "https://accounts.google.com"

// callbackPath is where the engine mounts the provider's redirect. The provider's own
// callback path is the method's name, so another provider's redirect cannot be aimed here.
const callbackPath = "/callback/" + methodName

const diagnosticPrefix = "authn: "

func init() { core.ProcessRegistry.Register(Module()) }

var _ authn.LoginMethod = (*product)(nil)

// Module is the descriptor a host registers. It requires no functional capability of another
// module; logging is optional, for the client secret's redaction rule. Prepare stands the
// method down when neither credential is set, fails on one alone, and validates the rest.
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
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},
	}
}

// prepare decides whether this run includes the Google method: neither credential stands it
// down and says why, one credential alone stops the start and names the missing item, and both
// let the rest of the configuration decide.
func prepare(reader config.Reader) (core.Enablement, error) {
	idGiven, err := credentialGiven(reader, &clientIDOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	secretGiven, err := credentialGiven(reader, &clientSecretOnly{})
	if err != nil {
		return core.Enablement{}, err
	}
	switch {
	case !idGiven && !secretGiven:
		return core.Enablement{
			State: core.StateDisabled,
			Reason: "no client-id and no client-secret were given, so no Google OAuth application " +
				"is registered and the Google sign-in method is not offered",
		}, nil
	case !idGiven:
		return core.Enablement{}, halfConfigured(clientIDItem)
	case !secretGiven:
		return core.Enablement{}, halfConfigured(clientSecretItem)
	}
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

type product struct {
	config   resolved
	client   *http.Client
	provider *providerCache
}

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
	// The restricted client is wrapped outside for the span and inside for the outbound record,
	// which names the method, host, path, status, outcome and time and never the query or body.
	client.Transport = observability.Transport(log.Transport(client.Transport))
	return newMethod(cfg, client), nil
}

// registerSecret registers the client secret as a literal redaction rule. It runs in New,
// before a logger is bound, because a rule governs only what is bound after it.
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

func newMethod(cfg resolved, client *http.Client) *product {
	return &product{
		config:   cfg,
		client:   client,
		provider: &providerCache{issuer: cfg.issuer, client: client},
	}
}

// readerOf takes up the configuration reader; its absence is the host's omission and is
// carried out as it stands.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the client credentials and the redirect target come from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
