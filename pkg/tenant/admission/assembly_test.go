package admission

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

type resolutionStub struct{}

func (resolutionStub) Register(string, tenant.Extractor) {}

type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type supply struct {
	module   string
	token    core.Token
	instance any
}

type fixture struct {
	body                        string
	noRouter                    bool
	noResolution, noMemberships bool
	memberships                 authn.Memberships
	supplied                    []supply
	instance                    any
}

const defaultBody = `{
	"http": {"listeners": {"public": {"address": "127.0.0.1:0"}, "admin": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}, "admin": {"listener": "admin"}}},
	"tenant": {"admission": {"endpoints": ["public"]}}
}`

func (f fixture) drive(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	body := f.body
	if body == "" {
		body = defaultBody
	}
	module := Module()
	entry := speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, body, module, entry)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{ModuleName}
	reg.Register(module)
	if !f.noRouter {
		reg.Register(entry)
		names = append(names, speedhttp.ModuleName)
	}
	supplied := f.supplied
	if !f.noResolution {
		supplied = append(supplied, supply{"stand-in.resolution", (*tenant.Resolution)(nil), resolutionStub{}})
	}
	if !f.noMemberships {
		memberships := f.memberships
		if memberships == nil {
			stub := newMemberStub()
			stub.add(alice, acme)
			memberships = stub
		}
		supplied = append(supplied, supply{"stand-in.memberships", (*authn.Memberships)(nil), memberships})
	}
	for _, s := range supplied {
		reg.Register(core.Module{Name: s.module, Provides: []core.Provision{{Token: s.token}}})
		reg.Supply(s.instance, s.module)
		names = append(names, s.module)
	}
	if f.instance != nil {
		reg.Supply(f.instance, ModuleName)
	}
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{Depth: depth, Participants: names, Action: action})
}

func withProviders(p authn.SSOProviders) supply {
	return supply{"stand-in.providers", (*authn.SSOProviders)(nil), p}
}

func withDecider(d authn.Decider) supply {
	return supply{"stand-in.decider", (*authn.Decider)(nil), d}
}

func withStore(s settings.Store) supply {
	return supply{"stand-in.settings", (*settings.Store)(nil), s}
}

func withBus(b event.Bus) supply {
	return supply{"stand-in.bus", (*event.Bus)(nil), b}
}

func withLogs(logs *recorder) supply {
	return supply{"stand-in.logger", (*log.Logger)(nil), loggerStub{logger: slog.New(logs)}}
}
