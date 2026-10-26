package webui

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The cases in module_test.go run against a real assembly: the entry point
// module itself, listening on a port the operating system picks, with this
// module registered through New and driven by the lifecycle. What the module
// answers is read off a socket, so the routing, the answer to a method that
// matches no route and the shape of an error response are the entry point's and
// not a stand-in's.

// waitLimit bounds every wait in these cases, so that a startup which never
// reaches Serve fails the case with a message instead of the whole binary at
// the global timeout.
const waitLimit = 10 * time.Second

// endpointName is the one endpoint the assembly declares and this module serves
// on. The frontend is alone on it: a catch-all route and any concrete route on
// one method tree are mutually exclusive, so this endpoint carries no other
// route.
const endpointName = "public"

// standInLoggerName is the name the stand-in logger is registered under. A
// command that names the entry point names this one beside it, because the
// bound address it records is the only way a client outside the process learns
// the port the system picked.
const standInLoggerName = "stand-in.logger"

// host is one assembly that is serving.
type host struct {
	addr string
	reg  *core.Registry
}

// startHost builds an assembly that serves fsys through this module, and runs
// it until the entry point is listening. Every registered module takes part.
// The run is ended when the case is, and a run that ends with an error fails
// the case.
func startHost(t *testing.T, fsys fs.FS) *host {
	t.Helper()
	return startAssembly(t, fsys, loadReader(t), nil)
}

// startAssembly builds the assembly and runs it until the entry point is
// listening. participants is what the command tree names: nil lets every
// registered module take part, and a list drives only those modules, leaving
// the rest registered and unnamed, as a command that does not take a module in
// leaves it. extra modules are registered in addition to this package's own.
//
// The run is ended when the case is, and a run that ends with an error fails
// the case.
func startAssembly(t *testing.T, fsys fs.FS, reader config.Reader, participants []string, extra ...core.Module) *host {
	t.Helper()
	logs := &entryRecorder{}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(core.Module{
		Name:     standInLoggerName,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInLogger{logs: logs}, nil },
	})
	reg.Register(New(fsys))
	for _, m := range extra {
		reg.Register(m)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		if participants == nil {
			runErr = reg.Run(ctx)
			return
		}
		runErr = reg.Drive(ctx, core.Drive{Depth: core.StageServe, Participants: participants})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
	})

	// The entry point writes the address of its bound socket once it is
	// listening; that is the only way a client outside the process learns the
	// port the system picked.
	deadline := time.Now().Add(waitLimit)
	for {
		if addr := logs.address(); addr != "" {
			return &host{addr: addr, reg: reg}
		}
		select {
		case <-done:
			t.Fatalf("the assembly ended before it served: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// response is what a client got back.
type response struct {
	status int
	header nethttp.Header
	body   string
}

// send issues one request over a connection of its own and reads the response
// to its end.
func (h *host) send(t *testing.T, method, path string) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, "http://"+h.addr+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	client := &nethttp.Client{
		Timeout:   waitLimit,
		Transport: &nethttp.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response to %s %s: %v", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// get is send for a GET.
func (h *host) get(t *testing.T, path string) response {
	t.Helper()
	return h.send(t, nethttp.MethodGet, path)
}

// loadReader loads the configuration this assembly runs on through the real
// loader: the entry point declares one endpoint on a port of the system's
// choosing, and this module serves on it.
func loadReader(t *testing.T) config.Reader {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"http": map[string]any{
			"listeners": map[string]any{
				endpointName: map[string]any{"address": "127.0.0.1:0"},
			},
			"endpoints": map[string]any{
				endpointName: map[string]any{"listener": endpointName},
			},
		},
		"webui": map[string]any{"endpoint": endpointName},
	})
	if err != nil {
		t.Fatalf("marshalling the config document: %v", err)
	}
	return loadDocument(t, document)
}

// loadDocument loads one configuration document through the real loader, with
// the entry point's and this module's declarations registered as a host's run
// would register them. It is how a case holds a document to what the modules
// declare: an item the schema does not declare, or a field whose tag names
// another key, comes out here.
func loadDocument(t *testing.T, document []byte) config.Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "WEBUITEST", DefaultLocator: "file:" + path}},
	})
	// The declarations reach the load as the resources of registered modules,
	// which is the only way one ever reaches it. New(nil) is how this module's
	// schema is reached without an assembly to serve.
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: New(nil).Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// entryRecorder is a slog handler that keeps the address of the entry point's
// bound socket, which is the only way a client outside the process learns the
// port the system picked.
type entryRecorder struct {
	mu   sync.Mutex
	addr string
}

func (*entryRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *entryRecorder) Handle(_ context.Context, rec slog.Record) error {
	if rec.Message != "an HTTP listener is listening" {
		return nil
	}
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key != "address" {
			return true
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.addr = a.Value.String()
		return true
	})
	return nil
}

func (r *entryRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *entryRecorder) WithGroup(string) slog.Handler      { return r }

func (r *entryRecorder) address() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addr
}

// standInLogger delivers the Logger capability over an entryRecorder.
type standInLogger struct{ logs *entryRecorder }

func (l standInLogger) Named(string) *slog.Logger { return slog.New(l.logs) }
func (standInLogger) Redaction() log.Redaction    { return noRedaction{} }

var _ log.Logger = standInLogger{}

// noRedaction takes registrations and masks nothing: what these cases observe
// of the Logger capability is only the address the entry point reports.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

var _ log.Redaction = noRedaction{}
