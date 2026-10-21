package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/settings"
)

// The runtime tunables this module is expected to declare, written out here
// rather than read from a constant so that the cases name the keys the design
// lists.
const (
	uploadWindowSetting = "objectstore.metadata.upload-window"
	objectTTLSetting    = "objectstore.metadata.object-ttl"
	sweepIntervalKey    = "objectstore.metadata.sweep-interval"
)

// TestTheTunablesInTheStoreDriveTheNextCreateAndComplete holds that the upload
// window is read at each Create and the object lifetime at each Complete: a
// value written while the process runs is what the next call records, not the
// value the process started with.
func TestTheTunablesInTheStoreDriveTheNextCreateAndComplete(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{withSettings: true})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			store := settingsStoreOf(t, reg)
			body := []byte("body")

			// The upload window follows a write made before the declaration.
			writeSetting(t, store, uploadWindowSetting, 3*time.Hour)
			first, before, after := createObject(t, tctx, objects, "first", body)
			uploadExpiry, expires := expiriesOf(t, reg, first)
			assertDeadline(t, "the first object's upload window", uploadExpiry, before, after, 3*time.Hour)
			if expires != nil {
				t.Errorf("a completed-so-far object carries the expiry %v, want none", msTime(*expires))
			}

			// The object lifetime follows a write made before the completion,
			// and only the second value is what it records.
			writeSetting(t, store, objectTTLSetting, 2*time.Hour)
			before, after = uploadAndComplete(t, tctx, objects, first, body)
			_, expires = expiriesOf(t, reg, first)
			if expires == nil {
				t.Fatal("completing an object with a lifetime set recorded no expiry")
			}
			assertDeadline(t, "the first object's lifetime", *expires, before, after, 2*time.Hour)

			// A second declaration and completion see the newer values, so the
			// reads happen per call rather than once at construction.
			writeSetting(t, store, uploadWindowSetting, time.Hour)
			second, before, after := createObject(t, tctx, objects, "second", body)
			uploadExpiry, _ = expiriesOf(t, reg, second)
			assertDeadline(t, "the second object's upload window", uploadExpiry, before, after, time.Hour)

			writeSetting(t, store, objectTTLSetting, 30*time.Minute)
			before, after = uploadAndComplete(t, tctx, objects, second, body)
			_, expires = expiriesOf(t, reg, second)
			if expires == nil {
				t.Fatal("completing the second object with a lifetime set recorded no expiry")
			}
			assertDeadline(t, "the second object's lifetime", *expires, before, after, 30*time.Minute)
			return nil
		})
	})
}

// TestTheModuleDeclaresExactlyItsRuntimeTunables holds the line the design
// draws: the upload window and the object lifetime are settings, while the sweep
// interval sizes the startup ticker and stays a configuration item that a
// change requires a restart to pick up.
func TestTheModuleDeclaresExactlyItsRuntimeTunables(t *testing.T) {
	declared := declaredSettings(t)
	for _, key := range []string{uploadWindowSetting, objectTTLSetting} {
		if _, ok := declared[key]; !ok {
			t.Errorf("%s is not declared as a setting", key)
		}
	}
	for key := range declared {
		if key != uploadWindowSetting && key != objectTTLSetting {
			t.Errorf("%s is declared as a setting, but only the upload window and the object lifetime "+
				"take effect without a restart", key)
		}
	}
	if _, ok := declared[sweepIntervalKey]; ok {
		t.Error("sweep-interval is declared as a setting, but it sizes the startup ticker and is read " +
			"once, so a change still needs a restart")
	}
}

