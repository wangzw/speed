// Package static carries a credential-verifying backend whose subject table
// comes from configuration.
//
// It is the smallest backend that is actually usable: a fixed set of subjects,
// each with a password entry, read once at startup and never changed afterwards.
// That makes it right for the accounts a deployment has before it has a user
// store, an operator, a probe, a first administrator, and for the way back in
// when the database is down; it is wrong for accounts that come and go, which
// need a backend backed by something writable.
//
// It is the one backend that takes the Basic scheme, and it reads the user name
// and the password out of the credential itself. A subject is the account
// "static." followed by its name, in a namespace no other backend may hand
// accounts back from, so what a subject of this table holds is held by no
// account anyone can sign up.
//
// A host takes it in by writing ModuleName into a command alongside the
// gateway. It delivers the verification capability non-exclusively, so it can
// stand beside backends that take other carriers.
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

// ConfigNamespace is where this module's input items are mounted. A backend
// hangs under authn.<backend>, so that every backend's section sits beside the
// gateway's under one heading.
const ConfigNamespace = "authn.static"

// backendName is the name this backend reports. It is a constant of the code and
// not an input: the gateway lets the static prefix be declared by the backend of
// this name and by no other.
const backendName = "static"

// basicScheme is the authentication scheme this backend takes. It is the one
// whose credential carries both the user name and the password in a place a
// standard fixes (RFC 7617).
const basicScheme = "basic"

// reasonClassThrottled is the reason_class of the line that says a name has
// passed its free attempts. It is one of the closed values the events design
// registers for the field.
const reasonClassThrottled = "throttled"

// noWait is how long a derivation that found no capacity waits for it: no time
// at all. A request that cannot start its check now is answered as an outage and
// the caller asks again, and nothing queues behind the derivations running.
const noWait time.Duration = 0

