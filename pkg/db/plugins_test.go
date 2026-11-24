package db_test

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

type pluginRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *pluginRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, name)
}

func (r *pluginRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.order)
}

type recordingPlugin struct {
	name string
	into *pluginRecorder
}

func (p recordingPlugin) Name() string { return p.name }

func (p recordingPlugin) Initialize(*gorm.DB) error {
	if p.into != nil {
		p.into.record(p.name)
	}
	return nil
}

func pluginModule(name string, plugins ...gorm.Plugin) core.Module {
	resources := make([]any, 0, len(plugins))
	for _, plugin := range plugins {
		resources = append(resources, db.Plugin{Plugin: plugin})
	}
	return core.Module{Name: name, Resources: resources}
}

func installedOn(handle *gorm.DB) []string {
	names := make([]string, 0, len(handle.Plugins))
	for name := range handle.Plugins {
		if name == db.OpenedMarkerName || name == db.EngineMarkerName {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func migrationSet(dialect db.Dialect, files map[string]string) db.Migrations {
	tree := fstest.MapFS{}
	for name, body := range files {
		tree[path.Join(string(dialect), name)] = &fstest.MapFile{Data: []byte(body)}
	}
	return db.Migrations{FS: tree}
}

type pluginObserver struct {
	seen chan []string
}

func (o *pluginObserver) module() core.Module {
	return core.Module{
		Name:     "observer",
		Requires: []core.Requirement{{Token: (*db.Database)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			instance, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			o.seen <- installedOn(instance.DB())
			return nil, nil
		},
	}
}

// TestDependantSeesEveryPluginAtResolveTime pins the timing of the installation: a dependant that
// takes the capability up has a handle whose declared plugins are already installed on it.
func TestDependantSeesEveryPluginAtResolveTime(t *testing.T) {
	observer := &pluginObserver{seen: make(chan []string, 1)}
	h, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", recordingPlugin{name: "audit-plugin"}),
		pluginModule("catalog", recordingPlugin{name: "catalog-plugin"}),
		observer.module(),
	)
	if err != nil {
		t.Fatalf("starting a host with declared plugins: %v", err)
	}

	want := []string{"audit-plugin", "catalog-plugin"}
	if got := <-observer.seen; !slices.Equal(got, want) {
		t.Errorf("a dependant resolving the capability saw the plugins %v, want %v; every statement it "+
			"issues on that handle runs without them", got, want)
	}
	if got := installedOn(h.Instance.DB()); !slices.Equal(got, want) {
		t.Errorf("the delivered handle carries the plugins %v, want %v", got, want)
	}
}

// TestOpenedHandleCarriesTheSameAssembly pins that a connection built with Open carries the
// declared plugins too.
func TestOpenedHandleCarriesTheSameAssembly(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", recordingPlugin{name: "audit-plugin"}),
		pluginModule("catalog", recordingPlugin{name: "catalog-plugin"}),
	)
	if err != nil {
		t.Fatalf("starting a host with declared plugins: %v", err)
	}

	opened, err := h.Instance.Open(t.Context(), filepath.Join(t.TempDir(), "elsewhere.db"))
	if err != nil {
		t.Fatalf("building a second connection: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(opened); err != nil {
			t.Errorf("releasing the second connection: %v", err)
		}
	})

	want := []string{"audit-plugin", "catalog-plugin"}
	if got := installedOn(h.Instance.DB()); !slices.Equal(got, want) {
		t.Fatalf("the delivered handle carries the plugins %v, want %v", got, want)
	}
	if got := installedOn(opened); !slices.Equal(got, want) {
		t.Errorf("the connection built with Open carries the plugins %v, want %v; the caller moved a "+
			"query to another database and lost what the declarations do to it", got, want)
	}
}

type originProbe struct {
	mu      sync.Mutex
	answers []bool
}

func (p *originProbe) Name() string { return "origin-probe" }

func (p *originProbe) Initialize(handle *gorm.DB) error {
	_, opened := db.OpenedFrom(handle)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers = append(p.answers, opened)
	return nil
}

func (p *originProbe) seen() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.answers)
}

