package stream

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
)

// The cases built on this file drive the module's descriptor through a real
// registry: the configuration module with a reader the real loader produced,
// this module itself, and stand-ins for the required bus and the optional
// entry point. What a product built by hand cannot show is which callback takes
// up which dependency and what the descriptor says of its own delivery, which
// is none.

// streamBody is the configuration a default drive runs on: one endpoint of the
// entry point, "public", that the stream route is mounted on.
const streamBody = `{"notification": {"stream": {"endpoints": ["public"]}}}`

// assembly is one drive. The zero value mounts the route on "public" with a
// stand-in bus and entry point; each field takes one capability away or
// replaces a stand-in or the body.
type assembly struct {
	body     string
	noBus    bool
	noRouter bool
	bus      event.Bus
	router   speedhttp.Router
	logger   *slog.Logger
}

// drive advances the assembly to depth and runs a no-op action.
func (a assembly) drive(t *testing.T, depth core.Stage) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = streamBody
	}
	module := Module()
	reader := authntest.ConfigReader(t, module, body)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{ModuleName}
	reg.Register(module)

	if !a.noBus {
		bus := a.bus
		if bus == nil {
			bus = newStubBus()
		}
		reg.Register(standIn("stand-in.bus", bus, (*event.Bus)(nil)))
		names = append(names, "stand-in.bus")
	}
	if !a.noRouter {
		router := a.router
		if router == nil {
			router = newStubRouter("public")
		}
		reg.Register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if a.logger != nil {
		reg.Register(standIn("stand-in.logger", loggerStub{logger: a.logger}, (*log.Logger)(nil)))
		names = append(names, "stand-in.logger")
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
}

// This module delivers no capability: it carries no product another module
// resolves, and the route is its only product.
func TestThisModuleDeliversNothing(t *testing.T) {
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}
}

// The event bus is required; the entry point and the logger are optional. The
// gateway is not declared at all: this module never resolves it, so where the
// stream route sits relative to one is the host's assembly.
func TestTheDescriptorRequiresTheBusAndLeavesTheRestOptional(t *testing.T) {
	cases := []struct {
		name     string
		token    core.Token
		optional bool
	}{
		{"event bus", (*event.Bus)(nil), false},
		{"http entry point", (*speedhttp.Router)(nil), true},
		{"logger", (*log.Logger)(nil), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found := false
			for _, r := range Module().Requires {
				if r.Token != c.token {
					continue
				}
				found = true
				if r.Optional != c.optional {
					t.Errorf("Optional = %v, want %v", r.Optional, c.optional)
				}
			}
			if !found {
				t.Errorf("%s is not declared in Requires", c.name)
			}
		})
	}
	if n := len(Module().Requires); n != len(cases) {
		t.Errorf("the descriptor declares %d requirements, want %d: %+v", n, len(cases), Module().Requires)
	}
}

// A command that brings this module in without a bus fails resolution with
// ErrMissingProvider, before New builds anything.
func TestAMissingBusFailsResolution(t *testing.T) {
	err := assembly{noBus: true}.drive(t, core.StageNew)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
	}
}

// A real registry mounts the stream route on the configured endpoint and
// subscribes to the inbox-created event.
func TestTheAssemblyMountsTheRouteAndSubscribes(t *testing.T) {
	bus := newStubBus()
	router := newStubRouter("public")
	if err := (assembly{bus: bus, router: router}).drive(t, core.StageStart); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	got := router.endpoint("public").registered()
	want := mountedRoute{http.MethodGet, pathStream}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("mounted %v, want the single route %s %s", got, want.method, want.path)
	}
	if pathStream != "/api/v1/notifications/stream" {
		t.Errorf("the route path is %q, want /api/v1/notifications/stream", pathStream)
	}
	if n := bus.subscribed(eventInboxCreated); n != 1 {
		t.Errorf("subscribed %d handlers to %q, want 1", n, eventInboxCreated)
	}
}

// Init takes up the optional logging capability and writes the start diagnostic
// through it, so a command that assembles logging sees what the push module
// mounted.
func TestTheOptionalLoggerIsTakenUpInInit(t *testing.T) {
	logs := &logBuffer{}
	if err := (assembly{logger: logs.logger()}).drive(t, core.StageStart); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if !strings.Contains(logs.String(), ModuleName+": the stream route is mounted") {
		t.Errorf("the start diagnostic did not reach the delivered logger: %q", logs.String())
	}
}

// A command with no entry point still constructs the module and subscribes; it
// simply mounts no route, and /stream is not served.
func TestTheModuleConstructsWithoutAnEntryPoint(t *testing.T) {
	bus := newStubBus()
	if err := (assembly{noRouter: true, bus: bus}).drive(t, core.StageStart); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if n := bus.subscribed(eventInboxCreated); n != 1 {
		t.Errorf("subscribed %d handlers to %q, want 1", n, eventInboxCreated)
	}
}

// The endpoint list is required of a run that mounts the route and is read
// nowhere by a run that assembles no entry point: a list with nothing in it then
// mounts nothing because there is nothing to mount on, and the module starts.
func TestWithoutAnEntryPointAnEmptyEndpointListIsLegal(t *testing.T) {
	cases := map[string]string{
		"an empty list":  `{"notification": {"stream": {"endpoints": []}}}`,
		"no list key":    `{"notification": {"stream": {}}}`,
		"no stream item": `{"notification": {}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newStubBus()
			if err := (assembly{noRouter: true, bus: bus, body: body}).drive(t, core.StageStart); err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
			if n := bus.subscribed(eventInboxCreated); n != 1 {
				t.Errorf("subscribed %d handlers to %q, want 1", n, eventInboxCreated)
			}
		})
	}
}

// An entry point present makes the endpoint list required: the route is mounted
// on the endpoints the module is told to, and a list that names none would drop
// it silently. Every shape the module cannot run on fails Init, naming the key.
func TestWithAnEntryPointTheEndpointListRefusesShapesItCannotRunOn(t *testing.T) {
	cases := map[string]string{
		"empty":      `[]`,
		"star":       `["*"]`,
		"empty name": `[""]`,
		"duplicate":  `["public", "public"]`,
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"notification": {"stream": {"endpoints": ` + endpoints + `}}}`
			err := assembly{body: body}.drive(t, core.StageStart)
			if !errors.Is(err, notification.ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want notification.ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}
