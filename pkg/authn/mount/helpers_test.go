package mount

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// routeCall is one Route call a stand-in entry point received.
type routeCall struct {
	endpoint string
	method   string
	path     string
}

// accessCall is one registration a stand-in gateway received.
type accessCall struct {
	kind     string // Exempt, Optional or Impersonable
	endpoint string
	method   string
	path     string
}

// ledger is what the stand-ins recorded, in the order of the calls. The entry
// point and the gateway write to one ledger so that a case reads the two halves
// of a registration side by side.
type ledger struct {
	mu     sync.Mutex
	routes []routeCall
	access []accessCall
}

func (l *ledger) addRoute(c routeCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.routes = append(l.routes, c)
}

func (l *ledger) addAccess(c accessCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.access = append(l.access, c)
}

func (l *ledger) snapshot() ([]routeCall, []accessCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.routes), slices.Clone(l.access)
}

// standInRouter is an entry point that declares the endpoints it was given and
// records the routes registered on them.
type standInRouter struct {
	names  []string
	ledger *ledger
}

func (r *standInRouter) Endpoints() []string { return slices.Sorted(slices.Values(r.names)) }

func (r *standInRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	if !slices.Contains(r.names, name) {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return standInEndpoint{name: name, ledger: r.ledger}, nil
}

type standInEndpoint struct {
	name   string
	ledger *ledger
}

func (e standInEndpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	if len(handlers) == 0 {
		panic("stand-in entry point: a route with no handler")
	}
	e.ledger.addRoute(routeCall{endpoint: e.name, method: method, path: path})
}
func (standInEndpoint) Use(speedhttp.Middleware) {}
func (standInEndpoint) Accepting() bool          { return false }

// standInGate is a gateway of a host's own: it implements the registration
// surface and nothing else, and records what it was asked.
type standInGate struct{ ledger *ledger }

var _ authn.Gate = standInGate{}

func (g standInGate) Exempt(endpoint, method, path string) {
	g.ledger.addAccess(accessCall{"Exempt", endpoint, method, path})
}

func (g standInGate) Optional(endpoint, method, path string) {
	g.ledger.addAccess(accessCall{"Optional", endpoint, method, path})
}

func (g standInGate) Impersonable(endpoint, method, path string) {
	g.ledger.addAccess(accessCall{"Impersonable", endpoint, method, path})
}

// passthroughGuard is the pkg/http RouteGuard a case supplies when the point is
// that a tenant-scoped group mounts at all: it checks nothing, and the guard's
// own rule is held by the tenant capability's tests.
type passthroughGuard struct{}

func (passthroughGuard) Guard(next gin.HandlerFunc) gin.HandlerFunc { return next }

// scopedGuard records which of the two route-guard seams the mounter used. The
// mounter applies the shared identity guard to every tenant-scoped group; the
// tenant administration modules apply the stricter guard themselves, so a
// non-zero admin count here would mean the mounter narrowed the shared rule.
type scopedGuard struct {
	mu     sync.Mutex
	shared int
	admin  int
}

func (g *scopedGuard) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.shared++
	g.mu.Unlock()
	return next
}

func (g *scopedGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.admin++
	g.mu.Unlock()
	return next
}

func (g *scopedGuard) counts() (shared, admin int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shared, g.admin
}

// standIn is a module that declares the capability token names and nothing
// else, for an instance a case supplies to it.
func standIn(name string, token core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

// registrant is a module that mounts routes the way a real one does: it requires
// the route-mounting capability and registers from its Init.
func registrant(register func(mount.Mounter)) core.Module {
	return core.Module{
		Name:     "stand-in.registrant",
		Requires: []core.Requirement{{Token: (*mount.Mounter)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			m, err := core.Resolve[mount.Mounter](reg)
			if err != nil {
				return err
			}
			register(m)
			return nil
		},
	}
}

// stack is one drive of a real registry through this module: the module itself,
// the configuration reader, an entry point and a gateway that are stand-ins
// unless a case brings the real ones.
type stack struct {
	// body is the whole configuration, JSON.
	body string
	// router is the stand-in entry point; nil leaves the command without one.
	router *standInRouter
	// realGateModule registers the real gateway module, so that the keys this
	// module reads are declared the way they are in a real assembly. With gate
	// set the instance is supplied to it, and its own callbacks do nothing.
	realGateModule bool
	// gate is the gateway of the host's own that is supplied. With
	// realGateModule false it is delivered by a module of its own.
	gate authn.Gate
	// routeGuard, when set, is delivered as the pkg/http RouteGuard capability. A
	// case that mounts a tenant-scoped group supplies one: the mounter refuses a
	// tenant-scoped registration without it.
	routeGuard speedhttp.RouteGuard
	// register runs in the Init of a module that requires the capability.
	register func(mount.Mounter)
	// logs, when set, is where the logging capability's records go.
	logs *recorder
}

// drive advances the stack to Start and runs action there, returning what the
// drive returned once every Stop and Close has run.
func (s stack) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	own := Module()
	modules := []core.Module{own}
	var gateModule core.Module
	if s.realGateModule {
		gateModule = gate.Module()
		modules = append(modules, gateModule)
	}
	reader := authntest.ConfigReaderAll(t, s.body, modules...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(own)
	names := []string{ModuleName}
	switch {
	case s.realGateModule:
		reg.Register(gateModule)
		names = append(names, gate.ModuleName)
		if s.gate != nil {
			reg.Supply(s.gate, gate.ModuleName)
		}
	case s.gate != nil:
		reg.Register(standIn("stand-in.gate", (*authn.Gate)(nil)))
		reg.Supply(s.gate, "stand-in.gate")
		names = append(names, "stand-in.gate")
	}
	if s.router != nil {
		reg.Register(standIn("stand-in.router", (*speedhttp.Router)(nil)))
		reg.Supply(s.router, "stand-in.router")
		names = append(names, "stand-in.router")
	}
	if s.routeGuard != nil {
		reg.Register(standIn("stand-in.route-guard", (*speedhttp.RouteGuard)(nil)))
		reg.Supply(s.routeGuard, "stand-in.route-guard")
		names = append(names, "stand-in.route-guard")
	}
	if s.register != nil {
		m := registrant(s.register)
		reg.Register(m)
		names = append(names, m.Name)
	}
	if s.logs != nil {
		m := loggingModule(s.logs)
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

func nothing(context.Context, *core.Registry) error { return nil }

// recorder is a slog handler that keeps every record written to it.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *recorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recorder) WithGroup(string) slog.Handler      { return h }

func (h *recorder) recordsOf() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.records)
}

// recordNamed returns the first record written with msg.
func (h *recorder) recordNamed(msg string) (slog.Record, bool) {
	for _, r := range h.recordsOf() {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// attrText renders a record's attribute as the string it holds.
func attrText(r slog.Record, key string) string {
	var text string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			text = a.Value.String()
			return false
		}
		return true
	})
	return text
}

// recordingLogging delivers the logging capability over a logger the case holds.
type recordingLogging struct{ logger *slog.Logger }

func (l recordingLogging) Named(string) *slog.Logger { return l.logger }
func (recordingLogging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

var _ log.Logger = recordingLogging{}

// loggingModule is the module that delivers the logging capability over logs.
func loggingModule(logs *recorder) core.Module {
	return core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return recordingLogging{logger: slog.New(logs)}, nil
		},
	}
}