// TestAConfigurationCarryingARemovedTunableFailsStartup holds the move out of
// configuration: a configuration file that still carries the upload window or the
// object lifetime is an unknown key and refuses the startup, naming the key. The
// sweep interval is the control: it stays a configuration item and loads.
func TestAConfigurationCarryingARemovedTunableFailsStartup(t *testing.T) {
	for _, c := range []struct {
		item, value, key string
	}{
		{"upload-window", "3h", uploadWindowSetting},
		{"object-ttl", "2h", objectTTLSetting},
	} {
		t.Run(c.item, func(t *testing.T) {
			err := loadMetadataConfiguration(t, map[string]any{c.item: c.value})
			if !errors.Is(err, config.ErrUnknownKey) {
				t.Fatalf("a configuration carrying %s loaded with %v, want config.ErrUnknownKey", c.key, err)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("the refusal %q does not name the key %s", err, c.key)
			}
		})
	}

	t.Run("the sweep interval stays a configuration item", func(t *testing.T) {
		if err := loadMetadataConfiguration(t, map[string]any{"sweep-interval": "30s"}); err != nil {
			t.Fatalf("a configuration carrying sweep-interval was refused: %v; it sets the startup ticker "+
				"and stays a configuration item", err)
		}
	})
}

// TestTheDeclaredTunablesApplyWithNoStoredRow pins the values a run acts on when
// the settings store holds no row: a fifteen minute upload window and no expiry
// on a completed object. The store answers the declared default for a key with no
// row, so these literals are the ones the design fixes.
func TestTheDeclaredTunablesApplyWithNoStoredRow(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{withSettings: true})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id, before, after := createObject(t, tctx, objects, "default-window", []byte("body"))
			uploadExpiry, expires := expiriesOf(t, reg, id)
			assertDeadline(t, "the default upload window", uploadExpiry, before, after, 15*time.Minute)
			if expires != nil {
				t.Errorf("a declared object with no stored lifetime carries the expiry %v, want none", msTime(*expires))
			}
			return nil
		})
	})
}

// TestAMetadataTunableReadFaultFallsBackToItsDeclaredDefault holds the other
// direction of a read: a setting that cannot be read, and one whose stored value
// is outside its domain, each answers the declared default and writes one Error
// naming the setting.
func TestAMetadataTunableReadFaultFallsBackToItsDeclaredDefault(t *testing.T) {
	var report bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&report, nil))
	store := tunableStore{
		values:  map[string]any{objectTTLPath: time.Duration(-1)},
		failKey: uploadWindowPath,
		err:     errors.New("the store is down"),
	}
	r := newSettingsReader(store)

	if got := r.uploadWindow(logger); got != 15*time.Minute {
		t.Errorf("an upload window that could not be read is %s, want the declared 15m", got)
	}
	if got := r.objectTTL(logger); got != 0 {
		t.Errorf("an object lifetime outside its domain is %s, want the declared zero", got)
	}
	logged := report.String()
	for _, key := range []string{uploadWindowPath, objectTTLPath} {
		if !strings.Contains(logged, key) {
			t.Errorf("the report does not name %s:\n%s", key, logged)
		}
	}
}

// TestASubMillisecondMetadataTunableIsRefused holds the boundary the domains
// draw: both values are stored and read in whole milliseconds, so a positive
// value shorter than one millisecond, which would truncate to zero, is refused at
// the write.
func TestASubMillisecondMetadataTunableIsRefused(t *testing.T) {
	for _, key := range []string{uploadWindowPath, objectTTLPath} {
		t.Run(key, func(t *testing.T) {
			forEachEngine(t, func(t *testing.T, e engine) {
				h := e.on(t, harness{withSettings: true})
				h.drive(t, func(ctx context.Context, reg *core.Registry) error {
					err := settingsStoreOf(t, reg).Set(ctx, settings.Write{
						Layer: settings.Global(), Key: key, Value: 500 * time.Microsecond, Actor: "test",
					})
					if !errors.Is(err, settings.ErrInvalidValue) {
						t.Errorf("writing %s = 500us returned %v, want ErrInvalidValue", key, err)
					}
					return nil
				})
			})
		})
	}
}

// tunableStore is a settings store over named rows with one key made to fail its
// read, so a case can drive the two tunables without the settings module.
type tunableStore struct {
	values  map[string]any
	failKey string
	err     error
}

