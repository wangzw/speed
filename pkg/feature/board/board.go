// Package board is the module that publishes the feature document — the feature
// names this process serves — on GET /api/v1/features to authenticated callers
// alone. It declares no feature of its own.
package board

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "feature.board"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const (
	endpointItem = "endpoint"
	endpointKey  = ConfigNamespace + "." + endpointItem
)

const pathFeatures = "/features"

const fragmentEndpoint = "account"

//go:embed openapi/board.json
var openAPIFragment []byte

var boardSpec = speedhttp.Spec{
	Endpoint: fragmentEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

var routePath = boardSpec.ResolvePath(pathFeatures)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. The entry point is required and
// the logger optional; the authentication gateway is taken up in Init and is
// mandatory there, and the authorization gate is optional.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*feature.Board)(nil), Exclusive: true}},
		Resources: []any{
			schema(),
			boardSpec,
			operations,
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			if _, err := readConfig(reg); err != nil {
				return core.Enablement{}, err
			}
			if err := validate(reg); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newBoard(reg)
		},

		Migrate: func(_ context.Context, _ *core.Registry, instance any) error {
			if b, ok := productOf(instance); ok {
				b.open()
			}
			return nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := productOf(instance)
			if !ok {
				return nil
			}
			return b.wire(reg)
		},

		Start: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := productOf(instance)
			if !ok {
				return nil
			}
			return b.seal(reg)
		},
	}
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the name of the listening endpoint the feature document is " +
					"published on, by the name the entry point's configuration gives it; required",
			},
		},
	}
}

var configDefaults = moduleConfig{}

type moduleConfig struct {
	Endpoint string `config:"endpoint"`
}

func readConfig(reg *core.Registry) (string, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return "", fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return "", err
	}
	return cfg.Endpoint, nil
}

func productOf(instance any) (*board, bool) {
	b, ok := instance.(*board)
	return b, ok && b != nil
}

type phase uint8

const (
	phaseShut phase = iota
	phaseOpen
	phaseSealed
)

type post struct {
	module string
	name   string
}

type board struct {
	endpoint string
	// logger is nil when the assembly has no logging capability.
	logger *slog.Logger

	// mu guards the window and the posts, which other modules' callbacks write.
	mu     sync.Mutex
	phase  phase
	posted map[post]struct{}

	// document is the frozen answer, written in Start and read without a lock
	// afterwards.
	document FeatureBoardDocument
}

func newBoard(reg *core.Registry) (*board, error) {
	endpoint, err := readConfig(reg)
	if err != nil {
		return nil, err
	}
	b := &board{endpoint: endpoint, posted: make(map[post]struct{})}
	logger, found, err := optional[log.Logger](reg, "the logging capability")
	if err != nil {
		return nil, err
	}
	if found {
		b.logger = logger.Named(ModuleName)
	}
	return b, nil
}

func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// Post records that module serves name in this run; outside the posting window
// it panics.
func (b *board) Post(module, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase != phaseOpen {
		panic("feature.board: Post was called outside the window between the board's Migrate " +
			"and its Start; a conditional feature is posted in Init")
	}
	b.posted[post{module: module, name: name}] = struct{}{}
}

func (b *board) open() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.phase = phaseOpen
}

func (b *board) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point: %w", ModuleName, err)
	}
	endpoint, err := router.Endpoint(b.endpoint)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %w", ModuleName, speedhttp.ErrUnknownEndpoint, b.endpoint, err)
	}

	gate, err := core.Resolve[authn.Gate](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return fmt.Errorf("%s: the feature document is published to authenticated callers "+
				"alone, which needs the authentication gateway: %w", ModuleName, feature.ErrNoGate)
		}
		return fmt.Errorf("%s: taking up the authentication gateway: %w", ModuleName, err)
	}

	endpoint.Route(http.MethodGet, routePath, readOperation.Handle(b.read))
	gate.Impersonable(b.endpoint, http.MethodGet, routePath)

	if declarer, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		declarer.Open(http.MethodGet, routePath)
	}
	return nil
}

func (b *board) seal(reg *core.Registry) error {
	b.mu.Lock()
	b.phase = phaseSealed
	posted := make([]post, 0, len(b.posted))
	for p := range b.posted {
		posted = append(posted, p)
	}
	b.mu.Unlock()

	declared := core.Resources[feature.Spec](reg)
	conditional := make(map[post]struct{}, len(declared))
	for _, d := range declared {
		if d.Value.Conditional {
			conditional[post{module: d.Module, name: d.Value.Name}] = struct{}{}
		}
	}

	sort.Slice(posted, func(i, j int) bool {
		if posted[i].module != posted[j].module {
			return posted[i].module < posted[j].module
		}
		return posted[i].name < posted[j].name
	})
	for _, p := range posted {
		if _, ok := conditional[p]; !ok {
			return fmt.Errorf("%w: module %q posted %q, which it does not declare as a "+
				"conditional feature", feature.ErrUndeclared, p.module, p.name)
		}
	}

	names, err := b.collect(reg, posted)
	if err != nil {
		return err
	}
	b.document = FeatureBoardDocument{Features: names}

	if b.logger != nil {
		b.logger.Info("feature set frozen",
			"count", len(names),
			"digest", digestOf(names),
			"names", names,
		)
	}
	return nil
}

func (b *board) collect(reg *core.Registry, posted []post) ([]string, error) {
	reported := make(map[post]struct{}, len(posted))
	for _, p := range posted {
		reported[p] = struct{}{}
	}

	force := make(map[string]bool)
	inForce := func(module string) (bool, error) {
		if got, known := force[module]; known {
			return got, nil
		}
		if reg.AllResourcesInForce(module) {
			force[module] = true
			return true, nil
		}
		e, known := reg.Enablement(module)
		if !known {
			return false, fmt.Errorf("%s: the enablement of module %q, which declares a feature, "+
				"could not be resolved", ModuleName, module)
		}
		got := e.State != core.StateDisabled
		force[module] = got
		return got, nil
	}

	names := make([]string, 0)
	seen := make(map[string]struct{})
	for _, d := range core.Resources[feature.Spec](reg) {
		ok, err := inForce(d.Module)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if d.Value.Conditional {
			if _, reported := reported[post{module: d.Module, name: d.Value.Name}]; !reported {
				continue
			}
		}
		if _, twice := seen[d.Value.Name]; twice {
			continue
		}
		seen[d.Value.Name] = struct{}{}
		names = append(names, d.Value.Name)
	}
	sort.Strings(names)
	return names, nil
}

func validate(reg *core.Registry) error {
	declared := make(map[string]string)
	for _, res := range core.Resources[feature.Spec](reg) {
		name, module := res.Value.Name, res.Module
		if !feature.ValidName(name) {
			return fmt.Errorf("%w: module %q declares %q, which is not a feature name: a name "+
				"has at least two dot-separated segments, each beginning with a lower-case "+
				"letter and continuing with lower-case letters, digits or dashes",
				feature.ErrInvalidName, module, name)
		}
		if want := firstSegment(module); firstSegment(name) != want {
			return fmt.Errorf("%w: module %q declares %q, whose first segment is not %q",
				feature.ErrForeignName, module, name, want)
		}
		if previous, taken := declared[name]; taken {
			return fmt.Errorf("%w: modules %q and %q both declare %q",
				feature.ErrDuplicate, previous, module, name)
		}
		declared[name] = module
	}
	return nil
}

func firstSegment(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}
