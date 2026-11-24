package db

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

const (
	postgresNamespace = "db.postgres"
	sqliteNamespace   = "db.sqlite"
)

func specWithMutex() Spec {
	return Spec{
		ModuleName:                  postgresNamespace,
		ConfigNamespace:             postgresNamespace,
		Dialect:                     Postgres,
		Dialector:                   func(string) gorm.Dialector { return nil },
		ConstraintViolation:         func(error) error { return nil },
		NewMigrationLock:            func(time.Duration) MigrationLock { return nil },
		DefaultMigrationLockTimeout: 5 * time.Minute,
	}
}

func specWithoutMutex() Spec {
	return Spec{
		ModuleName:          sqliteNamespace,
		ConfigNamespace:     sqliteNamespace,
		Dialect:             SQLite,
		Dialector:           func(string) gorm.Dialector { return nil },
		ConstraintViolation: func(error) error { return nil },
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	return locator
}

func readerOver(t *testing.T, body string, modules ...core.Module) (config.Reader, error) {
	t.Helper()
	locator := writeConfig(t, body)

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDBTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		reg.Register(m)
	}

	return config.Load(t.Context(), reg, nil)
}

func readerFor(t *testing.T, spec Spec, body string) config.Reader {
	t.Helper()
	reader, err := readerOver(t, body, NewModule(spec))
	if err != nil {
		t.Fatalf("loading the configuration failed: %v", err)
	}
	return reader
}

// TestPoolDefaults pins the values an assembly that configured nothing but a locator runs on.
func TestPoolDefaults(t *testing.T) {
	cfg, err := specWithMutex().read(readerFor(t, specWithMutex(), `{"db":{"postgres":{"dsn":"x"}}}`))
	if err != nil {
		t.Fatalf("reading a section that gives only a locator: %v", err)
	}
	if cfg.MaxOpenConns != 25 {
		t.Errorf("max-open-conns defaults to %d, want 25", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 5 {
		t.Errorf("max-idle-conns defaults to %d, want 5", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 30*time.Minute {
		t.Errorf("conn-max-lifetime defaults to %v, want 30m", cfg.ConnMaxLifetime)
	}
}

// TestConfiguredPoolParametersOverrideTheDefaults is the other half of the case above.
func TestConfiguredPoolParametersOverrideTheDefaults(t *testing.T) {
	spec := specWithMutex()
	body := `{"db":{"postgres":{"dsn":"x","max-open-conns":7,"max-idle-conns":2,` +
		`"conn-max-lifetime":"90s"}}}`
	cfg, err := spec.read(readerFor(t, spec, body))
	if err != nil {
		t.Fatalf("reading a fully configured section: %v", err)
	}
	if cfg.MaxOpenConns != 7 || cfg.MaxIdleConns != 2 || cfg.ConnMaxLifetime != 90*time.Second {
		t.Errorf("the section read back as %d/%d/%v, want 7/2/1m30s",
			cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime)
	}
}

// TestDurationItemsRejectBareNumber pins that a duration is written with a unit.
func TestDurationItemsRejectBareNumber(t *testing.T) {
	for _, c := range []struct {
		item string
		body string
	}{
		{connMaxLifetimeKey, `{"db":{"postgres":{"dsn":"x","conn-max-lifetime":1800}}}`},
		{MigrationLockTimeoutKey, `{"db":{"postgres":{"dsn":"x","migration-lock-timeout":300}}}`},
	} {
		t.Run(c.item, func(t *testing.T) {
			spec := specWithMutex()
			_, err := spec.read(readerFor(t, spec, c.body))
			if !errors.Is(err, config.ErrTypeMismatch) {
				t.Fatalf("a bare number for %s gave %v, want config.ErrTypeMismatch", c.item, err)
			}
			if want := postgresNamespace + "." + c.item; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %s: %v", want, err)
			}
		})
	}
}

// TestSensitiveItemsAreMarked pins the three items that carry a secret.
func TestSensitiveItemsAreMarked(t *testing.T) {
	items := specWithMutex().schema().Items
	for _, key := range []string{dsnKey, encryptionKeyKey, encryptionRetiredKeysKey} {
		item, declared := items[key]
		if !declared {
			t.Errorf("%s is not declared at all", key)
			continue
		}
		if !item.Sensitive {
			t.Errorf("%s is not marked sensitive, so its value reaches the logs and the "+
				"help output", key)
		}
		if item.Description == "" {
			t.Errorf("%s is sensitive and has no description, so the help output has nothing "+
				"to say about it", key)
		}
	}
	if items[dsnKey].Required {
		t.Errorf("%s is marked required, which turns an assembly that runs without this "+
			"engine into a startup failure", dsnKey)
	}
}

// TestEveryDeclaredItemIsDescribed keeps the help output complete.
func TestEveryDeclaredItemIsDescribed(t *testing.T) {
	for _, spec := range []Spec{specWithMutex(), specWithoutMutex()} {
		schema := spec.schema()
		for key, item := range schema.Items {
			if item.Description == "" {
				t.Errorf("%s.%s is declared without a description", schema.Namespace, key)
			}
		}
	}
}

func itemPaths(t *testing.T, schema config.Schema) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, mount := range schema.Mounts {
		v := reflect.ValueOf(mount.Value)
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			t.Fatalf("a mount carries %T, which config would refuse", mount.Value)
		}
		collectPaths(t, out, v.Type(), strings.Trim(schema.Namespace+"."+mount.Path, "."))
	}
	return out
}

func collectPaths(t *testing.T, into map[string]bool, carrier reflect.Type, prefix string) {
	t.Helper()
	for i := range carrier.NumField() {
		f := carrier.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			collectPaths(t, into, f.Type, prefix)
			continue
		}
		tag, tagged := f.Tag.Lookup("config")
		if !tagged {
			t.Fatalf("field %s of %s carries no config tag, so its key is derived and this "+
				"walk no longer matches the manifest's", f.Name, carrier)
		}
		into[prefix+"."+tag] = true
	}
}

