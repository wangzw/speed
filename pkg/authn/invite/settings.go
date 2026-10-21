package invite

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// ttl is the lifetime an invitation is issued with when the call states no
// deadline. It is the declared default when the assembly has no settings store,
// when the read fails and when the value is outside the domain, which the store
// judges when a value is written and not when it is read. A fault is reported at
// Error once, and again only when it changes or has been cleared in between.
func (p *product) ttl() time.Duration {
	if p.store == nil {
		return defaults.TTL
	}
	value, err := settings.Get[time.Duration](p.store, settings.Global(), ttlKey)
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		// No layer holds a row for the key, which is the declared default.
		p.report(ttlKey, "", "")
		return defaults.TTL
	case err != nil:
		p.report(ttlKey, "the setting cannot be read, so its declared default is used", err.Error())
		return defaults.TTL
	case !ttlInDomain(value):
		p.report(ttlKey, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(value))
		return defaults.TTL
	}
	p.report(ttlKey, "", "")
	return value
}

// report writes the Error line for a fault of one setting. The same fault is
// written once; an empty problem clears it so that it is written again if it
// comes back.
func (p *product) report(key, problem, detail string) {
	p.mu.Lock()
	if p.reported == nil {
		p.reported = make(map[string]string)
	}
	last, reported := p.reported[key]
	switch {
	case problem == "":
		delete(p.reported, key)
	case !reported || last != detail:
		p.reported[key] = detail
	default:
		problem = ""
	}
	p.mu.Unlock()
	if problem != "" {
		p.logger.Error(ModuleName+": "+problem, slog.String("setting", key))
	}
}
