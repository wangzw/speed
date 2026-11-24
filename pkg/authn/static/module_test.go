package static

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

type recordedLogger struct {
	mu    sync.Mutex
	names []string
	out   *capture
}

func (l *recordedLogger) Named(name string) *slog.Logger {
	l.mu.Lock()
	l.names = append(l.names, name)
	l.mu.Unlock()
	return l.out.logger().With("module", name)
}
func (l *recordedLogger) Redaction() log.Redaction { return noopRedaction{} }

type noopRedaction struct{}

func (noopRedaction) AddKeys(...string)              {}
func (noopRedaction) AddPattern(string, log.Matcher) {}

func driveStatic(t *testing.T, body string, store settings.Store, logger log.Logger) *backend {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	participants := []string{ModuleName}
	if store != nil {
		reg.Register(core.Module{
			Name:     "stand-in.settings",
			Provides: []core.Provision{{Token: (*settings.Store)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return store, nil },
		})
		participants = append(participants, "stand-in.settings")
	}
	if logger != nil {
		reg.Register(core.Module{
			Name:     "stand-in.logger",
			Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return logger, nil },
		})
		participants = append(participants, "stand-in.logger")
	}
	var found *backend
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action: func(_ context.Context, reg *core.Registry) error {
			v, err := core.Resolve[authn.Verifier](reg)
			if err != nil {
				return err
			}
			found, _ = v.(*backend)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	if found == nil {
		t.Fatal("the module delivered no backend")
	}
	return found
}

// the module, driven as a host drives it, takes up the settings store and
// the logger that are in the assembly and builds its state from its section.
func TestTheModuleTakesUpWhatTheAssemblyDeliversAndBuildsFromItsSection(t *testing.T) {
	subjects := `{"alice":"` + cheapEntry(t, "alices") + `","bob":"` + cheapEntry(t, "bobs") + `"}`
	body := func(extra string) string {
		return `{"authn":{"static":{` + extra + `"subjects":` + subjects + `}}}`
	}

	t.Run("the settings store and the logger", func(t *testing.T) {
		store := newFakeSettings()
		var out capture
		logger := &recordedLogger{out: &out}
		b := driveStatic(t, body(""), store, logger)
		if b.store != store {
			t.Fatal("the backend did not take up the settings store the assembly delivered")
		}
		if !reflect.DeepEqual(logger.names, []string{ModuleName}) {
			t.Errorf("the backend asked the logger for the names %v, want [%s]", logger.names, ModuleName)
		}
		store.set(cacheTTLPath, time.Duration(0))
		derived := counting(b)
		b.Verify(context.Background(), basic("alice", "alices"))
		b.Verify(context.Background(), basic("alice", "alices"))
		if derived.Load() != 2 {
			t.Errorf("with the cache lifetime set to zero in the store two checks derived %d times, want 2: the store was not read", derived.Load())
		}
	})

	t.Run("the cache size", func(t *testing.T) {
		for size, want := range map[int]int64{1: 3, 3: 2} {
			var out capture
			b := driveStatic(t, body(`"cache-size":`+strconv.Itoa(size)+`,`), nil, &recordedLogger{out: &out})
			wantWeakEntriesNamed(t, &out, "alice", "bob")
			derived := counting(b)
			for _, c := range []authn.Credential{basic("alice", "alices"), basic("bob", "bobs"), basic("alice", "alices")} {
				if got := b.Verify(context.Background(), c); outcome(got) != verdict.Authenticated {
					t.Fatalf("a check reads %v (%v)", outcome(got), cause(got))
				}
			}
			if derived.Load() != want {
				t.Errorf("with a cache of %d the three checks derived %d times, want %d", size, derived.Load(), want)
			}
		}
	})

	t.Run("the width", func(t *testing.T) {
		slow, err := HashPBKDF2("slow", 60_000)
		if err != nil {
			t.Fatal(err)
		}
		slowBody := func(width int) string {
			return `{"authn":{"static":{"concurrency":` + strconv.Itoa(width) + `,"subjects":{"slow":"` + slow + `"}}}}`
		}
		busy := func(width, callers int) int64 {
			var out capture
			b := driveStatic(t, slowBody(width), nil, &recordedLogger{out: &out})
			wantWeakEntriesNamed(t, &out, "slow")
			var unavailable atomic.Int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range callers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					got := b.Verify(context.Background(), basic("slow", "wrong"+fmt.Sprint(i)))
					if outcome(got) == verdict.Unavailable && errors.Is(cause(got), authn.ErrBusy) {
						unavailable.Add(1)
					}
				}()
			}
			close(start)
			wg.Wait()
			return unavailable.Load()
		}
		if got := busy(1, 6); got == 0 {
			t.Error("with a width of one six callers at once were all served: the width is not the one configured")
		}
		if got := busy(16, 6); got != 0 {
			t.Errorf("with a width of sixteen %d of six callers at once were refused as busy: the width is not the one configured", got)
		}
	})
}