func (s tunableStore) Get(_ settings.Layer, key string, target any) error {
	if key == s.failKey {
		return s.err
	}
	value, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

func (s tunableStore) Set(context.Context, settings.Write) error { return nil }

func (s tunableStore) Delete(context.Context, settings.Layer, string, string) error { return nil }

func (s tunableStore) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (s tunableStore) Declared() []settings.DeclaredItem { return nil }

// declaredSettings returns the items of this module's settings declaration, or
// stops the case when it makes none.
func declaredSettings(t *testing.T) map[string]settings.Item {
	t.Helper()
	for _, resource := range Module().Resources {
		if declared, ok := resource.(settings.Schema); ok {
			return declared.Items
		}
	}
	t.Fatal("the module declares no settings.Schema, so its runtime tunables are not settings")
	return nil
}

// settingsStoreOf resolves the settings store of the assembled run.
func settingsStoreOf(t *testing.T, reg *core.Registry) settings.Store {
	t.Helper()
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		t.Fatalf("resolving the settings store: %v", err)
	}
	return store
}

// writeSetting writes one value into the running settings store. A refusal is
// fatal: the case exists to hold that the key is declared as a setting.
func writeSetting(t *testing.T, store settings.Store, key string, value any) {
	t.Helper()
	write := settings.Write{Layer: settings.Global(), Key: key, Value: value, Actor: "test"}
	if err := store.Set(context.Background(), write); err != nil {
		t.Fatalf("writing %s: %v, want it declared as a setting", key, err)
	}
}

// createObject declares one object and returns its identifier together with the
// instants the call was made between, so an expiry can be judged against them.
func createObject(t *testing.T, ctx context.Context, objects objectstore.Objects, name string, body []byte) (string, time.Time, time.Time) {
	t.Helper()
	before := time.Now()
	obj, err := objects.Create(ctx, objectstore.Declaration{Name: name, Size: int64(len(body)), ContentType: "text/plain"})
	after := time.Now()
	if err != nil {
		t.Fatalf("declaring %q: %v", name, err)
	}
	return obj.ID, before, after
}

// uploadAndComplete uploads an object's bytes and completes it, returning the
// instants the completion was made between.
func uploadAndComplete(t *testing.T, ctx context.Context, objects objectstore.Objects, id string, body []byte) (time.Time, time.Time) {
	t.Helper()
	if err := objects.Upload(ctx, id, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("uploading %s: %v", id, err)
	}
	before := time.Now()
	if _, err := objects.Complete(ctx, id); err != nil {
		t.Fatalf("completing %s: %v", id, err)
	}
	return before, time.Now()
}

// expiriesOf reads one object row's upload deadline and lifetime expiry.
func expiriesOf(t *testing.T, reg *core.Registry, id string) (int64, *int64) {
	t.Helper()
	var upload int64
	var expires sql.NullInt64
	if err := dbHandle(t, reg).Raw(
		"SELECT upload_expires_at, expires_at FROM objects WHERE id = ?", id).Row().Scan(&upload, &expires); err != nil {
		t.Fatalf("reading the expiries of the object %s: %v", id, err)
	}
	if expires.Valid {
		at := expires.Int64
		return upload, &at
	}
	return upload, nil
}

// assertDeadline holds that a written instant is about now plus want, judged
// between the two instants the call was made in.
func assertDeadline(t *testing.T, what string, got int64, before, after time.Time, want time.Duration) {
	t.Helper()
	slack := time.Second
	lo := before.Add(want).Add(-slack).UnixMilli()
	hi := after.Add(want).Add(slack).UnixMilli()
	if got < lo || got > hi {
		t.Errorf("%s was written as %s, want about now + %s (between %s and %s)",
			what, msTime(got), want, msTime(lo), msTime(hi))
	}
}

// loadMetadataConfiguration runs the real loader over a JSON primary source
// holding an objectstore.metadata section, returning what loading it answered.
// Unlike the harness it does not stop the case on a refusal: the refusal is what
// the case asserts.
func loadMetadataConfiguration(t *testing.T, section map[string]any) error {
	t.Helper()
	document := map[string]any{"objectstore": map[string]any{"metadata": section}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDOBJECTSTORESETTINGS", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	_, err = config.Load(t.Context(), reg, nil)
	return err
}