// TestOpenedFromAnswersTheDeliveredHandle checks that every session derived from a connection
// built with Open answers the delivered handle, and a handle this module did not build does not.
func TestOpenedFromAnswersTheDeliveredHandle(t *testing.T) {
	probe := &originProbe{}
	h, err := startHost(t, sqliteSpec(), hostConfig(t), pluginModule("probe-plugin", probe))
	if err != nil {
		t.Fatalf("starting a host with a declared plugin: %v", err)
	}
	delivered := h.Instance.DB()
	opened, err := h.Instance.Open(t.Context(), filepath.Join(t.TempDir(), "elsewhere.db"))
	if err != nil {
		t.Fatalf("building a second connection: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(opened); closeErr != nil {
			t.Errorf("releasing the second connection: %v", closeErr)
		}
	})

	if got := probe.seen(); !slices.Equal(got, []bool{false, true}) {
		t.Errorf("the declared plugin, installed on the delivered handle and then on the second "+
			"connection, saw OpenedFrom answer %v, want [false true]", got)
	}

	requireOrigin := func(name string, session *gorm.DB) {
		t.Helper()
		got, ok := db.OpenedFrom(session)
		if !ok || got != delivered {
			t.Errorf("%s: OpenedFrom gave (%p, %v), want the delivered handle %p", name, got, ok, delivered)
		}
	}
	requireOrigin("the connection itself", opened)
	requireOrigin("a session with a context of its own", opened.WithContext(context.Background()))
	requireOrigin("a new session", opened.Session(&gorm.Session{NewDB: true}))
	requireOrigin("a statement under way", opened.Table("anything").Where("1 = 1"))
	if txErr := opened.Transaction(func(tx *gorm.DB) error {
		requireOrigin("a transaction", tx)
		return nil
	}); txErr != nil {
		t.Fatalf("running a transaction on the second connection: %v", txErr)
	}

	requireNone := func(name string, session *gorm.DB) {
		t.Helper()
		if got, ok := db.OpenedFrom(session); ok {
			t.Errorf("%s: OpenedFrom gave %p, want no answer", name, got)
		}
	}
	requireNone("the delivered handle", delivered)
	requireNone("a session on the delivered handle", delivered.WithContext(context.Background()))
	if txErr := delivered.Transaction(func(tx *gorm.DB) error {
		requireNone("a transaction on the delivered handle", tx)
		return nil
	}); txErr != nil {
		t.Fatalf("running a transaction on the delivered handle: %v", txErr)
	}
	foreign, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "foreign.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening a handle outside the module: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(foreign) })
	requireNone("a handle the module did not build", foreign)
	requireNone("no handle at all", nil)
}

type orderingCapability interface{ ordered() }

// orderingProduct is what the module providing it constructs.
type orderingProduct struct{}

func (orderingProduct) ordered() {}

// TestPluginsInstallInDependencyOrder pins the order the plugins go on in.
func TestPluginsInstallInDependencyOrder(t *testing.T) {
	t.Run("a dependency decides, even against the module names", func(t *testing.T) {
		order := &pluginRecorder{}
		zeta := pluginModule("zeta", recordingPlugin{name: "zeta-plugin", into: order})
		zeta.Provides = []core.Provision{{Token: (*orderingCapability)(nil)}}
		zeta.New = func(context.Context, *core.Registry) (any, error) { return orderingProduct{}, nil }

		alpha := pluginModule("alpha", recordingPlugin{name: "alpha-plugin", into: order})
		alpha.Requires = []core.Requirement{{Token: (*orderingCapability)(nil)}}

		h, err := startHost(t, sqliteSpec(), hostConfig(t), alpha, zeta)
		if err != nil {
			t.Fatalf("starting a host with dependent declarations: %v", err)
		}

		if got, want := installedOn(h.Instance.DB()), []string{"alpha-plugin", "zeta-plugin"}; !slices.Equal(got, want) {
			t.Fatalf("the delivered handle carries the plugins %v, want %v", got, want)
		}
		if got, want := order.recorded(), []string{"zeta-plugin", "alpha-plugin"}; !slices.Equal(got, want) {
			t.Errorf("the plugins were installed as %v, want %v: the dependant's plugin acts on a "+
				"statement before the plugin of the module it depends on", got, want)
		}
	})

	t.Run("without a dependency the module name decides", func(t *testing.T) {
		order := &pluginRecorder{}
		h, err := startHost(t, sqliteSpec(), hostConfig(t),
			pluginModule("catalog", recordingPlugin{name: "catalog-plugin", into: order}),
			pluginModule("audit", recordingPlugin{name: "audit-plugin", into: order}),
		)
		if err != nil {
			t.Fatalf("starting a host with independent declarations: %v", err)
		}

		if got, want := installedOn(h.Instance.DB()), []string{"audit-plugin", "catalog-plugin"}; !slices.Equal(got, want) {
			t.Fatalf("the delivered handle carries the plugins %v, want %v", got, want)
		}
		if got, want := order.recorded(), []string{"audit-plugin", "catalog-plugin"}; !slices.Equal(got, want) {
			t.Errorf("the plugins were installed as %v, want %v: with no dependency between the two "+
				"modules, the name order is what keeps one run looking like the next", got, want)
		}
	})
}

