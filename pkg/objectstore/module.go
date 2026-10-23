package objectstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Spec is what a backend subpackage hands this package to become a module.
//
// It is the whole of what a backend contributes. Everything else — the exclusive
// delivery of the byte seam, mounting the input-item declaration under a
// namespace of the backend's own, decoding the configuration, resolving the
// optional logging capability, registering the backend's credentials as literal
// redaction rules, and opening the store — is this package's, shared by every
// backend.
//
// The type parameter is the backend's own configuration carrier. Each backend
// accepts different items — a local root directory, or an endpoint and a bucket
// — so there is no shared carrier to hand here.
type Spec[C any] struct {
	// ModuleName is this backend's name in the registry: its identity in the
	// startup diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this backend's input items are mounted in the
	// config data. It has to be one of its own: the metadata module owns
	// objectstore.metadata, and the configuration manifest is collected from
	// every registered module, so two backends declaring input items on one path
	// conflict as soon as a host imports both.
	ConfigNamespace string
	// Config is a pointer to this backend's input-item prototype: the struct
	// config fills, carrying the values an absent key takes. The schema reads the
	// accepted keys and their defaults from it.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and states what this backend cannot
	// start without. A backend taken in at run level has to start, so the state
	// it returns is StateEnabled: two backends enabled at once fail the startup
	// instead of one silently winning the byte seam. Everything it cannot start
	// without — a value the loader's required-item declaration does not already
	// enforce — is an error naming what is wrong, and the startup fails with it.
	Stance func(cfg *C) (core.Enablement, error)
	// Secrets returns the values this backend holds that have to be registered as
	// literal redaction rules, so a credential that turns up in any log line of
	// the process is masked. It is the backend's way to name a credential without
	// importing the literal package: this package registers what it returns.
	//
	// It is optional; a backend with no credential of its own leaves it nil, and
	// a backend whose credentials come only from configuration returns what it
	// decoded.
	Secrets func(cfg *C) []string
	// Direct says the value Open returns also implements DirectURL. The module
	// then declares that capability beside the byte seam, so an assembly can
	// resolve it and the factory's delivery check covers it.
	//
	// The instance stays the one value Open returns, implementing both seams —
	// the one-module-one-instance-many-tokens pattern the SMS, authorization
	// policy and audit journal modules follow.
	Direct bool
	// Open builds this backend from the decoded configuration. It returns the one
	// value that implements Store, and DirectURL as well when Direct is set.
	//
	// logger is where the backend reports the calls it makes to the service, and
	// is the assembly's logging capability named after this module when the
	// assembly delivers one, the logging module's process default otherwise. A
	// backend holds it rather than reading one from each operation's context: the
	// background sweep calls Delete on a context it created, which carries no
	// injected logger, so FromContext would put that path's records on the
	// default chain instead of the configured destinations.
	//
	// The factory registers the backend's credentials as literal redaction rules
	// before this is reached, so a logger bound here is bound after the rules
	// exist.
	Open func(ctx context.Context, cfg *C, logger *slog.Logger) (Store, error)
}

// NewModule builds a backend's module descriptor.
//
// A defective Spec panics here rather than being reported: a subpackage fills it
// in at init, so the fault is in this repository's own code, shows up
// deterministically on the first run of any host that imports it, and leaves a
// host nothing to handle.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	provides := []core.Provision{{Token: (*Store)(nil), Exclusive: true}}
	if spec.Direct {
		// The direct-address capability is declared beside the byte seam, so an
		// assembly resolves it and the factory's delivery check is covered too.
		// Both tokens name the one instance Open returns.
		provides = append(provides, core.Provision{Token: (*DirectURL)(nil), Exclusive: true})
	}
	return core.Module{
		Name: spec.ModuleName,
		// Optional, because a backend has to work in an assembly that never took
		// logging in; declared, because a backend's credential is registered as a
		// literal redaction rule and an assembly with logging has to have it built
		// first.
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		// The byte seam is delivered exclusively: one process has exactly one
		// answer to where bytes land, so two backends at once fail the startup
		// instead of resolution letting one silently win. A backend that also
		// mints direct addresses appends that capability to the same claim.
		Provides:  provides,
		Resources: []any{spec.schema()},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return spec.Stance(cfg)
		},
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return nil, err
			}
			if err = spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			logger, err := spec.logger(reg)
			if err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg, logger)
		},
	}
}

// check refuses a Spec that cannot produce a working module. Each condition is
// one this package would otherwise meet much later and much further from its
// cause.
func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("objectstore: a Spec carries the module name this backend registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("objectstore: module %q gives no configuration namespace, and its input items "+
			"have to hang under one of its own: backends sharing a namespace conflict as soon as a host "+
			"imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no configuration prototype, which is what its "+
			"accepted input items and their defaults are read from; a backend that accepts nothing still "+
			"mounts an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no stance, so nothing states what it cannot "+
			"start without", s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no constructor, so nothing reaches its backend",
			s.ModuleName))
	}
}

// schema is this backend's input-item declaration, mounted under its own
// namespace.
func (s Spec[C]) schema() config.Schema {
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{{Value: s.Config}},
		Items:     s.Items,
	}
}

// read hands back a fresh copy of the prototype with the backend's own section
// decoded into it.
//
// The copy matters because Prepare and New each read, and a shared carrier would
// have the second read writing over the first one's values. Decode leaves a
// field no source gave a value for as it found it, so the copy is what carries
// the defaults into the result.
func (s Spec[C]) read(reg *core.Registry) (*C, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: this backend's input items are read from configuration, and the host "+
			"handed no reading face into the registry before assembly began: %w", s.ModuleName, err)
	}
	cfg := new(C)
	*cfg = *s.Config
	if err := reader.Decode(s.ConfigNamespace, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// registerSecrets registers the backend's credentials as literal redaction rules
// with the logging capability, when it names any and the assembly carries one.
//
// A credential has no shape to match, so the only thing known about it is the
// value; without a rule it would be masked only where the code remembered to,
// and a failed request would carry it into a log line. The registration runs in
// New, before the backend binds any attribute to its logger, which is the timing
// a redaction rule needs. With no logging capability there is no log to protect,
// and the absence is told apart from a failed lookup by the sentinel.
func (s Spec[C]) registerSecrets(reg *core.Registry, cfg *C) error {
	if s.Secrets == nil {
		return nil
	}
	values := s.Secrets(cfg)
	if len(values) == 0 {
		return nil
	}
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register its credentials: %w",
			s.ModuleName, err)
	}
	literal.Register(delivered.Redaction(), s.ModuleName+".credentials", values...)
	return nil
}

// logger takes up the logging capability when the assembly delivers one, and
// answers the process default logger otherwise.
//
// The returned logger carries this backend's name, which is what lets a line
// about an object-store call be told apart from every other module's once it
// reaches a shared destination. An absence is a legal assembly and is told
// apart from a failed lookup by the sentinel: anything other than "no provider"
// is the lookup itself failing, and that ends the startup rather than quietly
// sending the backend's records somewhere the host did not configure.
//
// It runs after the credentials are registered and before Open, which is the
// order a redaction rule needs: a rule governs what is bound after it, and Named
// binds the module attribute.
func (s Spec[C]) logger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case err == nil:
		return delivered.Named(s.ModuleName), nil
	case errors.Is(err, core.ErrMissingProvider):
		return log.Default(), nil
	default:
		return nil, fmt.Errorf("%s: taking up the logger its calls are reported through: %w",
			s.ModuleName, err)
	}
}
