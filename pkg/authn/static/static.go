// Package static carries a credential-verifying backend whose subject table comes from configuration. It takes the Basic scheme and reads the user name and password from one credential.
package static

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.static"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = "authn.static"

const backendName = "static"

const basicScheme = "basic"

const reasonClassThrottled = "throttled"

const noWait time.Duration = 0

type backend struct {
	subjects map[string]password.Entry
	cache    *password.Cache
	throttle *password.Throttle
	governor *password.Governor
	check    func(ctx context.Context, e password.Entry, plaintext string, wait time.Duration) (bool, error)

	store  settings.Store
	bus    event.Bus
	audit  audit.Audit
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

var _ authn.Verifier = (*backend)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. Several backends may run at once, and the settings store, logger, event bus and audit are optional.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*event.Bus)(nil), Optional: true},
			{Token: (*audit.Audit)(nil), Optional: true},
			// Optional, because this backend must work in an assembly that never
			// took logging in; declared, because an assembly that did has to have
			// it constructed first, so that New can register the subject table as
			// a literal redaction rule before Init names a logger.
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Resources: []any{schema(), settingsSchema()},

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
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			if err := registerSecrets(reg, cfg.secrets); err != nil {
				return nil, err
			}
			return newBackend(cfg), nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := instance.(*backend)
			if !ok || b == nil {
				return nil
			}
			return b.wire(reg)
		},
	}
}

// registerSecrets registers the subject table's entries as literal redaction
// rules. It runs in New, before Init names a logger, so that the rules cover
// every line the module logs: Named binds the module attribute, and a rule
// governs only what is bound after it.
func registerSecrets(reg *core.Registry, values []string) error {
	if len(values) == 0 {
		return nil
	}
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the subject table: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+".subjects", values...)
	return nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the subject table is read from configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

func prepare(reader config.Reader) (core.Enablement, error) {
	cfg, err := readConfig(reader)
	if err != nil {
		if errors.Is(err, config.ErrMissingRequired) {
			return core.Enablement{}, fmt.Errorf("%w: %w", err, nobodyToVouchFor())
		}
		return core.Enablement{}, err
	}
	if len(cfg.subjects) == 0 {
		return core.Enablement{}, nobodyToVouchFor()
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func nobodyToVouchFor() error {
	return fmt.Errorf("%w: the %s backend has no subject to answer for: %s.%s names none, "+
		"and a backend with no subject authenticates nobody. Write the subjects and their "+
		"password entries under that key to switch this backend on, or leave this backend "+
		"out of the command", authn.ErrInvalidClaim, ModuleName, ConfigNamespace, subjectsItem)
}

func newBackend(cfg resolved) *backend {
	b := &backend{
		subjects: cfg.subjects,
		cache:    password.NewCache(cfg.cacheSize, nil),
		governor: password.NewGovernor(cfg.concurrency, password.DefaultMemoryBudgetKiB),
		logger:   log.Default(),

		reported: make(map[string]string),
	}
	// The table's own subject keys are fixed, so names sprayed at the back-off never push one out.
	fixed := make([]password.Key, 0, len(cfg.subjects))
	for subject := range cfg.subjects {
		fixed = append(fixed, password.Key{Scope: authn.ScopeIdentifier, Key: password.ThrottleKey(subject)})
	}
	b.throttle = password.NewThrottle(b.throttleParams, nil, fixed...)
	b.check = b.governor.Verify
	return b
}

func (b *backend) wire(reg *core.Registry) error {
	store, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		b.store = store
	case errors.Is(err, core.ErrMissingProvider):
	default:
		return fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
	bus, err := core.Resolve[event.Bus](reg)
	switch {
	case err == nil:
		b.bus = bus
	case errors.Is(err, core.ErrMissingProvider):
	default:
		return fmt.Errorf("%s: taking up the event bus: %w", ModuleName, err)
	}
	seam, err := core.Resolve[audit.Audit](reg)
	switch {
	case err == nil:
		b.audit = seam
	case errors.Is(err, core.ErrMissingProvider):
	default:
		return fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		b.logger = delivered.Named(ModuleName)
	}

	names := make([]string, 0, len(b.subjects))
	for name := range b.subjects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := b.subjects[name]
		if entry.Legacy() {
			b.logger.Warn(ModuleName+": a subject's password entry is in the old form, which is read "+
				"and never written. Write it again with static.Hash or static.HashPBKDF2",
				slog.String("entry", name))
		}
		if entry.Params().BelowFloor() {
			b.logger.Warn(ModuleName+": a subject's password entry is below the least OWASP "+
				"recommends, so a copy of the table is cheap to guess against. The entry is read "+
				"as it is",
				slog.String("entry", name), slog.String("algorithm", string(entry.Params().Algorithm)))
		}
	}
	for _, group := range sharedBackoffs(names) {
		b.logger.Warn(ModuleName+": subjects whose names differ only in case share one back-off, "+
			"so failed attempts against one of them hold the others back too. The table is read "+
			"as it is",
			slog.String("entries", strings.Join(group, ",")))
	}
	if b.bus != nil && b.store != nil {
		b.bus.Subscribe(settings.ChangedEventType, ModuleName, b.onSettingsChanged)
	}
	return nil
}

func sharedBackoffs(names []string) [][]string {
	groups := make(map[string][]string, len(names))
	var order []string
	for _, name := range names {
		key := password.ThrottleKey(name)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], name)
	}
	var shared [][]string
	for _, key := range order {
		if len(groups[key]) > 1 {
			shared = append(shared, groups[key])
		}
	}
	return shared
}

