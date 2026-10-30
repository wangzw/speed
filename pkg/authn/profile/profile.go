// Package profile is the authentication capability's user-profile self-service
// module: it mounts GET B/me and PATCH B/me under the base path B and delivers
// no capability of its own.
//
// The account is the one the request's identity names, which the gateway left on
// the request; no path parameter carries an account identifier. The authority to
// read and write it is the one fact this module can check for itself — the
// caller is that account — so no Decider is asked and no permission point is
// required. A request through this surface that carries no identity, or an
// identity with no account, is answered 401.
//
// The module holds no data, no rule and no store of its own. It reads the
// account, its identifiers, its external bindings and its email verification
// state through the Accounts and SignupVerification interfaces, and writes the
// display name and the attribute set through AccountAdmin, mapping what those
// return to responses. The writable set is
// closed: the display name, the attribute set, the language and the time zone.
// The account's status, its identifiers and its external bindings are not
// writable here. The language and the time zone are named uses of the attribute
// set, under the keys the i18n package declares; nothing new is stored.
//
// Both routes are impersonable: an impersonating identity reads and writes the
// impersonated account's profile exactly as the account's own request does, and
// the write handler keeps no impersonation refusal of its own. A write needs a
// login session; it asks for no freshness beyond that, so a profile is changed
// by the account's holder at any point in the session's life.
//
// The module is taken in only by a command that wants this surface. Without it
// /me is not mounted, and the account is still read and written through its Go
// interfaces by the host's own commands. It states StateEnabled and never
// StateAuto: whether the profile surface exists is a decision on the command
// tree, not a conclusion a configuration can infer.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the profile routes.
const ModuleName = "authn.profile"

// defaultEndpoint is the endpoint this module's own OpenAPI fragment declares its
// routes for. The endpoint the routes are actually mounted on is the one the
// mounter resolves; the fragment is a static declaration written before that is
// known, so it carries the conventional name.
const defaultEndpoint = "account"

// profileFragment is this module's OpenAPI fragment, describing the two routes it
// mounts. It is generated from the declarations in operations.go, not written by
// hand.
//
//go:embed openapi/profile.json
var profileFragment []byte

// profileSpec is the API root this module's fragment and routes hang off.
var profileSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: profileFragment,
}

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers; it is the gateway's, so that a reader
// scanning for what authentication did finds both.
const diagnosticPrefix = "authn: "

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the profile
// surface" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The route mounter, the account read and write surfaces and the signup
// verification reads are required, and each is delivered exclusively by another
// module of the directory, so the closure of a command that takes this one in
// does not pull them in: the host writes them down.
//
// The audit capability and the logging capability are optional and taken up in
// Init: the absence of the audit capability drops the write's best-effort audit
// row; the logging capability orders this module after logging is assembled.
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

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capability; this module delivers none, so a supplied instance is
// left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the required capabilities and the
// optional ones taken up in Init.
type product struct {
	mounter            mount.Mounter
	accounts           authn.Accounts
	signupVerification authn.SignupVerification
	accountAdm         authn.AccountAdmin

	// The optional capabilities. audit is nil when the assembly delivers none,
	// and the write's best-effort audit row is then not written.
	audit  audit.Audit
	logger *slog.Logger
}

// newProduct takes up what the descriptor requires.
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

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
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

// wire runs in Init, where every instance exists. It takes up the optional
// capabilities and the logger and mounts the routes.
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

// diagnose writes the start diagnostic: the base path the profile routes were
// mounted under. The line is the module's own, so that a reader finds what the
// profile surface did without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(diagnosticPrefix + ModuleName + ": the profile routes are mounted under " +
		p.mounter.BaseIn(speedhttp.Scope("")) + "/me on the endpoint " + p.mounter.Endpoint())
}