func wantWeakEntriesNamed(t *testing.T, out *capture, names ...string) {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "below the least OWASP recommends") {
			lines = append(lines, line)
		}
	}
	if len(lines) != len(names) {
		t.Errorf("the startup says %d times that an entry is below the least OWASP recommends, want once for each of %v:\n%s",
			len(lines), names, out.String())
	}
	for _, name := range names {
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, " entry="+name+" ") }) {
			t.Errorf("the startup does not name the entry %q as below the least OWASP recommends:\n%s", name, out.String())
		}
	}
}

// the module declares both its schemas to the registry.
func TestTheModuleDeclaresBothSchemas(t *testing.T) {
	var haveConfig, haveSettings bool
	for _, r := range Module().Resources {
		switch r.(type) {
		case config.Schema:
			haveConfig = true
		case settings.Schema:
			haveSettings = true
		}
	}
	if !haveConfig || !haveSettings {
		t.Errorf("the module declares the configuration schema: %v, the settings schema: %v", haveConfig, haveSettings)
	}
}

func driveStaticErr(t *testing.T, body string, extra ...core.Module) error {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	participants := []string{ModuleName}
	for _, m := range extra {
		reg.Register(m)
		participants = append(participants, m.Name)
	}
	return reg.Drive(context.Background(), core.Drive{Depth: core.StageInit, Participants: participants})
}

// a table that names nobody stops the startup through the lifecycle, and
// names the key.
func TestAnEmptyTableStopsTheStartupThroughTheLifecycle(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"authn":{"static":{}}}`,
		"empty":  `{"authn":{"static":{"subjects":{}}}}`,
	} {
		err := driveStaticErr(t, body)
		if err == nil {
			t.Fatalf("%s: a command that names this module started with a table naming nobody", name)
		}
		if !errors.Is(err, authn.ErrInvalidClaim) || !strings.Contains(err.Error(), ConfigNamespace+"."+subjectsItem) {
			t.Errorf("%s: the startup failed with %v, want one wrapping %v that names %s.%s", name, err, authn.ErrInvalidClaim, ConfigNamespace, subjectsItem)
		}
		if absent := errors.Is(err, config.ErrMissingRequired); absent != (name == "absent") {
			t.Errorf("%s: the startup failure wraps %v: %v, want %v: %v", name, config.ErrMissingRequired, absent, name == "absent", err)
		}
	}
}

// a settings store that cannot be taken up is a startup failure and not
// a silent default.
func TestASettingsStoreThatCannotBeTakenUpStopsTheStartup(t *testing.T) {
	provider := func(name string) core.Module {
		return core.Module{
			Name:     name,
			Provides: []core.Provision{{Token: (*settings.Store)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return newFakeSettings(), nil },
		}
	}
	body := `{"authn":{"static":{"subjects":{"alice":"` + cheapEntry(t, "x") + `"}}}}`
	err := driveStaticErr(t, body, provider("stand-in.settings.a"), provider("stand-in.settings.b"))
	if err == nil {
		t.Fatal("two providers of the settings store started without complaint, so the backend runs on whichever it was handed")
	}
	if !strings.Contains(err.Error(), "settings store") {
		t.Errorf("the startup failed with %v, want it to say that the settings store could not be taken up", err)
	}
	if !errors.Is(err, core.ErrAmbiguousProvider) {
		t.Errorf("the startup failed with %v, want the cause kept in the chain: %v", err, core.ErrAmbiguousProvider)
	}
}

// the module is registered under the name the command and the registry share
// trees give it.
func TestTheModuleAndItsSectionKeepTheirDocumentedNames(t *testing.T) {
	if ModuleName != "authn.static" || ConfigNamespace != "authn.static" || backendName != "static" {
		t.Errorf("the module is %q, its section %q and its backend %q, want authn.static, authn.static and static",
			ModuleName, ConfigNamespace, backendName)
	}
}

// an assembly that never handed the registry a configuration reader is a
// startup failure that says so, and is not read as a missing table.
func TestAnAssemblyWithNoConfigurationReaderStopsTheStartupAndSaysSo(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	err := reg.Drive(context.Background(), core.Drive{Depth: core.StageInit, Participants: []string{ModuleName}})
	if err == nil {
		t.Fatal("the module started with no configuration reader in the registry")
	}
	if !errors.Is(err, core.ErrMissingProvider) || !strings.Contains(err.Error(), "no reader was handed to the registry") {
		t.Errorf("the startup failed with %v, want one wrapping %v that says no reader was handed to the registry", err, core.ErrMissingProvider)
	}
	if errors.Is(err, authn.ErrInvalidClaim) {
		t.Errorf("a missing reader was reported as a table naming nobody: %v", err)
	}
}