// TestDuplicatePluginNameIsErrPluginFailed pins what two modules declaring one plugin name get.
func TestDuplicatePluginNameIsErrPluginFailed(t *testing.T) {
	_, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", recordingPlugin{name: "cross-cutting"}),
		pluginModule("catalog", recordingPlugin{name: "cross-cutting"}),
	)
	if err == nil {
		t.Fatal("two modules declaring a plugin of the same name started up: one of the two " +
			"declarations was never installed, and nothing said which")
	}
	if !errors.Is(err, db.ErrPluginFailed) {
		t.Errorf("the startup reported %v, want ErrPluginFailed", err)
	}
	if !errors.Is(err, gorm.ErrRegistered) {
		t.Errorf("the startup reported %v, and GORM's refusal is not in the chain: the text has to "+
			"keep why the second one could not go on, or the two declarations look interchangeable", err)
	}
	for _, want := range []string{"audit", "catalog", "cross-cutting"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error text %q does not name %q", err, want)
		}
	}
}

func disabledModule(name string, plugin gorm.Plugin, set db.Migrations) core.Module {
	return core.Module{
		Name:      name,
		Provides:  []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		Resources: []any{db.Plugin{Plugin: plugin}, set},
	}
}

// TestTheDeclarationsOfADisabledModuleAreNotInForce pins both halves of what a module resolution
// stood down leaves behind: its migrations are not applied, and its plugin is not installed.
func TestTheDeclarationsOfADisabledModuleAreNotInForce(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", recordingPlugin{name: "audit-plugin"}),
		disabledModule("optout", recordingPlugin{name: "optout-plugin"},
			migrationSet(db.SQLite, map[string]string{
				"0001_create_optout_rows.sql": `CREATE TABLE optout_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
			})),
		core.Module{Name: "ledger", Resources: []any{
			migrationSet(db.SQLite, map[string]string{
				"0001_create_ledger_rows.sql": `CREATE TABLE ledger_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
			}),
		}},
	)
	if err != nil {
		t.Fatalf("starting a host beside a module resolution stood down: %v", err)
	}

	handle := h.Instance.DB()
	if !handle.Migrator().HasTable("ledger_rows") {
		t.Fatal("the migration of an enabled module was not applied, so what the next assertion " +
			"observes is a run that applies nothing")
	}
	if handle.Migrator().HasTable("optout_rows") {
		t.Error("the migration of a module resolution stood down was applied")
	}
	if got := installedOn(handle); !slices.Contains(got, "audit-plugin") {
		t.Fatalf("the handle carries the plugins %v, and an enabled module's plugin is not among "+
			"them: what the next assertion observes is a handle that installs nothing", got)
	}
	if got := installedOn(handle); slices.Contains(got, "optout-plugin") {
		t.Errorf("the handle carries the plugins %v, including one declared by a module that did "+
			"not run: such a module has a declaration in force, and every dependant that relies on "+
			"it loses it without a word", got)
	}
}

// TestOnlyTheEnabledModulesPluginReachesTheHandle pins the plugin side of the ruling at the handles
// a caller looks at: the one the capability delivered, and a second one built with Open.
func TestOnlyTheEnabledModulesPluginReachesTheHandle(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", recordingPlugin{name: "audit-plugin"}),
		disabledModule("optout", recordingPlugin{name: "optout-plugin"}, migrationSet(db.SQLite, nil)),
	)
	if err != nil {
		t.Fatalf("starting a host beside a module resolution stood down: %v", err)
	}

	handle := h.Instance.DB()
	if got := installedOn(handle); len(got) == 0 {
		t.Fatal("the delivered handle carries no plugins at all, so \"only the enabled module's " +
			"plugin is on it\" would hold for the wrong reason")
	}
	want := []string{"audit-plugin"}
	if got := installedOn(handle); !slices.Equal(got, want) {
		t.Errorf("the delivered handle carries the plugins %v, want exactly %v: the declaration of "+
			"a module that did not run is not in force", got, want)
	}

	opened, err := h.Instance.Open(t.Context(), filepath.Join(t.TempDir(), "elsewhere.db"))
	if err != nil {
		t.Fatalf("building a second connection: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(opened); err != nil {
			t.Errorf("releasing the second connection: %v", err)
		}
	})
	if got := installedOn(opened); !slices.Equal(got, want) {
		t.Errorf("a connection built with Open carries the plugins %v, want exactly %v: both handles "+
			"are installed from the one collection the assembly made, and a caller that moved a query "+
			"to another database got a declaration the delivered handle never had", got, want)
	}
}

