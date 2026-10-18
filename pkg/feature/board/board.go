// Package board is the module that publishes the feature document: the set of
// feature names this process serves, answered on GET /api/v1/features to authenticated
// callers alone.
//
// It declares no feature of its own. Modules that serve a user-facing surface
// declare one with a feature.Spec in their own resources; this module checks
// every declaration in the available set, collects the conditional ones the
// running modules post during Init, and freezes the answer in Start. The answer
// is process-wide: it follows the assembly, not the caller, and no permission or
// tenant narrows it. Whether the surface is authorized stays each serving
// module's decision.
//
// The module holds the declaration surface as feature.Board, delivered
// exclusively, so a dependency closure never pulls the board into a command: a
// host names it on the command tree of every command that serves a frontend.
//
// # Lifecycle
//
// Prepare reads this module's configuration and checks every feature
// declaration in the available set — including the ones made by modules this
// command never took in. Migrate opens the posting window. Init mounts the read
// route on the configured endpoint, registers it as letting an impersonating
// identity through, and declares it open on the authorization middleware when
// one is present. Start seals the window, judges every post, computes the set
// from the enabled set and the host's AllResources adjustment, and freezes the
// answer bytes. A request reads the frozen bytes and nothing else.
package board

import (
	"context"
	_ "embed"
	"encoding/json"
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

// ModuleName is this module's name in the registry. A host writes it into the
// command of every process that serves a frontend.
const ModuleName = "feature.board"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

// The configuration item this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	endpointItem = "endpoint"
	endpointKey  = ConfigNamespace + "." + endpointItem
)

// pathFeatures is the route the document is answered on, relative to the API
// root the fragment declares. The mounting resolves it against that root, so
// the path is written once.
const pathFeatures = "/features"

// fragmentEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The resource is a static declaration fixed before
// configuration is read, so the fragment carries the conventional name of the
// account surface rather than the configured one; the mounting uses the name
// configuration gives. A declared endpoint name no endpoint carries is not an
// error, the same way a declared path and a registered route are separately
// maintained facts (pkg/http's Spec).
const fragmentEndpoint = "account"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/board.json
var openAPIFragment []byte

// boardSpec is the API root this module's fragment and routes hang off.
var boardSpec = speedhttp.Spec{
	Endpoint: fragmentEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

// routePath is the absolute path the read route is mounted on: the declared
// path resolved against the API root.
var routePath = boardSpec.ResolvePath(pathFeatures)

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command publish the feature
// document" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The entry point is required: the module mounts a route through it, so the
// presence of the board implies the process has one. The logger is optional and
// taken up only to write the frozen-set summary in Start. The authentication and
// authorization gateways are not requirements — both claim what they deliver
// exclusively, so a dependency closure would not pull either in anyway — and are
// taken up in Init, where the authentication one is mandatory: without it the
// document would be published to anyone, so the startup fails rather than
// exposing it.
//
// The board delivers feature.Board exclusively. That keeps a dependency closure
// from adding the module to a command on another module's behalf: the host names
// it wherever the document is served.
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

		// Prepare reads the configuration and checks every declaration in the
		// available set. It does not filter: a module this command never took
		// in still has its declaration checked, because a malformed name is a
		// defect of the binary and would otherwise pass in one command and fail
		// in another for a declaration that never changed.
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

		// Migrate opens the posting window. Every module's Migrate has run
		// before any module's Init begins, so opening here contains the whole
		// Init stage and none of it precedes the board's own setup.
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

		// Start seals the window, judges the posts, computes the set from the
		// enabled set, and freezes the answer bytes.
		Start: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := productOf(instance)
			if !ok {
				return nil
			}
			return b.seal(reg)
		},
	}
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
//
// The endpoint takes the primary source alone. It decides where the document is
// published, so an environment variable or a command-line argument that could
// rewrite it would be a way to move the read surface onto an endpoint from
// outside the configuration file.
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

// configDefaults is what this module is worth without a configuration file.
// There is no default for the endpoint: a document mounted nowhere would be a
// read surface the frontend never finds, and guessing an endpoint is a guess
// about what is exposed. A module that cannot answer fails the startup, naming
// the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoint names the listening endpoint the read route is mounted on.
	Endpoint string `config:"endpoint"`
}

// readConfig decodes this module's section and returns the configured endpoint.
// Prepare and New both go through it, so the two see one configuration. A
// missing required item fails here with config.ErrMissingRequired, naming the
// full path.
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

