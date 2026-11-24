package invite

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

func (p *product) ttl() time.Duration {
	if p.store == nil {
		return defaults.TTL
	}
	value, err := settings.Get[time.Duration](p.store, settings.Global(), ttlKey)
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
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
