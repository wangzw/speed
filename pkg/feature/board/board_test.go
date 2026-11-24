package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
)

const defaultBody = `{"feature": {"board": {"endpoint": "account"}}}`

type contested interface{ markContested() }

type contender struct{}

func (contender) markContested() {}

type gateStub struct{}

func (*gateStub) Exempt(string, string, string)       {}
func (*gateStub) Optional(string, string, string)     {}
func (*gateStub) Impersonable(string, string, string) {}

type recordedRoute struct {
	method   string
	path     string
	handlers []gin.HandlerFunc
}

type recordingRouter struct {
	mu     sync.Mutex
	names  []string
	routes []recordedRoute
}

func newRecordingRouter(names ...string) *recordingRouter {
	if len(names) == 0 {
		names = []string{"account"}
	}
	own := slices.Clone(names)
	slices.Sort(own)
	return &recordingRouter{names: own}
}

func (r *recordingRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.names)
}

func (r *recordingRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.names, name) {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return r, nil
}

func (r *recordingRouter) Route(method, path string, handlers ...gin.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, recordedRoute{method: method, path: path, handlers: handlers})
}

func (*recordingRouter) Use(speedhttp.Middleware) {}

func (*recordingRouter) Accepting() bool { return true }

func (r *recordingRouter) handlers(method, path string) []gin.HandlerFunc {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, route := range r.routes {
		if route.method == method && route.path == path {
			return slices.Clone(route.handlers)
		}
	}
	return nil
}

type loggerStub struct{ inner *slog.Logger }

func (l loggerStub) Named(name string) *slog.Logger { return l.inner.With("module", name) }

func (loggerStub) Redaction() log.Redaction { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

type recordHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*recordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordHandler) find(message string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == message {
			return r, true
		}
	}
	return slog.Record{}, false
}

type fixture struct {
	body         string
	modules      []core.Module
	participants []string
	declaring    []core.Declaration
	noGate       bool
	router       *recordingRouter
	logger       *slog.Logger
	board        core.Module
	depth        core.Stage
	toPrepare    bool
	action       func(reg *core.Registry) error
}

func (f fixture) run(t *testing.T) error {
	t.Helper()

	body := f.body
	if body == "" {
		body = defaultBody
	}
	router := f.router
	if router == nil {
		router = newRecordingRouter("account")
	}
	module := f.board
	if module.Name == "" {
		module = Module()
	}
	modules := append([]core.Module{module}, f.modules...)
	reader := authntest.ConfigReaderAll(t, body, modules...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(module)
	names := []string{ModuleName}
	for _, m := range f.modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(routecontract.StandIn("stand-in.router", router, (*speedhttp.Router)(nil)))
	names = append(names, "stand-in.router")
	if !f.noGate {
		reg.Register(routecontract.StandIn("stand-in.gate", &gateStub{}, (*authn.Gate)(nil)))
		names = append(names, "stand-in.gate")
	}
	if f.logger != nil {
		reg.Register(routecontract.StandIn("stand-in.logging", loggerStub{inner: f.logger}, (*log.Logger)(nil)))
		names = append(names, "stand-in.logging")
	}
	if len(f.participants) > 0 {
		names = slices.Clone(f.participants)
	}
	if f.logger != nil && !slices.Contains(names, "stand-in.logging") {
		names = append(names, "stand-in.logging")
	}
	depth := f.depth
	switch {
	case f.toPrepare:
		depth = core.StagePrepare
	case depth == 0:
		depth = core.StageStart
	}
	action := f.action
	if action == nil {
		action = func(*core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:         depth,
		Participants:  names,
		DeclaringOnly: f.declaring,
		Action:        func(_ context.Context, reg *core.Registry) error { return action(reg) },
	})
}

func boardOf(t *testing.T, reg *core.Registry) *board {
	t.Helper()
	delivered, err := core.Resolve[feature.Board](reg)
	if err != nil {
		t.Fatalf("resolving the declaration surface: %v", err)
	}
	b, ok := delivered.(*board)
	if !ok || b == nil {
		t.Fatalf("the delivered declaration surface is %T, not this module's product", delivered)
	}
	return b
}

func decodeBody(t *testing.T, body []byte) []string {
	t.Helper()
	var doc FeatureBoardDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decoding the frozen answer %q: %v", body, err)
	}
	return doc.Features
}

func resources(specs ...feature.Spec) []any {
	out := make([]any, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec)
	}
	return out
}

func declared(name string, specs ...feature.Spec) core.Module {
	return core.Module{Name: name, Resources: resources(specs...)}
}

func posting(name, posted string, specs ...feature.Spec) core.Module {
	return core.Module{
		Name:      name,
		Resources: resources(specs...),
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			delivered, err := core.Resolve[feature.Board](reg)
			if err != nil {
				return fmt.Errorf("resolving the declaration surface: %w", err)
			}
			delivered.Post(name, posted)
			return nil
		},
	}
}

