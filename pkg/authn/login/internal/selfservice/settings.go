package selfservice

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// The settings this package reads, each with the default its declaring module
// states and the domain that module holds a written value to. authn.mfa owns
// the first and authn.sms the other two; this package reads them and declares
// none. authn.login owns the email-code pair, which the email self-service route
// reads and this package declares none of either.
const (
	keyRecentWindow     = "authn.mfa.recent-window"
	defaultRecentWindow = 10 * time.Minute
	minRecentWindow     = time.Minute
	maxRecentWindow     = time.Hour

	keyCodeTTL     = "authn.sms.code-ttl"
	defaultCodeTTL = 5 * time.Minute
	minCodeTTL     = time.Minute
	maxCodeTTL     = 10 * time.Minute

	keyResendInterval     = "authn.sms.resend-interval"
	defaultResendInterval = 60 * time.Second
	minResendInterval     = 30 * time.Second
	maxResendInterval     = 10 * time.Minute

	keyEmailCodeTTL     = "authn.login.email-code-ttl"
	defaultEmailCodeTTL = 10 * time.Minute
	minEmailCodeTTL     = time.Minute
	maxEmailCodeTTL     = 30 * time.Minute

	keyEmailResendInterval     = "authn.login.email-resend-interval"
	defaultEmailResendInterval = 60 * time.Second
	minEmailResendInterval     = 30 * time.Second
	maxEmailResendInterval     = 10 * time.Minute

	// The email verification code budgets. authn.login declares them and
	// this package reads them into the domains the declaring module states.
	keyEmailPerAddressHourly     = "authn.login.email-per-address-hourly"
	defaultEmailPerAddressHourly = 5
	minEmailPerAddressHourly     = 1
	maxEmailPerAddressHourly     = 60

	keyEmailPerAddressDaily     = "authn.login.email-per-address-daily"
	defaultEmailPerAddressDaily = 10
	minEmailPerAddressDaily     = 1
	maxEmailPerAddressDaily     = 200

	keyEmailPerAccountHourly     = "authn.login.email-per-account-hourly"
	defaultEmailPerAccountHourly = 10
	minEmailPerAccountHourly     = 1
	maxEmailPerAccountHourly     = 60

	keyEmailPerAccountDaily     = "authn.login.email-per-account-daily"
	defaultEmailPerAccountDaily = 100
	minEmailPerAccountDaily     = 1
	maxEmailPerAccountDaily     = 1000

	keyEmailPerSourceHourly     = "authn.login.email-per-source-hourly"
	defaultEmailPerSourceHourly = 30
	minEmailPerSourceHourly     = 1
	maxEmailPerSourceHourly     = 10000

	keyEmailPerSourceDaily     = "authn.login.email-per-source-daily"
	defaultEmailPerSourceDaily = 100
	minEmailPerSourceDaily     = 1
	maxEmailPerSourceDaily     = 100000

	keyEmailGlobalHourly     = "authn.login.email-global-hourly"
	defaultEmailGlobalHourly = 300
	minEmailGlobalHourly     = 1
	maxEmailGlobalHourly     = 1000000
)

// durations reads duration settings from the store. A read that fails and a
// value outside the setting's domain, which the store judges when it is written
// and not when it is read, both read as the declared default, and the fault is
// written at Error. The same fault is written once and again only when it
// changes or has been cleared in between, because a setting is read on every
// request.
type durations struct {
	store settings.Store

	mu       sync.Mutex
	reported map[string]string
}

func newDurations(store settings.Store) *durations {
	return &durations{store: store, reported: make(map[string]string)}
}

// get reads key as a duration in [lo, hi] and falls back to def.
func (d *durations) get(ctx context.Context, key string, def, lo, hi time.Duration) time.Duration {
	if d.store == nil {
		return def
	}
	v, err := settings.Get[time.Duration](d.store, settings.Global(), key)
	switch {
	case err != nil:
		d.report(ctx, key, "the setting cannot be read, so its declared default is used", err.Error())
		return def
	case v < lo || v > hi:
		d.report(ctx, key, "the setting holds a value outside its domain, so its declared default is used", "")
		return def
	}
	d.report(ctx, key, "", "")
	return v
}

// counts reads whole-number settings from the store, with the same fallback as
// durations: a read that fails and a value outside the setting's domain both
// read as the declared default, and the fault is written at Error once per
// distinct fault.
type counts struct {
	store settings.Store

	mu       sync.Mutex
	reported map[string]string
}

func newCounts(store settings.Store) *counts {
	return &counts{store: store, reported: make(map[string]string)}
}

// get reads key as a whole number in [lo, hi] and falls back to def.
func (c *counts) get(ctx context.Context, key string, def, lo, hi int) int {
	if c.store == nil {
		return def
	}
	v, err := settings.Get[int](c.store, settings.Global(), key)
	switch {
	case err != nil:
		c.report(ctx, key, "the setting cannot be read, so its declared default is used", err.Error())
		return def
	case v < lo || v > hi:
		c.report(ctx, key, "the setting holds a value outside its domain, so its declared default is used", "")
		return def
	}
	c.report(ctx, key, "", "")
	return v
}

func (c *counts) report(ctx context.Context, key, problem, cause string) {
	c.mu.Lock()
	last, was := c.reported[key]
	switch {
	case problem == "":
		delete(c.reported, key)
		c.mu.Unlock()
		return
	case was && last == problem:
		c.mu.Unlock()
		return
	}
	c.reported[key] = problem
	c.mu.Unlock()
	attrs := []any{slog.String("key", key)}
	if cause != "" {
		attrs = append(attrs, slog.String("cause", cause))
	}
	log.FromContext(ctx).Error(moduleName+": "+problem, attrs...)
}

func (d *durations) report(ctx context.Context, key, problem, cause string) {
	d.mu.Lock()
	last, was := d.reported[key]
	switch {
	case problem == "":
		delete(d.reported, key)
		d.mu.Unlock()
		return
	case was && last == problem:
		d.mu.Unlock()
		return
	}
	d.reported[key] = problem
	d.mu.Unlock()
	attrs := []any{slog.String("key", key)}
	if cause != "" {
		attrs = append(attrs, slog.String("cause", cause))
	}
	log.FromContext(ctx).Error(moduleName+": "+problem, attrs...)
}