// backend is this module's product.
type backend struct {
	subjects map[string]password.Entry
	cache    *password.Cache
	throttle *password.Throttle
	governor *password.Governor
	// check derives one entry against one password under the width the
	// module allows, waiting at most the given time for capacity. It is the
	// Governor's Verify and is a field so that a case can tell when a
	// derivation happened, when it did not, and what wait it asked for.
	check func(ctx context.Context, e password.Entry, plaintext string, wait time.Duration) (bool, error)

	// store is the settings store, nil when the assembly has none, in which case
	// every setting reads as its declared default. It is taken up in Init and
	// read-only afterwards.
	store settings.Store
	// bus is the event bus, nil when the assembly has none, in which case a
	// setting moved to a more permissive value is not watched. It is taken up in
	// Init and read-only afterwards.
	bus event.Bus
	// audit is the audit seam, nil when the assembly has none, in which case a
	// relaxed setting is a Warn and only that. It is taken up in Init and
	// read-only afterwards.
	audit audit.Audit
	// logger is where the diagnostics go.
	logger *slog.Logger

	// mu guards reported, which holds for each setting the fault last written,
	// so that a setting read on every request is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

// backend must satisfy the capability it delivers.
var _ authn.Verifier = (*backend)(nil)

// init registers this module with the process registry.
//
// A host chooses its backends by importing them, and what enters its go.mod
// follows the packages it imported: a host that takes only this one carries a
// password hash and no other backend's library — no directory client and no
// database driver in it. The compile closure does hold pkg/settings with what
// that package imports, pkg/db, pkg/event and gorm among them, and importing it
// registers the settings module with the process registry: the settings store
// this backend reads its parameters from is declared in the package that
// carries its implementation. The go.sum and the `go mod tidy` fetch are not held
// to that face — they follow the test closures of the imported packages — and
// whole-graph reads see every library an implementation binds
// (ADR-module-packaging-2026-09-22).
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is left empty, which is what lets several backends run at once, each
// taking its own carriers. Prepare is declared, and declaring it is not optional
// for a backend: a module without one first reads its configuration in New, so a
// missing section is still a startup failure but one construction later, after
// the modules ordered before this one have already been built.
//
// The settings store, the logging capability, the event bus and the audit
// capability are taken up in Init, where every instance already exists, and none
// is required: without the store the four settings read as their declared
// defaults, without logging the diagnostics go to the process default logger,
// and without the bus or the audit a setting moved to a more permissive value is
// not reported. The event bus and the audit are declared as optional
// requirements so that a command naming a provider of either initialises it
// before this backend; an optional requirement nothing delivers adds nothing.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*event.Bus)(nil), Optional: true},
			{Token: (*audit.Audit)(nil), Optional: true},
			// Optional, because this backend has to work in an assembly that
			// never took logging in; declared, because an assembly that did has
			// to have it constructed first, so that New can register the subject
			// table as a literal redaction rule before Init names a logger.
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

// registerSecrets registers the subject table's stored entries as literal
// redaction rules with the logging capability, when the assembly carries one.
//
// Each entry is a password hash, and the table is a secret as a whole: an entry
// that turns up in any log line of the process is masked. The registration runs
// in New, before Init names a logger, which is the timing a redaction rule
// needs — Named binds the module attribute, and a rule governs only what is
// bound after it. With no logging capability there is no log to protect, and the
// absence is told apart from a failed lookup by the sentinel.
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

// readerOf takes up the configuration reader.
//
// Its absence means the host never handed one to the registry, which is core's
// own failure and is carried out as it stands. It is told apart from a missing
// section on purpose: reading the two alike would let a host that forgot the
// reader lose a whole backend in silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the subject table is read from configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// prepare reads the configuration and states that this backend runs.
//
// There is no stance in which it runs with nobody to vouch for. A command that
// names this module has promised it starts, so a table that names no subject is
// a startup failure naming the key to write: a backend with no subject
// authenticates nobody, and the gateway refuses it rather than carrying it along.
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

// nobodyToVouchFor is the failure this backend reports when its table holds no
// subject.
//
// An absent table and an empty one are the same fact — this backend would
// answer for nobody — and are worth the same sentence. It is a startup failure
// rather than a stance: a module taken into a command has to start, and the way
// out is to write the subjects, or to leave this backend out of the command whose
// traffic it was meant to authenticate.
//
// It wraps the sentinel the gateway refuses a declaration with, because an
// empty table is a backend that takes a carrier and has nobody behind it.
func nobodyToVouchFor() error {
	return fmt.Errorf("%w: the %s backend has no subject to answer for: %s.%s names none, "+
		"and a backend with no subject authenticates nobody. Write the subjects and their "+
		"password entries under that key to switch this backend on, or leave this backend "+
		"out of the command", authn.ErrInvalidClaim, ModuleName, ConfigNamespace, subjectsItem)
}

// newBackend builds the product: the table, and the three pieces of state a
// check goes through, each sized once.
func newBackend(cfg resolved) *backend {
	b := &backend{
		subjects: cfg.subjects,
		cache:    password.NewCache(cfg.cacheSize, nil),
		governor: password.NewGovernor(cfg.concurrency, password.DefaultMemoryBudgetKiB),
		logger:   log.Default(),

		reported: make(map[string]string),
	}
	// The keys of the subjects the table holds are fixed: names sprayed at the
	// back-off never push one of them out of it.
	fixed := make([]password.Key, 0, len(cfg.subjects))
	for subject := range cfg.subjects {
		fixed = append(fixed, password.Key{Scope: authn.ScopeIdentifier, Key: password.ThrottleKey(subject)})
	}
	b.throttle = password.NewThrottle(b.throttleParams, nil, fixed...)
	b.check = b.governor.Verify
	return b
}

// wire takes up what is optional and writes the startup diagnostics: the
// settings store, the event bus, the audit seam, the logger, a line for each
// entry the table holds that is written the old way or below what OWASP
// recommends, and a line for each set of names that share one back-off. It also
// subscribes to the settings change event when both the bus and the store are
// present.
func (b *backend) wire(reg *core.Registry) error {
	store, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		b.store = store
	case errors.Is(err, core.ErrMissingProvider):
		// No settings in this assembly: the four parameters read as their
		// declared defaults.
	default:
		return fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
	bus, err := core.Resolve[event.Bus](reg)
	switch {
	case err == nil:
		b.bus = bus
	case errors.Is(err, core.ErrMissingProvider):
		// No bus in this assembly: a setting moved to a more permissive value
		// is not watched.
	default:
		return fmt.Errorf("%s: taking up the event bus: %w", ModuleName, err)
	}
	seam, err := core.Resolve[audit.Audit](reg)
	switch {
	case err == nil:
		b.audit = seam
	case errors.Is(err, core.ErrMissingProvider):
		// No audit in this assembly: a relaxed setting is a Warn and only that.
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
	// A line about the table names an entry under the attribute entry. The log
	// has no field called subject: elsewhere in the library the word means a
	// bound external identity.
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
	// A change to one of this module's settings is watched only when both the
	// bus that carries the event and the store the change is about are present:
	// without either there is nothing to hear, or nothing this backend reads.
	if b.bus != nil && b.store != nil {
		b.bus.Subscribe(settings.ChangedEventType, ModuleName, b.onSettingsChanged)
	}
	return nil
}

// sharedBackoffs groups the names that fall under one back-off key, which are
// the names that differ only in case, and returns the groups of more than one
// name in the order of their first member. The back-off folds case because a
// caller must not start its count over by changing the spelling of a name,
// while the table matches names exactly, so a table may hold two names that the
// back-off cannot tell apart.
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

// Name reports the name this backend answers under. It is a constant: the
// gateway stamps it on every identity this backend authenticates, and lets the
// static account prefix be declared by the backend of this name alone.
func (b *backend) Name() string { return backendName }

// Accepts declares what this backend takes and what it may hand back: the Basic
// scheme, and accounts under the static prefix. It claims neither impersonation
// nor SSO.
func (b *backend) Accepts() authn.Accepts {
	return authn.Accepts{
		Schemes:  []string{basicScheme},
		Accounts: authn.StaticPrefix,
	}
}

// Verify checks one Basic credential against the table.
//
// The order is what keeps a check cheap for whoever has none to make. A
// credential that is not a user and a password is refused at once. A name that
// is being held back is refused at once, before anything else is looked at. A
// password that held lately and is remembered is accepted without a derivation
// and without an attempt: it cannot be a guess, because only a password that
// already held is remembered, so it is not counted against the free attempts of
// its name and is not held against the attempts of others, however many arrive
// together. A name that is let through to a derivation has this attempt counted
// against it from that moment, as a failure, in the same step that read the
// wait: attempts on one name that arrive together are held against one another,
// so the attempts a name may make before it is held back are its free attempts
// and not however many requests are in flight. The count is settled when the
// outcome is known. A password that holds gives back that one attempt and no
// failure that came before it, and a success clears nothing: failures only age,
// because a caller who could clear them with a request of its own between two
// guesses would never be held back. A password that fails keeps its attempt as a
// failure.
//
// A call whose context has ended is answered as an outage before anything is
// recorded, and holds no attempt. A name that is not in the table is refused
// without a derivation, which is what lets the names be told from each other by
// the time a refusal takes, and the table has always accepted that cost. For a
// name that is there, any password that is not remembered is derived against its
// entry under the capacity the module allows: a number of derivations at once,
// and a budget of memory that the derivations of memory-hard entries draw on.
//
// The back-off counts the name presented, whether or not it is in the table, so
// that holding a name back says nothing about whether the name exists. It is
// kept in this process, and not in the database: this table is the way in when
// the database is down.
//
// It answers Unavailable when it cannot check: when the context has ended before
// a derivation began, and when no capacity is free, because the width is taken
// or the memory budget is. Neither is a failed attempt, and neither keeps its
// reservation. The table lives in memory, so there is no other moment at which
// this backend is present and unable to say. The standard library's derivation
// takes no context and runs to completion once begun, so what one attempt costs
// once it has begun is bounded by the cost the entry states and by the capacity,
// not by the call budget.
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
		// A password that is remembered is not a guess, so it takes no attempt.
		// The name is still refused while the failures that stand hold it back.
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
	// From here the attempt is settled exactly once, on every path out.

	// A call whose time is gone checks nothing and changes nothing: it is
	// neither a refusal nor a failed attempt, and the back-off is left as it was.
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
		// A derivation the process cannot run, such as one a FIPS 140 mode
		// refuses for this password, is not a match. It counts as a failed
		// attempt and does not read as an outage.
		b.fail(attempt, user)
		return authn.Denied(fmt.Errorf("%s: the password cannot be checked: %w", ModuleName, err))
	}
	// A derivation that ran to its end has an answer whether or not the call
	// is still there to hear it: the derivation takes no context, and a wrong
	// password whose caller left is still a failed attempt, or leaving would
	// be a way to try passwords without being counted.
	if !held {
		b.fail(attempt, user)
		return authn.Denied(fmt.Errorf("%s: the password does not match", ModuleName))
	}
	// A password is remembered from the derivation that vouched for it and not
	// from the hits that follow: the lifetime is absolute and runs from that
	// derivation, so a password presented more often than the lifetime is still
	// derived again once per lifetime.
	b.cache.Remember(ttl, user, entry, plaintext)
	attempt.Refund()
	return passed(user)
}