// TestTheFrozenSetFollowsTheEnabledSetAndThePostingWindow checks that the frozen
// set is the enabled modules' static features plus the conditionals they posted.
func TestTheFrozenSetFollowsTheEnabledSetAndThePostingWindow(t *testing.T) {
	modules := []core.Module{
		declared("a.app", feature.Spec{Name: "a.one"}),
		declared("b.app", feature.Spec{Name: "b.one"}),
		declared("c.app", feature.Spec{Name: "c.one"}, feature.Spec{Name: "c.cond", Conditional: true}),
		declared("c2.app", feature.Spec{Name: "c2.one"}),
		{
			Name:      "e.app",
			Resources: resources(feature.Spec{Name: "e.one"}),
			Provides:  []core.Provision{{Token: (*contested)(nil), Exclusive: true}},
			New: func(context.Context, *core.Registry) (any, error) {
				return contender{}, nil
			},
		},
		{
			Name:     "x.app",
			Provides: []core.Provision{{Token: (*contested)(nil), Exclusive: true}},
			Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
				return core.Enablement{State: core.StateEnabled}, nil
			},
			New: func(context.Context, *core.Registry) (any, error) {
				return contender{}, nil
			},
		},
		declared("f.app", feature.Spec{Name: "f.two"}, feature.Spec{Name: "f.one"}),
		posting("g.app", "g.cond",
			feature.Spec{Name: "g.cond", Conditional: true},
			feature.Spec{Name: "g.idle", Conditional: true},
		),
	}

	var got []string
	f := fixture{
		modules:      modules,
		participants: []string{ModuleName, "stand-in.router", "stand-in.gate", "a.app", "e.app", "x.app", "f.app", "g.app"},
		declaring: []core.Declaration{
			{Name: "c.app", AllResources: true},
			{Name: "c2.app"},
		},
		action: func(reg *core.Registry) error {
			got = boardOf(t, reg).document.Features
			return nil
		},
	}
	if err := f.run(t); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := []string{"a.one", "c.one", "f.one", "f.two", "g.cond"}
	if !slices.Equal(got, want) {
		t.Errorf("the frozen set is %v, want exactly %v", got, want)
	}
}

// TestAPostThatIsNotADeclaredConditionalFailsTheStart checks that a post of a
// name the poster did not declare conditional fails the start with ErrUndeclared.
func TestAPostThatIsNotADeclaredConditionalFailsTheStart(t *testing.T) {
	for name, modules := range map[string][]core.Module{
		"a name nobody declared": {
			posting("p.app", "p.missing"),
		},
		"a name another module declared": {
			posting("p.app", "q.one"),
			declared("q.app", feature.Spec{Name: "q.one", Conditional: true}),
		},
		"a name the poster declared static": {
			posting("p.app", "p.static", feature.Spec{Name: "p.static"}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := fixture{
				modules:      modules,
				participants: append([]string{ModuleName, "stand-in.router", "stand-in.gate"}, moduleNames(modules)...),
			}
			err := f.run(t)
			if !errors.Is(err, feature.ErrUndeclared) {
				t.Fatalf("the drive ended with %v, want ErrUndeclared", err)
			}
			if err != nil && !strings.Contains(err.Error(), "p.app") {
				t.Errorf("the failure does not name the posting module: %v", err)
			}
		})
	}
}

// TestAPostOutsideTheWindowPanics checks that a post before the window opens or
// after it closes panics.
func TestAPostOutsideTheWindowPanics(t *testing.T) {
	t.Run("in New", func(t *testing.T) {
		poster := core.Module{
			Name:      "zzz.poster",
			Resources: resources(feature.Spec{Name: "zzz.one", Conditional: true}),
			New: func(_ context.Context, reg *core.Registry) (any, error) {
				delivered, err := core.Resolve[feature.Board](reg)
				if err != nil {
					return nil, fmt.Errorf("resolving the declaration surface: %w", err)
				}
				delivered.Post("zzz.poster", "zzz.one")
				return struct{}{}, nil
			},
		}
		requirePanic(t, func() {
			_ = fixture{
				modules:      []core.Module{poster},
				participants: []string{ModuleName, "stand-in.router", "stand-in.gate", "zzz.poster"},
			}.run(t)
		})
	})

	t.Run("in Migrate before the board's own Migrate", func(t *testing.T) {
		poster := core.Module{
			Name:      "aaa.poster",
			Resources: resources(feature.Spec{Name: "aaa.one", Conditional: true}),
			Migrate: func(_ context.Context, reg *core.Registry, _ any) error {
				delivered, err := core.Resolve[feature.Board](reg)
				if err != nil {
					return fmt.Errorf("resolving the declaration surface: %w", err)
				}
				delivered.Post("aaa.poster", "aaa.one")
				return nil
			},
		}
		requirePanic(t, func() {
			_ = fixture{
				modules:      []core.Module{poster},
				participants: []string{ModuleName, "stand-in.router", "stand-in.gate", "aaa.poster"},
			}.run(t)
		})
	})

	t.Run("after Start", func(t *testing.T) {
		requirePanic(t, func() {
			f := fixture{
				action: func(reg *core.Registry) error {
					boardOf(t, reg).Post("late.poster", "late.one")
					return nil
				},
			}
			_ = f.run(t)
		})
	})
}

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("the call returned instead of panicking")
		}
	}()
	fn()
}