// Name reports the name this backend answers under; it is a constant the gateway stamps on every identity.
func (b *backend) Name() string { return backendName }

// Accepts declares the Basic scheme and accounts under the static prefix, and claims neither impersonation nor a challenge.
func (b *backend) Accepts() authn.Accepts {
	return authn.Accepts{
		Schemes:  []string{basicScheme},
		Accounts: authn.StaticPrefix,
	}
}

// Verify checks one Basic credential against the table. A name held back is refused first; a remembered password needs no derivation. Errors are a refusal, an outage, or unavailable capacity.
func (b *backend) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	user, plaintext, ok := basicCredential(c)
	if !ok {
		return authn.Denied(fmt.Errorf("%s: the credential is not the %s scheme with a base64 "+
			"user:password", ModuleName, basicScheme))
	}

	key := password.ThrottleKey(user)
	entry, known := b.subjects[user]
	var ttl time.Duration
	if known {
		// A remembered password is not a guess, so it takes no attempt, though the name is still refused while a back-off stands.
		if wait := b.throttle.Standing(authn.ScopeIdentifier, key); wait > 0 {
			return authn.Denied(&authn.ThrottledError{RetryAfter: wait})
		}
		if ended := ctx.Err(); ended != nil {
			return endedBeforeChecked(ended)
		}
		ttl = b.cacheTTL()
		if b.cache.Hit(ttl, user, entry, plaintext) {
			return passed(user)
		}
	}

	wait, attempt := b.throttle.Reserve(authn.ScopeIdentifier, key)
	if wait > 0 {
		return authn.Denied(&authn.ThrottledError{RetryAfter: wait})
	}

	if ended := ctx.Err(); ended != nil {
		attempt.Refund()
		return endedBeforeChecked(ended)
	}

	if !known {
		b.fail(attempt, "")
		return authn.Denied(fmt.Errorf("%s: the name is not in this table", ModuleName))
	}

	held, err := b.check(ctx, entry, plaintext, noWait)
	switch {
	case errors.Is(err, authn.ErrBusy):
		attempt.Refund()
		return authn.Unavailable(fmt.Errorf("%s: %w", ModuleName, err))
	case err != nil && ctx.Err() != nil:
		attempt.Refund()
		return authn.Unavailable(fmt.Errorf("%s: the call ended before a derivation gave an answer: %w",
			ModuleName, ctx.Err()))
	case err != nil:
		// A derivation the process cannot run is not a match: it counts as a failed attempt.
		b.fail(attempt, user)
		return authn.Denied(fmt.Errorf("%s: the password cannot be checked: %w", ModuleName, err))
	}
	// A derivation that ran to its end counts whether or not the caller still waits: the derivation takes no context.
	if !held {
		b.fail(attempt, user)
		return authn.Denied(fmt.Errorf("%s: the password does not match", ModuleName))
	}
	// A password is remembered from the derivation that vouched for it, not extended by later hits.
	b.cache.Remember(ttl, user, entry, plaintext)
	attempt.Refund()
	return passed(user)
}

