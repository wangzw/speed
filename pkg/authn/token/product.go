package token

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// The routes this module mounts, relative to the mounter's base path. The two
// POST endpoints are registered by the piece that builds the token endpoint; the
// key set is the one this piece mounts. All three are a stable closed set: a
// reader outside the module writes them down.
const (
	tokenPath  = "/token"
	revokePath = "/token/revoke"
)

// refreshTokenPrefix starts every refresh token. It makes the credential
// recognisable to the log redactors and secret scanners, and is part of the
// stable surface. tokenBytes is the randomness behind it, in bytes.
const (
	refreshTokenPrefix = "spd_rt_"
	tokenBytes         = 32
)

// The instruments this module reports through. meterName is the meter the
// capability's counters are taken from; the instrument names and the mandatory
// attribute are the stable ones a dashboard reads.
const (
	meterName               = "authn"
	operationsInstrument    = "authn.token.operations"
	auditFailuresInstrument = "authn.audit.write-failures"
	mandatoryAttribute      = "mandatory"
)

// diagnostic is a caution collected while the keys were loaded, written once at
// startup: a private key file other users can read, which the module can point
// out but not undo.
type diagnostic struct {
	file   string
	reason string
}

// meters are the instruments this module reports through. The zero value records
// nothing, which is what an assembly without a meter provider gets.
type meters struct {
	operations    metric.Int64Counter
	auditFailures metric.Int64Counter
}

// refreshStore reads and writes the authn_refresh_tokens table. handle is the
// database handle, dialect the engine behind it, and now the clock every time
// written to a row is read from. It never touches the table before the
// migrations have been applied.
type refreshStore struct {
	handle  *gorm.DB
	dialect db.Dialect
	now     clock.Source
}

// product is this module's instance. One value satisfies every interface the
// module delivers. The fields hold the configuration, the loaded key set, the
// required capabilities, the optional ones taken up in Init, and the shared
// working state the pieces of the module build on.
type product struct {
	cfg  resolved
	keys *keySet

	handle   *gorm.DB
	dialect  db.Dialect
	mounter  mount.Mounter
	clock    clock.Source
	logger   *slog.Logger
	settings *settingReader

	accounts  authn.Accounts
	sessions  authn.Sessions
	passwords authn.Passwords
	throttle  authn.Throttle

	// The optional capabilities, taken up in Init and nil when the assembly has
	// none of them.
	impersonation authn.Impersonation
	claims        []authn.ClaimsContributor
	bus           event.Bus
	audit         audit.Audit
	meters        meters

	refresh *refreshStore

	// cacheSlot holds the two read-through caches the verifying backend serves
	// from, rebuilt when check-ttl changes.
	cacheSlot atomic.Pointer[cacheSet]

	// seen remembers the identifiers of the cache-clearing events the
	// subscriptions handled, so a redelivery is a no-op.
	seen *seenEvents

	// sweepRuns counts the sweep goroutines currently running for this product:
	// it is one while the sweep is up and zero after Stop, so a second start
	// that launched a second goroutine would be observable.
	sweepRuns atomic.Int64

	// diagnostics are the startup cautions written by diagnose.
	diagnostics []diagnostic
}

// newProduct builds the product over the configuration, the loaded key set and
// the capabilities resolved for it. The optional capabilities are taken up in
// wire; the clock is the system clock unless a test replaces it.
func newProduct(cfg resolved, keys *keySet, database db.Database, mounter mount.Mounter,
	accounts authn.Accounts, sessions authn.Sessions, passwords authn.Passwords, throttle authn.Throttle,
) *product {
	var now clock.Source
	p := &product{
		cfg:       cfg,
		keys:      keys,
		handle:    database.DB(),
		dialect:   database.Dialect(),
		mounter:   mounter,
		clock:     now,
		logger:    log.Default(),
		accounts:  accounts,
		sessions:  sessions,
		passwords: passwords,
		throttle:  throttle,
		refresh:   &refreshStore{handle: database.DB(), dialect: database.Dialect(), now: now},
		seen:      newSeenEvents(),
	}
	p.settings = newSettingReader(p.logger)
	return p
}

