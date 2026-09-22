// Package gate carries the authentication gateway: the module that registers a
// middleware layer on the configured listening endpoints, decides who each
// request's caller is, and closes the decision into a pass or a status code.
//
// It holds the ownership table that says which credential-verifying backend is
// the authority for which subject, and the per-endpoint exemption table that
// says which routes go through without a credential. A host takes the gateway
// in by writing ModuleName into a command, together with at least one backend;
// the gateway itself implements no credential check.
//
// Nothing here is reached through the interface package. A registrant reaches
// the exemption surface as authn.Gate, taken up from the registry, and a backend
// answers through authn.Verifier; both are in the parent package, which is what
// keeps a module that registers an exemption or delivers a backend from
// depending on this one.
package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose traffic is authenticated.
const ModuleName = "authn.gate"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, the way a registering module's namespace always is, so
// that each backend's section sits beside this one under the capability
// directory's heading and no module owns that heading itself.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the capability directory
// rather than the module: a reader scanning the diagnostics is looking for what
// authentication did, and the gateway is the only part of it that writes here.
const diagnosticPrefix = "authn: "

// gateway is this module's product. It implements authn.Gate.
type gateway struct {
	settings settings

	// mu guards the exemption registration window and what is registered
	// through it. Both are written from other modules' callbacks, which may
	// register from a goroutine of their own, so the order of the writes is
	// not that of the drive.
	mu       sync.Mutex
	phase    phase
	declared []exemption

	// router is taken up in Init and read again in Start, where the
	// registered endpoint names are checked against it.
	router speedhttp.Router
	// table is built in Init and read-only afterwards.
	table *ownership
	// matchers are built in Start, one per endpoint that carries an
	// exemption, and read-only afterwards. An endpoint with no exemption
	// has no entry, and the nil matcher exempts nothing.
	matchers map[string]*matcher

	// logger is where the startup diagnostics go. Request-path records go
	// to the logger the entry point injected into the request context
	// instead, so that they carry that request's own correlation fields.
	logger *slog.Logger

	// slots is the in-flight gate: one token per call allowed to be inside
	// a backend at once.
	slots chan struct{}
	// callsMu guards stopped and the joining half of inFlight, so that a
	// call can never join after Stop has returned.
	callsMu  sync.Mutex
	stopped  bool
	inFlight sync.WaitGroup

	// counts are the four buckets every request-time conclusion lands in,
	// indexed by outcome.
	counts [4]atomic.Int64
}

// gateway must satisfy the capability it delivers.
var _ authn.Gate = (*gateway)(nil)

// init registers this module with the process registry.
//
// Registration travels with the import, which is what makes "does this binary
// have an authentication entry at all" a question the host answers by importing
// this subpackage. The interface package registers nothing, so the backends
// that implement its interfaces and the business code that reads an identity
// out of a context carry no module in with them.
//
// The cost is that an import is enough: a dependency that reaches this package
// for any reason puts the gateway in the available set, where a command taking
// in every available module will assemble it.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Requires is empty, and that is a decision rather than an omission. Both the
// entry point and the backends are taken up in Init, where every instance
// already exists, so there is nothing for a construction-time requirement to
// order. Declaring one would also make the closure of a command that includes
// this module pull in whichever provider happens to be registered, which is the
// host's choice to write down, not this module's to force.
//
// The capability is delivered exclusively and the stance is StateEnabled. Two
// gateways on one endpoint would each register a layer and each decide the same
// request, so the pair is deliberate: resolution only stands a provider down
// when it claims exclusivity and states StateAuto, and a second gateway should
// fail the startup rather than quietly lose its registrations.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Provides:  []core.Provision{{Token: (*authn.Gate)(nil), Exclusive: true}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			set, err := readSettings(reader)
			if err != nil {
				return nil, err
			}
			return newGateway(set), nil
		},

		// The gate is opened in Migrate and sealed in Start, the two
		// stage slots the design's stage table gives it. The window that
		// leaves open contains the Init stage whole and reaches beyond it
		// at both ends, because a stage advances one module at a time:
		// this module's Migrate callback runs before the assembly enters
		// Init, and its Start callback runs once Init has ended. The rest
		// of the Migrate stage, and the Start callbacks ordered ahead of
		// this module, lie inside it too, and a registration from any of
		// them is accepted and takes effect.
		// Neither end could be this module's own Init callback — a module
		// that never declared a dependency on Gate may legally resolve it
		// and register from an Init that runs before this one. This
		// module migrates nothing and has no service of its own to start,
		// so the two slots have no other use.
		//
		// Whether either slot runs is the depth of the drive to decide,
		// and the two are tied by nothing. A drive shallower than Migrate
		// runs neither and reaches no Init: both sides of the window are
		// absent together and the result is consistent. A drive that stops
		// exactly at Migrate opens the gate with nobody to seal it, and it
		// stays open for the rest of the process; that run has no Init, no
		// registration and no traffic. The way round it is for a command
		// whose purpose is to apply migrations not to take this module in.
		Migrate: func(_ context.Context, _ *core.Registry, instance any) error {
			if g, ok := productOf(instance); ok {
				g.open()
			}
			return nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return g.wire(reg)
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return g.freeze()
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if g, ok := productOf(instance); ok {
				g.stop()
			}
			return nil
		},

		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			g, ok := productOf(instance)
			if !ok {
				return nil
			}
			return g.drain(ctx)
		},
	}
}

