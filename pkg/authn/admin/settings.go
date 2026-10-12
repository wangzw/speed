package admin

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// recentWindowPath is the setting this module reads. It is the multi-factor
// module's key, read from the global layer by every consumer of the freshness
// requirement, and it is declared by that module; this module declares no key
// of its own.
const recentWindowPath = "authn.mfa.recent-window"

// The bounds and the default of the freshness window, held the same as the
// multi-factor module holds them. A read that fails or a value outside the
// bounds falls back to the default and is reported at Error.
const (
	defaultRecentWindow = 10 * time.Minute
	minRecentWindow     = time.Minute
	maxRecentWindow     = time.Hour
)

// reader reads the freshness window with the multi-factor module's failure
// direction. The store is nil when the assembly has none, and the setting then
// reads as its declared default, which is a legal assembly.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, so that a fault on a setting read on every request is
	// written once and again only when it changes.
	mu       sync.Mutex
	reported string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger}
}

// recentWindow reads the freshness window. A read that fails, and a value
// outside its domain, read as the declared default and are reported at Error;
// an unread key (the multi-factor module not being in the assembly) is the
// default without a report, since a route that needs the window is mounted only
// with that module.
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

// report writes the Error line of a read that is not as the setting says. An
// empty problem clears the last report.
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