// TestTwoImplementationsDeclareDisjointConfigPaths pins that the two implementations
// declare their input items on separate paths.
func TestTwoImplementationsDeclareDisjointConfigPaths(t *testing.T) {
	withMutex, withoutMutex := specWithMutex(), specWithoutMutex()
	if got := withMutex.schema().Namespace; got != postgresNamespace {
		t.Errorf("the mutex-carrying implementation mounts under %q, want %q", got, postgresNamespace)
	}
	if got := withoutMutex.schema().Namespace; got != sqliteNamespace {
		t.Errorf("the other implementation mounts under %q, want %q", got, sqliteNamespace)
	}

	left, right := itemPaths(t, withMutex.schema()), itemPaths(t, withoutMutex.schema())
	shared := slices.Sorted(maps.Keys(left))
	shared = slices.DeleteFunc(shared, func(p string) bool { return !right[p] })
	if len(shared) != 0 {
		t.Errorf("the two implementations declare %v on the same paths; a host importing both "+
			"fails to load its configuration at all", shared)
	}
	if _, err := readerOver(t, `{"db":{"postgres":{"dsn":"x"}}}`,
		NewModule(withMutex), NewModule(withoutMutex)); err != nil {
		t.Fatalf("a host importing both implementations could not load its configuration: %v", err)
	}

	rival := specWithoutMutex()
	rival.ModuleName = "db.rival"
	rival.ConfigNamespace = postgresNamespace
	_, err := readerOver(t, `{}`, NewModule(withMutex), NewModule(rival))
	if !errors.Is(err, config.ErrConfigConflict) {
		t.Errorf("two implementations sharing a namespace loaded with %v, want "+
			"config.ErrConfigConflict: that is the failure separate namespaces avoid", err)
	}
}

// TestOnlyAnImplementationWithAMutexDeclaresTheTimeout pins that the timeout follows the mutex.
func TestOnlyAnImplementationWithAMutexDeclaresTheTimeout(t *testing.T) {
	if _, declared := specWithoutMutex().schema().Items[MigrationLockTimeoutKey]; declared {
		t.Errorf("an implementation with no mutex declares %s", MigrationLockTimeoutKey)
	}
	if _, declared := specWithMutex().schema().Items[MigrationLockTimeoutKey]; !declared {
		t.Errorf("an implementation with a mutex does not declare %s", MigrationLockTimeoutKey)
	}

	body := `{"db":{"sqlite":{"dsn":"x","migration-lock-timeout":"5m"}}}`
	_, err := readerOver(t, body, NewModule(specWithoutMutex()))
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("setting %s on an implementation without a mutex gave %v, want "+
			"config.ErrUnknownKey", MigrationLockTimeoutKey, err)
	}
}

