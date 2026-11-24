package admin

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const recentWindowPath = "authn.mfa.recent-window"

const (
	defaultRecentWindow = 10 * time.Minute
	minRecentWindow     = time.Minute
	maxRecentWindow     = time.Hour
)

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger}
}

func (r *reader) recentWindow() time.Duration {
	if r.store == nil {
		return defaultRecentWindow
	}
	v, err := settings.Get[time.Duration](r.store, settings.Global(), recentWindowPath)
	switch {
	case err != nil:
		r.report("the setting cannot be read, so its declared default is used", err.Error())
		return defaultRecentWindow
	case v < minRecentWindow || v > maxRecentWindow:
		r.report("the setting holds a value outside its domain, so its declared default is used", fmt.Sprint(v))
		return defaultRecentWindow
	}
	r.report("", "")
	return v
}

func (r *reader) report(problem, detail string) {
	r.mu.Lock()
	last := r.reported
	switch {
	case problem == "":
		r.reported = ""
	case last != detail:
		r.reported = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", recentWindowPath))
	}
}