// productOf narrows an instance to this module's product. The callbacks are
// also handed an instance a host supplied for the module, which can be any
// value that delivers feature.Board; this module leaves such an instance alone,
// so its own lifecycle does nothing for it.
func productOf(instance any) (*board, bool) {
	b, ok := instance.(*board)
	return b, ok && b != nil
}

// phase is the state of the posting window. The zero value is shut, so a Post
// from New — before Migrate has run — panics for the same reason one after
// Start does.
type phase uint8

const (
	phaseShut phase = iota
	phaseOpen
	phaseSealed
)

// post is one reporting module and the name it reported. Two calls with the
// same pair are one entry.
type post struct {
	module string
	name   string
}

// board is the module's product. It implements feature.Board.
type board struct {
	endpoint string
	// logger is nil when the assembly has no logging capability, in which case
	// Start writes no summary.
	logger *slog.Logger

	// mu guards the window and the posts. Post is called from other modules'
	// callbacks, which may run on a goroutine of their own, so the writes are
	// not ordered by the drive.
	mu     sync.Mutex
	phase  phase
	posted map[post]struct{}

	// body is the frozen answer. Start writes it before Serve begins, and
	// request handling reads it afterwards without a lock.
	body []byte
}

// newBoard takes up the configuration and the optional logging capability.
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

// optional takes up a capability whose absence is a legal assembly: the second
// result says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
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

// Post records that module serves name in this run. Outside the window opened
// by Migrate and closed by Start it panics: such a call is a defect in the
// declaring module's code, not a configuration this module could report.
func (b *board) Post(module, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase != phaseOpen {
		panic("feature.board: Post was called outside the window between the board's Migrate " +
			"and its Start; a conditional feature is posted in Init")
	}
	b.posted[post{module: module, name: name}] = struct{}{}
}

// open opens the posting window. It runs in Migrate.
func (b *board) open() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.phase = phaseOpen
}

// wire mounts the read route and registers it with the gateways. It runs in
// Init.
func (b *board) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point: %w", ModuleName, err)
	}
	endpoint, err := router.Endpoint(b.endpoint)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %w", ModuleName, speedhttp.ErrUnknownEndpoint, b.endpoint, err)
	}

	// The document's confidentiality is the gateway's alone, so a process
	// without one fails here rather than publishing the assembly's shape to
	// every caller.
	gate, err := core.Resolve[authn.Gate](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return fmt.Errorf("%s: the feature document is published to authenticated callers "+
				"alone, which needs the authentication gateway: %w", ModuleName, feature.ErrNoGate)
		}
		return fmt.Errorf("%s: taking up the authentication gateway: %w", ModuleName, err)
	}

	endpoint.Route(http.MethodGet, routePath, b.serve)
	gate.Impersonable(b.endpoint, http.MethodGet, routePath)

	// An impersonating identity reads the document like any other: the set is
	// process-wide, so impersonation changes neither it nor who may read it.
	//
	// The declaration surface is optional. When it is present, the route is
	// declared open: on an endpoint the authorization middleware guards with
	// undeclared closed, this declaration is what keeps it from answering 403,
	// and on an endpoint it does not guard the declaration changes nothing.
	if declarer, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		declarer.Open(http.MethodGet, routePath)
	}
	return nil
}

// seal closes the window, judges every post, computes the set and freezes the
// answer bytes. It runs in Start.
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
	body, err := json.Marshal(document{Features: names})
	if err != nil {
		return fmt.Errorf("%s: encoding the feature document: %w", ModuleName, err)
	}
	b.body = body

	if b.logger != nil {
		b.logger.Info("feature set frozen",
			"count", len(names),
			"digest", digestOf(names),
			"names", names,
		)
	}
	return nil
}

// collect computes the names this process serves, in ascending order.
//
// A module is in force when the host put all of its declarations in force, or
// when resolution did not disable it. A static feature counts then; a
// conditional one also has to have been posted, which a module that does not
// run cannot do — which is why the host's AllResources adjustment reaches static
// features alone.
//
// The enabled set is used rather than the participating set: a module that lost
// an exclusive resolution is a participant, but it has no instance and no
// routes, so it is disabled. The reason's text is not compared: membership of
// the verdict is what decides.
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

// validate checks every Spec in the available set: the name's shape, its
// namespace against the declaring module's name, and that no name is declared
// twice. Any failure names the declaring module and the name.
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

// firstSegment returns what precedes the first dot of a module or feature name.
func firstSegment(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}
