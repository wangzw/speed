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
// none.
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
