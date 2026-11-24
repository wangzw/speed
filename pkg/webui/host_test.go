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
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

const endpointName = "public"

const standInLoggerName = "stand-in.logger"

type host struct {
	addr string
	reg  *core.Registry
}

func startHost(t *testing.T, fsys fs.FS) *host {
	t.Helper()
	return startAssembly(t, fsys, loadReader(t), nil)
}

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
	limit := waitLimit()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("the assembly did not stop cleanly: %v", runErr)
			}
		case <-time.After(limit):
			t.Errorf("the assembly did not stop within %s", limit)
		}
	})

	deadline := time.Now().Add(limit)
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
			t.Fatalf("the assembly did not reach Serve within %s", limit)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type response struct {
	status int
	header nethttp.Header
	body   string
}

func (h *host) send(t *testing.T, method, path string) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, "http://"+h.addr+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	client := &nethttp.Client{
		Timeout:   waitLimit(),
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

func (h *host) get(t *testing.T, path string) response {
	t.Helper()
	return h.send(t, nethttp.MethodGet, path)
}

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
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: New(nil).Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

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

type standInLogger struct{ logs *entryRecorder }

func (l standInLogger) Named(string) *slog.Logger { return slog.New(l.logs) }
func (standInLogger) Redaction() log.Redaction    { return noRedaction{} }

var _ log.Logger = standInLogger{}

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

var _ log.Redaction = noRedaction{}