// wire takes up what is optional, writes the startup diagnostics and mounts the
// routes. It runs in Init, where every instance exists.
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
	p.claims = core.ResolveAll[authn.ClaimsContributor](reg)

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
	if recorder, err := core.Resolve[audit.Audit](reg); err == nil {
		p.audit = recorder
	} else if !errors.Is(err, core.ErrMissingProvider) {
		return fmt.Errorf("%s%s: taking up the audit capability: %w", diagnosticPrefix, ModuleName, err)
	}
	if err := p.takeMeter(reg); err != nil {
		return err
	}

	p.subscribe()
	p.diagnose()
	p.mountRoutes()
	return nil
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
		metric.WithDescription("Token operations, by the operation"))
	if err != nil {
		return fmt.Errorf("%s%s: registering the %s counter: %w", diagnosticPrefix, ModuleName,
			operationsInstrument, err)
	}
	failures, err := provider.Meter(meterName).Int64Counter(auditFailuresInstrument,
		metric.WithUnit("{failure}"),
		metric.WithDescription("Audit rows that could not be written, by whether the write was mandatory"))
	if err != nil {
		return fmt.Errorf("%s%s: registering the %s counter: %w", diagnosticPrefix, ModuleName,
			auditFailuresInstrument, err)
	}
	p.meters = meters{operations: counter, auditFailures: failures}
	return nil
}

// recordAuditFailure counts one best-effort audit write that failed on the
// module's meter. None of this module's audit rows is mandatory, so the
// attribute is always false; it is carried so this counter joins the one the
// other modules report their audit failures under. When the assembly carries no
// meter provider the counter is nil and nothing is recorded.
func (p *product) recordAuditFailure(ctx context.Context) {
	if p.meters.auditFailures == nil {
		return
	}
	p.meters.auditFailures.Add(context.WithoutCancel(ctx), 1,
		metric.WithAttributes(attribute.Bool(mandatoryAttribute, false)))
}

// diagnose writes the startup lines: one Warn for each caution the keys brought
// with them and for the relaxation switch, and one Info for each optional
// capability that is absent, saying what does not happen without it.
func (p *product) diagnose() {
	for _, d := range p.diagnostics {
		p.logger.Warn(diagnosticPrefix+ModuleName+": "+d.reason, slog.String("file", d.file))
	}
	if !p.cfg.checkSession {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the session check is off, so a revocation reaches a normal "+
			"token only at its expiry. This is for a deployment that runs the token backend stateless",
			slog.String("setting", ConfigNamespace+"."+checkSessionItem))
	}
	if p.keys.signing == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no signing key is configured, so the module verifies " +
			"tokens and issues none")
	}
	if p.impersonation == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no impersonation capability in this command, so a " +
			"token that names an impersonation record is refused")
	}
	if p.settings.store == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no settings store in this command, so every token " +
			"setting reads as its declared default")
	}
	if p.bus == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no event bus in this command, so a reuse detection " +
			"reaches no subscriber")
	}
}

// mountRoutes registers the routes this module mounts: the key set on its
// public route, and the token and revocation endpoints on their callback route.
func (p *product) mountRoutes() {
	p.mountJWKS()
	p.mountTokenRoutes()
}

// newRefreshToken makes the credential of a refresh token: the prefix followed
// by tokenBytes random bytes in base64url. The plaintext is handed to the client
// once and only its digest is stored.
func newRefreshToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("%s: reading randomness for a refresh token: %w", ModuleName, err)
	}
	return refreshTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// digestRefreshToken is the digest a refresh token is stored and looked up by:
// the hex SHA-256 of the plaintext. The store never holds the token itself.
func digestRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
