// Package session carries the login sessions of the identity system: the
// browser session behind a cookie, the token session behind an access token and a
// refresh token, and the one row of each service-account secret. All three share
// one table, authn_sessions, and one revocation surface.
//
// The module delivers the session store (authn.Sessions) exclusively, and
// delivers a credential-verifying backend that recognises the session cookie
// (authn.Verifier, non-exclusively) and the hook that cleans up after an account
// is disabled or deleted (authn.AccountLifecycle, non-exclusively). It is the
// only place the session cookie is named, written, cleared and read.
//
// The session library, scs, is used for what it gets right: the token, its
// digest in the store, the cookie's attributes and the creation path. The adapter
// that stores rows, the public identifier, the revocation, the period of each
// kind, the node-local caches and the token rotation are this package's. The
// library appears in this package and in no signature of the interface package.
//
// Nothing here writes a token, a cookie value or a password into a log line, an
// error text, an event or a metric attribute. A token is held as its digest from
// the moment it is read.
package session

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexedwards/scs/v2"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose users log in.
const ModuleName = "authn.session"

// defaultEndpoint is the endpoint this module's own OpenAPI fragment declares its
// routes for. The endpoint the routes are actually mounted on is the one the
// mounter resolves; the fragment is a static declaration written before that is
// known, so it carries the conventional name.
const defaultEndpoint = "account"

// sessionFragment is this module's OpenAPI fragment, describing the five routes
// it mounts.
//
//go:embed openapi/session.json
var sessionFragment []byte

// sessionSpec is the API root this module's fragment and routes hang off. Its
// prefix and version are the common ones; the fragment writes the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below this root.
var sessionSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: sessionFragment,
}

// ConfigNamespace is where this module's input items and settings are mounted.
const ConfigNamespace = ModuleName

// backendName is the name this backend reports. It is a constant of the code and
// not an input: the gateway stamps it on every identity this backend vouches for.
const backendName = "session"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so that the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// product is this module's instance. One value satisfies every interface the
// module delivers.
type product struct {
	cookie   cookieSpec
	handle   *gorm.DB
	accounts authn.Accounts
	mounter  mount.Mounter
	sm       *scs.SessionManager
	clock    clock.Source
	logger   *slog.Logger
	settings *settingReader

	// impersonation, bus, audit and operations are taken up in Init and nil when
	// the assembly has none of them.
	impersonation authn.Impersonation
	bus           event.Bus
	audit         audit.Audit
	operations    metric.Int64Counter

	// cookieCtx is a context carrying an empty library session, which the
	// library's cookie writer reads a flag from.
	cookieCtx context.Context

	cacheSlot atomic.Pointer[cacheSet]
	seen      *recentEvents

	sweepMu sync.Mutex
	sweep   *sweeper
	// sweepEvery is how often the sweep runs.
	sweepEvery sweepPace
}

// product must satisfy everything the module delivers.
var (
	_ authn.Sessions         = (*product)(nil)
	_ authn.Verifier         = (*product)(nil)
	_ authn.CookieClearer    = (*product)(nil)
	_ authn.AccountLifecycle = (*product)(nil)
)

// init registers this module with the process registry. Importing the package is
// what puts the session store in a binary's available set, as importing a
// backend is what gives a gateway something to ask.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The three required capabilities are the database the table is in, the mounter
// the routes are registered through, and the account store the validity of a
// session is judged against. A command that takes this module in without one of
// them fails at resolution, naming it. The impersonation capability, the settings
// store, the event bus, the logger and the meter are taken up in Init: each has a
// stated behaviour for its absence, so none is declared. The audit capability is
// taken up in Init too, and is declared as an optional requirement only so that a
// command carrying one initialises it before this module; its absence stays a
// legal assembly, in which a relaxed setting writes the Warn alone and no record.
//
// Sessions is delivered exclusively, and the stance is StateEnabled and never
// StateAuto: an exclusive provider that states StateAuto is the cell resolution
// disables, which would let one session store yield to another. A module a drive
// includes has to start, so a configuration it cannot run on is an error from
// Prepare.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*audit.Audit)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.Sessions)(nil), Exclusive: true},
			{Token: (*authn.Verifier)(nil)},
			{Token: (*authn.AccountLifecycle)(nil)},
		},
		Resources: []any{
			schema(), settingsSchema(),
			db.Migrations{FS: migrationsFS},
			sessionSpec,
			operations,
		},

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
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cookie, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, err
			}
			mounter, err := core.Resolve[mount.Mounter](reg)
			if err != nil {
				return nil, err
			}
			return newProduct(cookie, database.DB(), accounts, mounter), nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx)
		},

		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.stop(ctx)
			}
			return nil
		},
	}
}

// productOf recovers this module's product from what the registry hands back. A
// nil instance or one of another type is tolerated: the host may have supplied an
// instance of its own, and every callback leaves it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the cookie is read from configuration, and no reader was handed "+
			"to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// newProduct builds the product over the handle, the account store and the
