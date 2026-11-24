package ops

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const listeningMsg = "an HTTP listener is listening"

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

type host struct {
	addr string
	logs *recorder
}

type hostOptions struct {
	config   string
	database db.Database
}

func startHost(t *testing.T, opts hostOptions) *host {
	t.Helper()
	logs := newRecorder()

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(loadReader(t, opts.config), config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInLogger{logs: logs}, nil },
	})
	if opts.database != nil {
		reg.Register(core.Module{
			Name:     "stand-in.database",
			Provides: []core.Provision{{Token: (*db.Database)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return opts.database, nil },
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
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

	return &host{addr: logs.waitListening(t, done, &runErr), logs: logs}
}

func loadReader(t *testing.T, document string) config.Reader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDOPSTEST", DefaultLocator: "file:" + path}},
	})
	reg.Register(core.Module{Name: speedhttp.ModuleName, Resources: speedhttp.Module().Resources})
	reg.Register(core.Module{Name: ModuleName, Resources: Module().Resources})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

type response struct {
	status int
	header nethttp.Header
	body   string
}

func (h *host) get(t *testing.T, path string) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), nethttp.MethodGet, "http://"+h.addr+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	client := &nethttp.Client{Timeout: waitLimit(), Transport: &nethttp.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response to GET %s: %v", path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type recorder struct {
	mu      *sync.Mutex
	records *[]record
	bound   []slog.Attr
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, records: &[]record{}}
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.bound)+rec.NumAttrs())
	for _, a := range r.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.records = append(*r.records, record{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := append(append([]slog.Attr(nil), r.bound...), attrs...)
	return &recorder{mu: r.mu, records: r.records, bound: bound}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) all() []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record(nil), *r.records...)
}

func (r *recorder) withMessage(prefix string) []record {
	var out []record
	for _, rec := range r.all() {
		if strings.HasPrefix(rec.msg, prefix) {
			out = append(out, rec)
		}
	}
	return out
}

func (r *recorder) waitListening(t *testing.T, done <-chan struct{}, runErr *error) string {
	t.Helper()
	limit := waitLimit()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if recs := r.withMessage(listeningMsg); len(recs) > 0 {
			return recs[0].attrs["address"]
		}
		select {
		case <-done:
			t.Fatalf("the assembly ended before it served: %v", *runErr)
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the assembly did not bind an endpoint within %s", limit)
	return ""
}

type standInLogger struct{ logs *recorder }

func (l standInLogger) Named(name string) *slog.Logger {
	return slog.New(l.logs.WithAttrs([]slog.Attr{slog.String("logger", name)}))
}

func (standInLogger) Redaction() log.Redaction { return noRedaction{} }

var _ log.Logger = standInLogger{}

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

type fakeRouter struct {
	endpoints map[string]speedhttp.Endpoint
	names     []string
}

func (r fakeRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	e, ok := r.endpoints[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return e, nil
}

func (r fakeRouter) Endpoints() []string { return r.names }

type fakeEndpoint struct{ accepting bool }

func (fakeEndpoint) Route(string, string, ...gin.HandlerFunc) {}
func (fakeEndpoint) Use(speedhttp.Middleware)                 {}
func (e fakeEndpoint) Accepting() bool                        { return e.accepting }

type fakeDatabase struct{ handle *gorm.DB }

func (d fakeDatabase) DB() *gorm.DB                                 { return d.handle }
func (fakeDatabase) Dialect() db.Dialect                            { return db.SQLite }
func (fakeDatabase) BlindIndex(string) string                       { return "" }
func (fakeDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

var _ db.Database = fakeDatabase{}

func brokenDatabase(t *testing.T) db.Database {
	t.Helper()
	handle, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("opening an in-memory handle: %v", err)
	}
	if err := db.Close(handle); err != nil {
		t.Fatalf("closing the handle's pool: %v", err)
	}
	return fakeDatabase{handle: handle}
}