// productOf recovers this module's product from what the registry hands back.
// A nil instance, or one of another type, is tolerated rather than asserted: a
// startup that fails part-way runs Stop and Close over every constructed
// instance whatever stage it reached.
func productOf(instance any) (*gateway, bool) {
	g, ok := instance.(*gateway)
	return g, ok && g != nil
}

// readerOf takes up the configuration reader.
//
// Its absence means the host never handed one to the registry before the drive
// began, which is core's own failure and is carried out as it stands: the fix is
// the host's, not a defect this module judged. It is deliberately not read as
// "nothing was configured" — that reading would let a host missing the reader
// lose the whole authentication layer in silence, which is the same hole as a
// zero verdict read as a pass.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%sthe endpoints this gateway guards are read from "+
			"configuration, and no reader was handed to the registry before the assembly "+
			"began: %w", diagnosticPrefix, err)
	}
	return reader, nil
}

// prepare reads the configuration and states that this module runs.
//
// There is no stance in which this gateway guards nothing. A command that names
// this module has promised it starts, so a configuration that names no endpoint
// to guard is a startup failure naming the key to write: registering on no
// endpoint would leave every route on every endpoint served without a
// credential, and which traffic is public is not a guess this module makes for
// the host.
func prepare(reader config.Reader) (core.Enablement, error) {
	set, err := readSettings(reader)
	if err != nil {
		if errors.Is(err, config.ErrMissingRequired) {
			return core.Enablement{}, fmt.Errorf("%w: %w", err, nothingToGuard())
		}
		return core.Enablement{}, err
	}
	if len(set.endpoints) == 0 {
		return core.Enablement{}, nothingToGuard()
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// nothingToGuard is the failure this module reports when the configuration
// names no endpoint for it to guard.
//
// The absent key and an explicitly empty list are the same fact — no endpoint is
// guarded — and are worth the same sentence. It is a startup failure rather than
// a stance: a module taken into a command has to start, and the way out is to
// write the endpoints, or to leave this module out of the command.
func nothingToGuard() error {
	return fmt.Errorf("no endpoint is guarded: %s.%s names none, so every route would be "+
		"served without a credential. Name the endpoints to guard under that key to switch "+
		"authentication on, or leave this module out of the command",
		ConfigNamespace, endpointsItem)
}

// newGateway builds the product. It takes up no module's product: everything
// this gateway reaches is taken up in Init, where every instance exists.
func newGateway(set settings) *gateway {
	return &gateway{
		settings: set,
		slots:    make(chan struct{}, set.inFlight),
		logger:   log.Default(),
	}
}

// wire runs in Init: it registers the middleware on every guarded endpoint,
// builds the ownership table, and registers the credential redaction rules.
func (g *gateway) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return fmt.Errorf("%w: this command includes authentication but no HTTP entry "+
				"point, so there is nothing to authenticate. Write the entry "+
				"implementation subpackage's module name into this command",
				authn.ErrNoRouter)
		}
		return fmt.Errorf("%staking up the HTTP entry point to register on: %w",
			diagnosticPrefix, err)
	}
	g.router = router

	verifiers := core.ResolveAll[authn.Verifier](reg)
	if len(verifiers) == 0 {
		return fmt.Errorf("%w: this command includes the authentication gateway but not one "+
			"credential-verifying backend, so no credential could ever hold. Write the "+
			"chosen backend subpackage's module name into this command", authn.ErrNoVerifier)
	}
	table, err := buildOwnership(verifiers)
	if err != nil {
		return err
	}
	g.table = table

	// The layers are registered only now. Each is built from the ownership
	// table — the challenge it answers a refusal with names the schemes in
	// it — so the table has to exist first. Registering earlier reads as
	// harmless and is not: it takes the table while it is still nil.
	for _, name := range g.settings.endpoints {
		endpoint, missing := router.Endpoint(name)
		if missing != nil {
			// Told apart the way the exemption check tells them
			// apart. Router is an interface, and its contract says a
			// name that was never declared wraps ErrUnknownEndpoint —
			// not that nothing else can go wrong. Folding every
			// failure into "correct the name" would send a reader to
			// a configuration key that is already right.
			if errors.Is(missing, speedhttp.ErrUnknownEndpoint) {
				return fmt.Errorf("%w: %s.%s names the endpoint %q, which the entry point "+
					"does not have. Correct the name, or declare that endpoint in "+
					"the entry point's configuration", authn.ErrUnknownEndpoint,
					ConfigNamespace, endpointsItem, name)
			}
			return fmt.Errorf("%staking up the endpoint %q named by %s.%s: %w",
				diagnosticPrefix, name, ConfigNamespace, endpointsItem, missing)
		}
		endpoint.Use(g.middlewareFor(name))
	}

	g.takeLogger(reg)
	g.logger.Info(diagnosticPrefix+"the credential-verifying backends taking part",
		slog.String("backends", strings.Join(table.names(), ", ")),
		slog.String("endpoints", describeGuarded(g.settings.endpoints)))
	return nil
}

