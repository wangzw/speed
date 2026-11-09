package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event/memory"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
	objectstorefs "github.com/vislake/speed/pkg/objectstore/fs"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// engine is one database capability this module is exercised on.
type engine struct {
	name     string
	database core.Module
	section  string
	locate   func(t *testing.T) string
}

// engines lists the capabilities this build runs the cases on. PostgreSQL is
// appended by the tagged file, which is also where its locator lives: without
// the tag it is not in the list, and no untagged case names it.
var engines = []engine{
	{
		name:     "sqlite",
		database: sqlite.Module(),
		section:  "sqlite",
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "objects.db") },
	},
}

// forEachEngine runs a case on every engine.
func forEachEngine(t *testing.T, run func(t *testing.T, e engine)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e) })
	}
}

// harness is one assembled run of this module: the engine it sits on, the
// module's own configuration section, the byte seam and the optional surfaces
// the case supplies, and the stage the run reaches.
type harness struct {
	database core.Module
	section  string
	dsn      string
	metadata map[string]any
	store    *fakeStore
	// router stands in for the entry point, so a case can see where the content
	// route was mounted.
	router speedhttp.Router
	// direct stands in for a backend that mints its own addresses; when it is
	// set the content route must not be mounted.
	direct objectstore.DirectURL
	// fsRoot, when set, assembles the real local-directory backend over that
	// directory instead of the in-memory stand-in, so a case can drive bytes
	// through the backend the content route serves.
	fsRoot string
	// withSettings assembles the settings module and its event bus, so a case
	// can write a runtime tunable and watch the next call read it.
	withSettings bool
	depth        core.Stage
}

// metadataBody is the module's default configuration: one content endpoint named
// "public" and the sweep turned off. The upload window, the object lifetime and
// the URL lifetime are settings and are not configuration items.
func metadataBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"content-endpoints": []string{"public"},
		"sweep-interval":    "0s",
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// on builds a harness on an engine over a fresh database of that engine's own.
func (e engine) on(t *testing.T, h harness) harness {
	h.database = e.database
	h.section = e.section
	h.dsn = e.locate(t)
	return h
}

// assemble loads the configuration the way a host loads it and returns the
// registry to drive together with the names of the modules taking part.
func (h harness) assemble(t *testing.T) (*core.Registry, []string) {
	t.Helper()
	dir := t.TempDir()
	objectstoreBody := map[string]any{"metadata": metadataBody(h.metadata)}
	if h.fsRoot != "" {
		objectstoreBody["fs"] = map[string]any{"root": h.fsRoot}
	}
	document := map[string]any{
		"db": map[string]any{h.section: map[string]any{"dsn": h.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
		"objectstore": objectstoreBody,
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	modules := []core.Module{
		h.database,
		log.Module(),
		catalog.Module(),
		Module(),
	}
	if h.fsRoot != "" {
		modules = append(modules, objectstorefs.Module())
	} else {
		store := h.store
		if store == nil {
			store = newFakeStore()
		}
		modules = append(modules, store.module())
	}
	if h.router != nil {
		modules = append(modules, standIn("stand-in.router", h.router, (*speedhttp.Router)(nil)))
	}
	if h.direct != nil {
		modules = append(modules, standIn("stand-in.direct", h.direct, (*objectstore.DirectURL)(nil)))
	}
	if h.withSettings {
		modules = append(modules, memory.Module(), settings.Module())
	}

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDOBJECTSTORETEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(modules))
	seen := map[string]bool{}
	for _, m := range modules {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)
	return reg, names
}

// driveTo drives the assembly to depth and returns the drive's error, so a case
// can tell which stage a refusal landed at.
func (h harness) driveTo(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reg, names := h.assemble(t)
	return reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action:       action,
	})
}

// drive drives a fresh assembly to the run's depth and runs action there,
// ending the case if the drive fails.
func (h harness) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	depth := h.depth
	if depth == 0 {
		depth = core.StageStart
	}
	if err := h.driveTo(t, depth, action); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// objectsOf resolves the metadata seam.
func objectsOf(t *testing.T, reg *core.Registry) objectstore.Objects {
	t.Helper()
	objects, err := core.Resolve[objectstore.Objects](reg)
	if err != nil {
		t.Fatalf("resolving the object store: %v", err)
	}
	return objects
}

// productOfAssembly narrows the resolved seam to this module's product.
func productOfAssembly(t *testing.T, reg *core.Registry) *product {
	t.Helper()
	objects := objectsOf(t, reg)
	p, ok := objects.(*product)
	if !ok {
		t.Fatalf("the object store is a %T, not this module's product", objects)
	}
	return p
}

// tenantCtx registers a tenant in the roster and returns a context that belongs
// to it.
func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := roster.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Name: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := roster.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

// dbHandle resolves the database handle.
func dbHandle(t *testing.T, reg *core.Registry) *gorm.DB {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	return database.DB()
}

// fakeStore is a stand-in byte seam: an in-memory map plus the failures and the
// deletion log a case steers.
type fakeStore struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	putErr  error
	openErr error
	statErr error
	delErr  error
	// notSeekable makes Open answer a stream ServeContent cannot seek, which
	// exercises the full-copy branch.
	notSeekable bool
	deleted     []string
}

func newFakeStore() *fakeStore { return &fakeStore{blobs: map[string][]byte{}} }

func (s *fakeStore) module() core.Module {
	return standIn("stand-in.store", s, (*objectstore.Store)(nil))
}

func (s *fakeStore) Name() string { return "fake" }

func (s *fakeStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.blobs[key] = data
	return nil
}

// readSeekCloser is what the local-directory backend answers Open with: an
// *os.File is an io.ReadSeeker, and the content route uses that to let
// ServeContent answer ranges and HEAD.
type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

func (s *fakeStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return nil, s.openErr
	}
	data, ok := s.blobs[key]
	if !ok {
		// The file system's own not-exist error, as the fs backend reports it.
		return nil, fmt.Errorf("%w: no bytes under that key", fs.ErrNotExist)
	}
	reader := bytes.NewReader(data)
	if s.notSeekable {
		return io.NopCloser(reader), nil
	}
	return readSeekCloser{reader}, nil
}

func (s *fakeStore) Stat(_ context.Context, key string) (objectstore.Blob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statErr != nil {
		return objectstore.Blob{}, s.statErr
	}
	data, ok := s.blobs[key]
	if !ok {
		return objectstore.Blob{}, fmt.Errorf("%w: no bytes under that key", objectstore.ErrContentMissing)
	}
	return objectstore.Blob{Size: int64(len(data))}, nil
}

func (s *fakeStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delErr != nil {
		return s.delErr
	}
	delete(s.blobs, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.blobs[key]
	return ok
}

func (s *fakeStore) deletions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

// standIn is a module that delivers instance for the tokens named.
func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// declare puts an object through declaration, upload and completion and returns
// its identifier.
func declare(t *testing.T, ctx context.Context, objects objectstore.Objects, name, body string) string {
	t.Helper()
	obj, err := objects.Create(ctx, objectstore.Declaration{
		Name: name, Size: int64(len(body)), ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("declaring %q: %v", name, err)
	}
	if err := objects.Upload(ctx, obj.ID, bytes.NewReader([]byte(body)), int64(len(body))); err != nil {
		t.Fatalf("uploading %q: %v", name, err)
	}
	if _, err := objects.Complete(ctx, obj.ID); err != nil {
		t.Fatalf("completing %q: %v", name, err)
	}
	return obj.ID
}