// mounter. The library's manager is configured here, once, as the design's
// adaptation rules S1 to S6 say.
func newProduct(cookie cookieSpec, handle *gorm.DB, accounts authn.Accounts, mounter mount.Mounter) *product {
	p := &product{
		cookie:     cookie,
		handle:     handle,
		accounts:   accounts,
		mounter:    mounter,
		logger:     log.Default(),
		seen:       newRecentEvents(recentEventsCapacity),
		sweepEvery: sweepPace{interval: sweepInterval},
	}
	p.settings = newSettingReader(p.logger)
	p.sm = newManager(p, cookie)
	// The library's cookie writer asks the context whether the session was
	// marked to persist, and needs a library session in it to answer. Loading an
	// empty token makes one without touching the store.
	ctx, err := p.sm.Load(context.Background(), "")
	if err != nil {
		// An empty token is not looked up, so this cannot fail; a failure would
		// leave every cookie write without a context to read.
		panic(ModuleName + ": loading an empty session: " + err.Error())
	}
	p.cookieCtx = ctx
	return p
}

// newManager configures the library's session manager. S1: the lifetime is the
// largest the settings allow, the idle timeout is off, tokens are stored as
// their digest and the codec is JSON.
func newManager(p *product, cookie cookieSpec) *scs.SessionManager {
	sm := scs.New()
	sm.Lifetime = lifetimeUpperBound
	sm.IdleTimeout = 0
	sm.HashTokenInStore = true
	sm.Codec = jsonCodec{}
	sm.Store = storeAdapter{p: p}
	// The library writes the session cookie from its own description of it,
	// which takes every attribute from the cookie cookiename.Cookie builds.
	c := cookie.cookie("", 0)
	sm.Cookie = scs.SessionCookie{
		Name:        c.Name,
		Domain:      c.Domain,
		HttpOnly:    c.HttpOnly,
		Path:        c.Path,
		SameSite:    c.SameSite,
		Secure:      c.Secure,
		Partitioned: false,
		Persist:     false,
	}
	return sm
}

// now is the instant this module reads the time at, in UTC.
func (p *product) now() time.Time { return p.clock.Now() }

// wire takes up what is optional, writes the startup diagnostics, subscribes to
// the events and registers the routes. It runs in Init, where every instance
// exists.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
		p.settings.logger = p.logger
	}

	if imp, err := core.Resolve[authn.Impersonation](reg); err == nil {
		p.impersonation = imp
	} else if !errors.Is(err, core.ErrMissingProvider) {
		return fmt.Errorf("%s%s: taking up the impersonation capability: %w", diagnosticPrefix, ModuleName, err)
	}
	if store, err := core.Resolve[settings.Store](reg); err == nil {
		p.settings.store = store
	} else if !errors.Is(err, core.ErrMissingProvider) {
		return fmt.Errorf("%s%s: taking up the settings store: %w", diagnosticPrefix, ModuleName, err)
	}
	if bus, err := core.Resolve[event.Bus](reg); err == nil {
		p.bus = bus
	} else if !errors.Is(err, core.ErrMissingProvider) {
		return fmt.Errorf("%s%s: taking up the event bus: %w", diagnosticPrefix, ModuleName, err)
	}
	if auditor, err := core.Resolve[audit.Audit](reg); err == nil {
		p.audit = auditor
	} else if !errors.Is(err, core.ErrMissingProvider) {
		return fmt.Errorf("%s%s: taking up the audit capability: %w", diagnosticPrefix, ModuleName, err)
	}
	if err := p.takeMeter(reg); err != nil {
		return err
	}

	p.diagnose()
	p.subscribe()
	p.mountRoutes()
	return nil
}

// diagnose writes the startup lines: one Warn for each protection the
// configuration gives up, and one Info for each optional capability that is
// absent, saying what does not happen without it.
func (p *product) diagnose() {
	if p.cookie.domain != "" {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the session cookie carries a Domain, so it is named "+
			"__Secure-authn-sid and any subdomain can set it; the host-only guarantee of the __Host- prefix is given up",
			slog.String("setting", ConfigNamespace+"."+cookieDomainItem))
	}
	if p.impersonation == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no impersonation capability in this command, so a " +
			"session that names an impersonation record is refused")
	}
	if p.settings.store == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no settings store in this command, so every session " +
			"setting reads as its declared default")
	}
	if p.bus == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no event bus in this command, so a revocation reaches " +
			"another replica only when its cache entry runs out, within check-ttl")
	}
}

// takeMeter registers the operations counter, when the assembly carries a meter
// provider. A failure other than the provider's absence stops the startup rather
// than leaving the counts unreported without a word.
func (p *product) takeMeter(reg *core.Registry) error {
	provider, err := core.Resolve[observability.MeterProvider](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil
		}
		return fmt.Errorf("%s%s: taking up the metric capability: %w", diagnosticPrefix, ModuleName, err)
	}
	counter, err := provider.Meter(meterName).Int64Counter(operationsInstrument,
		metric.WithUnit("{operation}"),
		metric.WithDescription("Session operations, by the operation"))
	if err != nil {
		return fmt.Errorf("%s%s: registering the %s counter: %w", diagnosticPrefix, ModuleName,
			operationsInstrument, err)
	}
	p.operations = counter
	return nil
}

// caches is the cache set for the current value of check-ttl. A change of the
// setting replaces the set, so a lifetime that was lowered takes effect at once
// and one that was raised does not keep serving from entries made under the
// shorter one.
func (p *product) caches() *cacheSet {
	ttl := p.settings.checkTTL()
	for {
		cs := p.cacheSlot.Load()
		if cs != nil && cs.ttl == ttl {
			return cs
		}
		fresh := newCacheSet(ttl, p.clock.Now)
		if p.cacheSlot.CompareAndSwap(cs, fresh) {
			return fresh
		}
	}
}