func endedBeforeChecked(ended error) authn.Verdict {
	return authn.Unavailable(fmt.Errorf("%s: the call ended before the credential was checked: %w",
		ModuleName, ended))
}

func passed(user string) authn.Verdict {
	return authn.Authenticated(authn.Principal{
		Account:   authn.AccountID(authn.StaticPrefix + user),
		Methods:   []authn.Method{authn.MethodStatic},
		Assurance: authn.AssuranceSingle,
		AuthTime:  clock.Now(),
	})
}

func (b *backend) fail(attempt *password.Attempt, subject string) {
	if attempt.Fail() {
		attrs := []any{slog.String("reason_class", reasonClassThrottled)}
		if subject != "" {
			attrs = append(attrs, slog.String("entry", subject))
		}
		b.logger.Info(ModuleName+": a name has failed enough attempts to be held back; further "+
			"attempts are refused without a check until its back-off ends", attrs...)
	}
}

func basicCredential(c authn.Credential) (user, plaintext string, ok bool) {
	if c.Scheme != basicScheme {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return "", "", false
	}
	user, plaintext, found := strings.Cut(string(raw), ":")
	if !found || user == "" {
		return "", "", false
	}
	return user, plaintext, true
}

func (b *backend) cacheTTL() time.Duration {
	ttl, ok := readSetting(b, cacheTTLPath, cacheTTLInDomain)
	if !ok {
		return defaults.CacheTTL
	}
	return ttl
}

func (b *backend) throttleParams() password.ThrottleParams {
	p := password.ThrottleParams{
		FreeAttempts: defaults.FreeAttempts,
		Window:       defaults.FailWindow,
		MaxBackoff:   defaults.MaxBackoff,
	}
	if v, ok := readSetting(b, freeAttemptsPath, freeAttemptsInDomain); ok {
		p.FreeAttempts = v
	}
	if v, ok := readSetting(b, failWindowPath, failWindowInDomain); ok {
		p.Window = v
	}
	if v, ok := readSetting(b, maxBackoffPath, maxBackoffInDomain); ok {
		p.MaxBackoff = v
	}
	return p
}

func readSetting[T any](b *backend, path string, inDomain func(T) bool) (T, bool) {
	var zero T
	if b.store == nil {
		return zero, false
	}
	value, err := settings.Get[T](b.store, settings.Global(), path)
	switch {
	case err != nil:
		b.reportSetting(path, "the setting cannot be read, so its declared default is used", err.Error())
		return zero, false
	case !inDomain(value):
		b.reportSetting(path, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(value))
		return zero, false
	}
	b.reportSetting(path, "", "")
	return value, true
}

func (b *backend) reportSetting(path, problem, detail string) {
	b.mu.Lock()
	last, reported := b.reported[path]
	switch {
	case problem == "":
		delete(b.reported, path)
	case !reported || last != detail:
		b.reported[path] = detail
	default:
		problem = ""
	}
	b.mu.Unlock()
	if problem != "" {
		b.logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}
