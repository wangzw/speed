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

// The cases built on this file drive a real registry through the descriptor:
// the configuration module with a reader the real loader produced, the entry
// point module itself, this module, and stand-ins for the modules that deliver
// the capabilities it takes. What they hold is what a handle built by hand
// cannot: which callback runs which step, what each takes up from the
// registry, and what the registry itself says of the descriptor.

// resolutionStub stands for the resolution layer: only the capability, which is
// what this module requires.
type resolutionStub struct{}

func (resolutionStub) Register(string, tenant.Extractor) {}

// loggerStub delivers the logging capability over a recorder.
type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes redaction registrations and masks nothing.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// supply is one instance handed to the registry for a stand-in module.
type supply struct {
	module   string
	token    core.Token
	instance any
}

// fixture is one assembly.
type fixture struct {
	// body is the configuration, JSON. The default declares one endpoint of
	// the entry point, "public", and has this module guard it.
	body string
	// noRouter leaves the entry point module out of the command.
	noRouter bool
	// noResolution and noMemberships leave out the stand-ins for what this
	// module requires.
	noResolution, noMemberships bool
	// memberships is delivered as the memberships; the default is a stub of
	// which alice is a member of acme.
	memberships authn.Memberships
	// supplied are further capabilities, the optional ones.
	supplied []supply
	// instance is supplied as this module's own instance, in place of what
	// its New builds.
	instance any
}

const defaultBody = `{
	"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}, "admin": {"address": "127.0.0.1:0"}}},
	"tenant": {"admission": {"endpoints": ["public"]}}
}`

// drive advances the assembly to the stage, runs action there, and returns what
// the drive returned.
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

// optionals are the stand-ins a case adds to take the optional capabilities in.
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