// TestTheMutexTimeoutDefaultsToTheImplementationValue pins that the default reaching the mutex is
// the one the implementation gave, not a zero that would have the run give up before it waited.
func TestTheMutexTimeoutDefaultsToTheImplementationValue(t *testing.T) {
	spec := specWithMutex()
	cfg, err := spec.read(readerFor(t, spec, `{"db":{"postgres":{"dsn":"x"}}}`))
	if err != nil {
		t.Fatalf("reading a section that configures no timeout: %v", err)
	}
	if cfg.MigrationLockTimeout != spec.DefaultMigrationLockTimeout {
		t.Errorf("%s defaults to %v, want the implementation's %v",
			MigrationLockTimeoutKey, cfg.MigrationLockTimeout, spec.DefaultMigrationLockTimeout)
	}

	configured, err := spec.read(readerFor(t, spec,
		`{"db":{"postgres":{"dsn":"x","migration-lock-timeout":"9s"}}}`))
	if err != nil {
		t.Fatalf("reading a configured timeout: %v", err)
	}
	if configured.MigrationLockTimeout != 9*time.Second {
		t.Errorf("a configured %s read back as %v, want 9s",
			MigrationLockTimeoutKey, configured.MigrationLockTimeout)
	}
}

// TestANonPositiveMutexTimeoutFailsTheStartup pins the configured timeout to the rule the Spec's
// default is held to.
func TestANonPositiveMutexTimeoutFailsTheStartup(t *testing.T) {
	spec := specWithMutex()
	for _, timeout := range []string{"0s", "-5s"} {
		body := `{"db":{"postgres":{"dsn":"x","migration-lock-timeout":"` + timeout + `"}}}`
		_, err := spec.prepare(readerFor(t, spec, body))
		if err == nil {
			t.Errorf("Prepare accepted %s of %s, so a replica that meets the mutex held gives up "+
				"without waiting", MigrationLockTimeoutKey, timeout)
			continue
		}
		if want := postgresNamespace + "." + MigrationLockTimeoutKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of %s does not name %s: %v", timeout, want, err)
		}
	}
	if _, err := spec.prepare(readerFor(t, spec,
		`{"db":{"postgres":{"dsn":"x","migration-lock-timeout":"1s"}}}`)); err != nil {
		t.Errorf("Prepare refused a positive %s: %v", MigrationLockTimeoutKey, err)
	}
}

// TestRetiredKeysReadBackAsAList pins that the retired keys are a list rather than one string.
func TestRetiredKeysReadBackAsAList(t *testing.T) {
	spec := specWithMutex()
	body := `{"db":{"postgres":{"dsn":"x","encryption-key":"new",` +
		`"encryption-retired-keys":["older","oldest"]}}}`
	cfg, err := spec.read(readerFor(t, spec, body))
	if err != nil {
		t.Fatalf("reading a section with retired keys: %v", err)
	}
	if !slices.Equal(cfg.EncryptionRetiredKeys, []string{"older", "oldest"}) {
		t.Errorf("the retired keys read back as %q, want both of them in order",
			cfg.EncryptionRetiredKeys)
	}
	if cfg.EncryptionKey != "new" {
		t.Errorf("the current key read back as %q, want the configured one", cfg.EncryptionKey)
	}
}

// TestAnUndeclaredKeyInThisSectionIsRefused pins that the section is closed.
func TestAnUndeclaredKeyInThisSectionIsRefused(t *testing.T) {
	body := `{"db":{"postgres":{"dsn":"x","max-open-connections":7}}}`
	_, err := readerOver(t, body, NewModule(specWithMutex()))
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("a misspelled pool parameter gave %v, want config.ErrUnknownKey", err)
	}
}

// TestTheReaderIsAskedForThisImplementationOwnSection pins the path the module decodes from.
func TestTheReaderIsAskedForThisImplementationOwnSection(t *testing.T) {
	spec := specWithMutex()
	asked := &pathRecordingReader{}
	if _, err := spec.read(asked); err != nil {
		t.Fatalf("reading through a recording reader: %v", err)
	}
	if asked.path != postgresNamespace {
		t.Errorf("the module decoded from %q, want its own section %q", asked.path, postgresNamespace)
	}
}

type pathRecordingReader struct {
	path string
}

func (r *pathRecordingReader) Decode(path string, _ any) error {
	r.path = path
	return nil
}

var _ config.Reader = (*pathRecordingReader)(nil)

// TestDefaultsSurviveAReaderThatGivesNothing pins that the defaults are in the struct the module
// decodes into, not only in the layer the loader builds.
func TestDefaultsSurviveAReaderThatGivesNothing(t *testing.T) {
	cfg, err := specWithMutex().read(&pathRecordingReader{})
	if err != nil {
		t.Fatalf("reading through a reader that gives nothing: %v", err)
	}
	if cfg.MaxOpenConns != 25 || cfg.MaxIdleConns != 5 || cfg.ConnMaxLifetime != 30*time.Minute {
		t.Errorf("a reader that gave nothing left the pool at %d/%d/%v, want the defaults",
			cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime)
	}
}
