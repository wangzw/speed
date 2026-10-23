package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
)

// bufferLogger is a logging capability whose Named logger writes into buf with
// the module name bound, the way the factory's own logger does.
type bufferLogger struct{ buf *bytes.Buffer }

func (l bufferLogger) Named(name string) *slog.Logger {
	return slog.New(slog.NewTextHandler(l.buf, nil)).With("module", name)
}

func (bufferLogger) Redaction() log.Redaction { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

// registryWithReader builds the minimal assembly this module's own config
// reading needs: a real configuration loader over a JSON primary source holding
// body, with this module registered so its declared items are known, supplied
// under the config module's name and registered so the descriptor can be driven.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()

	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}

	loader := core.New()
	loader.Register(jsonformat.Module())
	loader.Register(filesource.Module())
	loader.Register(core.Module{
		Name: "host",
		Resources: []any{config.HostIdentity{
			Prefix:         "SPEEDOBJECTSTORES3TEST",
			DefaultLocator: "file://" + locator,
		}},
	})
	loader.Register(Module())
	reader, err := config.Load(t.Context(), loader, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	return reg
}

// completeBody is the configuration body a working assembly is built from.
func completeBody(endpoint string) string {
	return `{"objectstore":{"s3":{` +
		`"endpoint":` + mustJSON(endpoint) + `,` +
		`"region":"us-east-1",` +
		`"bucket":"` + testBucket + `",` +
		`"access-key":"` + testAccessKey + `",` +
		`"secret-key":"` + testSecretKey + `",` +
		`"use-path-style":true}}}`
}

// mustJSON encodes one JSON string, so an endpoint carrying a scheme still
// produces a valid body.
func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestTheDescriptorDeliversTheByteSeamExclusively holds the shape the design
// names: the objectstore.Store seam and the objectstore.DirectURL capability
// claimed for this module alone, so a command names exactly one backend, and the
// logging capability taken up optionally.
func TestTheDescriptorDeliversTheByteSeamExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	seams := map[core.Token]bool{
		core.Token((*objectstore.Store)(nil)):     false,
		core.Token((*objectstore.DirectURL)(nil)): false,
	}
	if len(m.Provides) != len(seams) {
		t.Fatalf("Provides = %+v, want the byte seam and the direct-URL capability", m.Provides)
	}
	for _, p := range m.Provides {
		if _, ok := seams[p.Token]; !ok || !p.Exclusive {
			t.Errorf("Provides carries %+v, want both seams delivered exclusively", p)
			continue
		}
		seams[p.Token] = true
	}
	for token, found := range seams {
		if !found {
			t.Errorf("Provides does not claim %v exclusively", token)
		}
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the one optional logging capability", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want an optional logging capability", r)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one input-item schema", m.Resources)
	}
}

// TestPrepareStatesEnabledForARunnableConfiguration holds that a complete
// configuration lets the module state it runs.
func TestPrepareStatesEnabledForARunnableConfiguration(t *testing.T) {
	reg := registryWithReader(t, completeBody("https://s3.example.com"))
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestTheModuleDeliversTheStoreThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves the byte seam and the direct-URL
// capability from the one product and names it.
func TestTheModuleDeliversTheStoreThroughTheRegistry(t *testing.T) {
	server := httptest.NewServer(recorder(&requestRecord{}, http.StatusOK, "ok"))
	defer server.Close()

	reg := registryWithReader(t, completeBody(endpointOf(server)))
	var store objectstore.Store
	var direct objectstore.DirectURL
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var resolveErr error
			if store, resolveErr = core.Resolve[objectstore.Store](reg); resolveErr != nil {
				return resolveErr
			}
			direct, resolveErr = core.Resolve[objectstore.DirectURL](reg)
			return resolveErr
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if store == nil || store.Name() != backendName {
		t.Errorf("the byte seam resolved %v, want one named %s", store, backendName)
	}
	if direct == nil {
		t.Fatal("the direct-URL capability resolved nothing")
	}
	if direct != store.(objectstore.DirectURL) {
		t.Errorf("the two seams resolved different instances: %T and %T", store, direct)
	}
}

// TestTheCredentialsAreRegisteredAsRedactionRules holds that the module hands
// its credentials to the factory's secrets hook, which is where they are
// registered as literal redaction rules before the client is built.
func TestTheCredentialsAreRegisteredAsRedactionRules(t *testing.T) {
	got := secrets(&moduleConfig{AccessKey: "the-key", SecretKey: "the-secret"})
	want := map[string]bool{"the-key": true, "the-secret": true}
	if len(got) != len(want) {
		t.Fatalf("secrets = %v, want the access key and the secret", got)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("secrets = %v, want the access key and the secret", got)
		}
	}
}

// TestTheStoreReportsThroughTheLoggerNamedAfterTheModule drives the factory's
// New with a logging capability supplying a logger that binds the name it is
// given, and holds that a byte operation's line arrives on that logger with the
// module attribute: this is the logger the factory takes up and hands to Open,
// which is what makes a background sweep's Delete reach the configured
// destinations.
func TestTheStoreReportsThroughTheLoggerNamedAfterTheModule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var out bytes.Buffer
	reg := registryWithReader(t, completeBody(endpointOf(server)))
	reg.Register(core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
		New: func(context.Context, *core.Registry) (any, error) {
			return bufferLogger{buf: &out}, nil
		},
	})

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName, "stand-in.logging"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			store, err := core.Resolve[objectstore.Store](reg)
			if err != nil {
				return err
			}
			return store.Delete(ctx, testKey)
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	line := out.String()
	for _, field := range []string{
		"module=" + ModuleName,
		"operation=delete",
		"outcome=ok",
		"key_hash=",
	} {
		if !strings.Contains(line, field) {
			t.Errorf("the call line lacks %q: %s", field, line)
		}
	}
	if strings.Contains(line, testKey) {
		t.Errorf("the call line carries the internal key: %s", line)
	}
}