type failingPlugin struct {
	name  string
	cause error
}

func (p failingPlugin) Name() string { return p.name }

func (p failingPlugin) Initialize(*gorm.DB) error { return p.cause }

// TestAFailingPluginIsErrPluginFailed pins the sentinel on the plugin's own refusal, which is the
// way a plugin that needs a model convention or a column it cannot find fails.
func TestAFailingPluginIsErrPluginFailed(t *testing.T) {
	cause := errors.New("the model this plugin filters by has no tenant column")
	_, err := startHost(t, sqliteSpec(), hostConfig(t),
		pluginModule("audit", failingPlugin{name: "audit-plugin", cause: cause}),
	)
	if err == nil {
		t.Fatal("a plugin that refused to install did not fail the startup, so the handle was " +
			"delivered with a declaration that is not in force")
	}
	if !errors.Is(err, db.ErrPluginFailed) {
		t.Errorf("the startup reported %v, want ErrPluginFailed", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("the startup reported %v, and what the plugin said is not in the chain: it is the "+
			"only account of what the plugin needed", err)
	}
	for _, want := range []string{"audit", "audit-plugin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error text %q does not name %q", err, want)
		}
	}
}

// nilPlugin is a plugin whose methods have value receivers, so a nil pointer to
// it still satisfies gorm.Plugin — and panics on the first call.
type nilPlugin struct{}

func (nilPlugin) Name() string              { return "nil-plugin" }
func (nilPlugin) Initialize(*gorm.DB) error { return nil }

type rawGuard struct{}

func (rawGuard) Name() string { return "raw-guard" }

func (rawGuard) Initialize(handle *gorm.DB) error {
	return handle.Callback().Raw().Before("gorm:raw").Register("raw-guard", func(tx *gorm.DB) {
		_ = tx.AddError(errors.New("raw-guard refuses native SQL"))
	})
}

// TestTheMigrationRunPassesThroughTheInstalledPlugins pins that the migration run
// issues its statements on the delivered handle, through its plugins.
func TestTheMigrationRunPassesThroughTheInstalledPlugins(t *testing.T) {
	tables := core.Module{Name: "catalog", Resources: []any{migrationSet(db.SQLite, map[string]string{
		"0001_widgets.sql": `CREATE TABLE widgets (id INTEGER PRIMARY KEY)`,
	})}}
	_, err := startHost(t, sqliteSpec(), hostConfig(t), pluginModule("guard", rawGuard{}), tables)
	if !errors.Is(err, db.ErrMigrationFailed) {
		t.Fatalf("a plugin refusing native SQL left the startup with %v, want ErrMigrationFailed", err)
	}
	if !strings.Contains(err.Error(), "raw-guard") {
		t.Errorf("the failure does not carry the plugin's refusal: %v", err)
	}
}

// TestAPluginDeclarationWithNothingInItIsRefused pins the two shapes of an empty declaration
// against what they would otherwise do.
func TestAPluginDeclarationWithNothingInItIsRefused(t *testing.T) {
	cases := map[string]core.Module{
		"the nil interface": {Name: "audit", Resources: []any{db.Plugin{}}},
		"a typed nil pointer": {
			Name:      "audit",
			Resources: []any{db.Plugin{Plugin: (*nilPlugin)(nil)}},
		},
	}
	for name, module := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := startHost(t, sqliteSpec(), hostConfig(t), module)
			if !errors.Is(err, db.ErrPluginFailed) {
				t.Fatalf("a declaration carrying nothing reported %v, want ErrPluginFailed", err)
			}
			if !strings.Contains(err.Error(), "audit") {
				t.Errorf("the error text %q does not name the module that declared it", err)
			}
		})
	}
}