// TestTheAnswerCarriesNoStoreAndAnOrderedUniqueSet checks the no-store header,
// the media type and an ascending unique name set.
func TestTheAnswerCarriesNoStoreAndAnOrderedUniqueSet(t *testing.T) {
	router := newRecordingRouter("account")
	f := fixture{
		router:       router,
		modules:      []core.Module{declared("f.app", feature.Spec{Name: "f.two"}, feature.Spec{Name: "f.one"})},
		participants: []string{ModuleName, "stand-in.router", "stand-in.gate", "f.app"},
	}
	if err := f.run(t); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	handlers := router.handlers(http.MethodGet, routePath)
	if len(handlers) == 0 {
		t.Fatalf("no GET %s handler was registered", routePath)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET(routePath, handlers...)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, routePath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d, want 200", routePath, rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET %s carries Cache-Control %q, want no-store", routePath, got)
	}
	if got := rec.Header().Get("Content-Type"); strings.SplitN(got, ";", 2)[0] != "application/json" {
		t.Errorf("GET %s carries Content-Type %q, want the application/json media type", routePath, got)
	}
	got := decodeBody(t, rec.Body.Bytes())
	if want := []string{"f.one", "f.two"}; !slices.Equal(got, want) {
		t.Errorf("the answer is %v, want %v", got, want)
	}
}

// TestPrepareChecksEveryDeclarationInTheAvailableSet checks that Prepare refuses
// a malformed, foreign or duplicate name even in a module the command never took in.
func TestPrepareChecksEveryDeclarationInTheAvailableSet(t *testing.T) {
	for name, tc := range map[string]struct {
		modules []core.Module
		want    error
		names   []string
	}{
		"a malformed name": {
			modules: []core.Module{declared("a.app", feature.Spec{Name: "single"})},
			want:    feature.ErrInvalidName,
		},
		"a name from another namespace": {
			modules: []core.Module{declared("q.app", feature.Spec{Name: "other.one"})},
			want:    feature.ErrForeignName,
		},
		"one name declared twice": {
			modules: []core.Module{
				declared("p.a", feature.Spec{Name: "p.one"}),
				declared("p.b", feature.Spec{Name: "p.one"}),
			},
			want: feature.ErrDuplicate,
		},
		"a malformed name in a module the command never took in": {
			modules: []core.Module{declared("z.unused", feature.Spec{Name: "Bad.name"})},
			want:    feature.ErrInvalidName,
			names:   []string{ModuleName, "stand-in.router", "stand-in.gate"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			participants := tc.names
			if len(participants) == 0 {
				participants = append([]string{ModuleName, "stand-in.router", "stand-in.gate"}, moduleNames(tc.modules)...)
			}
			err := fixture{
				modules:      tc.modules,
				participants: participants,
				toPrepare:    true,
			}.run(t)
			if !errors.Is(err, tc.want) {
				t.Fatalf("the drive ended with %v, want %v", err, tc.want)
			}
		})
	}
}

// TestTheStartLogCarriesTheCountAndTheDigest checks the Start summary's count
// and its set-dependent digest.
func TestTheStartLogCarriesTheCountAndTheDigest(t *testing.T) {
	recorder := &recordHandler{}
	logger := slog.New(recorder)
	f := fixture{
		logger:       logger,
		modules:      []core.Module{declared("f.app", feature.Spec{Name: "f.two"}, feature.Spec{Name: "f.one"})},
		participants: []string{ModuleName, "stand-in.router", "stand-in.gate", "f.app"},
	}
	if err := f.run(t); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	record, ok := recorder.find("feature set frozen")
	if !ok {
		t.Fatalf("Start wrote no %q record; it wrote %v", "feature set frozen", recorder.records)
	}
	attrs := map[string]string{}
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	if got := attrs["count"]; got != "2" {
		t.Errorf("the summary carries count=%q, want 2", got)
	}
	names := []string{"f.one", "f.two"}
	if got, want := attrs["digest"], digestOf(names); got != want {
		t.Errorf("the summary carries digest=%q, want %q", got, want)
	}

	if digestOf(names) != digestOf([]string{"f.one", "f.two"}) {
		t.Error("the digest changed for the same name set")
	}
	if digestOf(names) == digestOf([]string{"f.one"}) {
		t.Error("the digest stayed the same for a different name set")
	}
}

// TestAMissingEndpointFailsPrepareNamingTheFullPath checks that a missing
// endpoint fails Prepare with the full key named.
func TestAMissingEndpointFailsPrepareNamingTheFullPath(t *testing.T) {
	err := fixture{body: `{}`, toPrepare: true}.run(t)
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("the drive ended with %v, want %v", err, config.ErrMissingRequired)
	}
	if err != nil && !strings.Contains(err.Error(), endpointKey) {
		t.Errorf("the failure does not name %q: %v", endpointKey, err)
	}
}

func moduleNames(modules []core.Module) []string {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		out = append(out, m.Name)
	}
	return out
}