// takeLogger takes up the logging capability for the startup diagnostics and
// registers the credential redaction rules.
//
// Logging is not a dependency of this module. The subject is bound as a
// correlation field through log.WithAttrs, a package-level function that goes
// nowhere near the registry; only registering the redaction rules needs the
// capability's instance, and registering them changes no authentication
// behaviour, so their absence changes none either.
//
// The registration is here rather than in New because taking the instance in New
// would mean either writing logging into Requires or betting on construction
// order, and this module's Requires is empty on purpose.
func (g *gateway) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		// No logging module in this assembly. There is nowhere to
		// register the rules, and the diagnostics go to the process
		// default logger.
		return
	}
	g.logger = delivered.Named(ModuleName)
	registerRedaction(delivered.Redaction(), g.table.schemes())
}

// freeze runs in Start: it shuts the registration window, validates what was
// registered and builds the matchers, then writes the frozen table out.
func (g *gateway) freeze() error {
	if err := g.seal(g.router); err != nil {
		return err
	}
	g.logger.Info(diagnosticPrefix+"the exemption table, frozen", exemptionAttrs(g.matchers)...)
	return nil
}

// exemptionAttrs renders the frozen table for the diagnostics. It is written
// unconditionally on the success path: a route that is public is worth saying
// out loud once at startup, whether or not anyone registered one.
func exemptionAttrs(matchers map[string]*matcher) []any {
	if len(matchers) == 0 {
		return []any{slog.String("exemptions", "none: every route on every guarded endpoint "+
			"requires a credential")}
	}
	attrs := make([]any, 0, len(matchers))
	names := make([]string, 0, len(matchers))
	for name := range matchers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		attrs = append(attrs, slog.String(name, strings.Join(matchers[name].patterns, ", ")))
	}
	return attrs
}

// stop runs in Stop: no further backend call may join.
func (g *gateway) stop() {
	g.callsMu.Lock()
	g.stopped = true
	g.callsMu.Unlock()
}

// drain runs in Close: it waits for the calls already inside a backend, bounded
// by the call budget, which is the longest any one of them should still take.
//
// The waiter below outlives this function when the bound is reached, and that is
// the best available answer rather than an oversight: a WaitGroup cannot be
// abandoned, and the only alternative is to keep Close waiting on a backend that
// has stopped answering, which would hold the whole shutdown open. The goroutine
// ends when those calls end, and Close runs once per process, so nothing
// accumulates.
func (g *gateway) drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.inFlight.Wait()
		close(done)
	}()

	timer := time.NewTimer(g.settings.budget)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%sbackend calls were still in flight after the call budget of %s",
			diagnosticPrefix, g.settings.budget)
	case <-ctx.Done():
		return fmt.Errorf("%swaiting for in-flight backend calls: %w", diagnosticPrefix, ctx.Err())
	}
}

// tally reports the four counters, for the tests and for whoever reads them
// back. The bucket names are fixed by the outcome's own String.
func (g *gateway) tally() map[string]int64 {
	out := make(map[string]int64, len(g.counts))
	for i := range g.counts {
		out[verdict.Outcome(i).String()] = g.counts[i].Load()
	}
	return out
}

// challengeFor builds the WWW-Authenticate value the 401s on one endpoint
// carry.
//
// The schemes are the ones backends claim whole, plus the one the gateway reads
// a subject out of itself: together those are exactly the schemes under which a
// credential can arrive and reach a backend at all. Each is a token, which
// newClaimant checks when the claim is made, so taking a scheme's first byte
// below lands on a whole character.
//
// The realm is the endpoint's name. Basic requires one (RFC 7617), and a realm
// is the name of a protection space — which is what an endpoint is here, since
// what a gateway guards is named per endpoint and a caller's credentials are
// worth exactly as much as the endpoint they were issued for. Inventing a word
// instead would put a name of this module's choosing into every client that
// keeps credentials per realm.
func challengeFor(table *ownership, endpoint string) string {
	named := map[string]struct{}{canonicalScheme(basicScheme): {}}
	for _, scheme := range table.schemes() {
		named[canonicalScheme(scheme)] = struct{}{}
	}
	out := make([]string, 0, len(named))
	for name := range named {
		out = append(out, name+` realm="`+quoteRealm(endpoint)+`"`)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// quoteRealm escapes what goes inside the quoted realm string. An endpoint name
// comes from configuration, and a quote or a backslash in one would otherwise
// end the quoted string early and let the rest of the name be read as further
// challenge parameters.
func quoteRealm(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch c := name[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\r', '\n':
			// A line ending would not be an escape but a second
			// header; it is dropped rather than escaped.
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// canonicalScheme renders a folded scheme name the way a challenge writes it.
// Scheme names are case-insensitive, so this is presentation alone.
func canonicalScheme(scheme string) string {
	if scheme == "" {
		return scheme
	}
	return strings.ToUpper(scheme[:1]) + scheme[1:]
}