// endedBeforeChecked is the answer for a call whose context had ended before the
// password was checked.
func endedBeforeChecked(ended error) authn.Verdict {
	return authn.Unavailable(fmt.Errorf("%s: the call ended before the credential was checked: %w",
		ModuleName, ended))
}

// passed is the verdict for a password that held. The account is the name
// under the static prefix, and the authentication time is now: every request
// carries its own password, so each one is an authentication of its own.
func passed(user string) authn.Verdict {
	return authn.Authenticated(authn.Principal{
		Account:   authn.AccountID(authn.StaticPrefix + user),
		Methods:   []authn.Method{authn.MethodStatic},
		Assurance: authn.AssuranceSingle,
		AuthTime:  clock.Now(),
	})
}

// fail keeps the attempt as a failure against the name. The line that says a name
// has been held back carries the reason class of a security event of the
// back-off, and names a subject of the table and nobody else: a name that is not
// in the table is whatever a caller typed.
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

// basicCredential splits a Basic credential into the name it presents and the
// password it offers, at the first colon. A credential under any other scheme,
// one that is not base64 of a pair, and one with no name are refused.
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

// cacheTTL reads how long a check that held is remembered, as the setting says,
// or as it is declared when the setting cannot be read or is outside its domain.
// The lifetime is absolute: it runs from the derivation that remembered the
// check, and the hits that follow do not extend it.
func (b *backend) cacheTTL() time.Duration {
	ttl, ok := readSetting(b, cacheTTLPath, cacheTTLInDomain)
	if !ok {
		return defaults.CacheTTL
	}
	return ttl
}

// throttleParams reads the three back-off settings, each as it says or as it is
// declared when it cannot be read or is outside its domain.
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

// readSetting reads one setting from the store under the type it is declared
// with, and reports whether the value is one the code acts on. An absent store
// is the declared default and says nothing. A read that fails and a value
// outside the domain, which the store judges when it is written and not when it
// is read, both read as no value, so the caller falls back to the declared
// default, and the reading is reported at Error.
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

// reportSetting writes the Error line for a setting that was read as its default
// when something is wrong with it. A setting is read on every request, so the
// same fault is written once and again only when it changes or has been cleared
// in between; an empty problem clears it.
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
