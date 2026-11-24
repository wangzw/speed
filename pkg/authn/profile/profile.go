// Package profile is the authentication capability's user-profile self-service
// module: it mounts GET and PATCH /me and delivers no capability of its own.
package profile

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.profile"

const defaultEndpoint = "account"

//go:embed openapi/profile.json
var profileFragment []byte

var profileSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: profileFragment,
}

const diagnosticPrefix = "authn: "

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It requires the route mounter and
// the account read, write and signup-verification surfaces; the audit and log
// capabilities are optional.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*audit.Audit)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			profileSpec,
			operations,
		},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			p.diagnose()
			return nil
		},

		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	mounter            mount.Mounter
	accounts           authn.Accounts
	signupVerification authn.SignupVerification
	accountAdm         authn.AccountAdmin

	audit  audit.Audit
	logger *slog.Logger
}

func newProduct(reg *core.Registry) (*product, error) {
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the route mounter: %w", diagnosticPrefix, ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account reads: %w", diagnosticPrefix, ModuleName, err)
	}
	signupVerification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the signup verification reads: %w", diagnosticPrefix, ModuleName, err)
	}
	accountAdm, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account writes: %w", diagnosticPrefix, ModuleName, err)
	}
	logger := log.Default()
	return &product{
		mounter:            mounter,
		accounts:           accounts,
		signupVerification: signupVerification,
		accountAdm:         accountAdm,
		logger:             logger,
	}, nil
}

func optional[T any](reg *core.Registry, what string) (T, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, nil
	default:
		return zero, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
	}
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	auditCap, err := optional[audit.Audit](reg, "the audit capability")
	if err != nil {
		return err
	}
	p.audit = auditCap
	p.mountRoutes()
	return nil
}

func (p *product) diagnose() {
	p.logger.Info(diagnosticPrefix + ModuleName + ": the profile routes are mounted under " +
		p.mounter.BaseIn(speedhttp.Scope("")) + "/me on the endpoint " + p.mounter.Endpoint())
}
