package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The cases that observe the request layer drive the real entry module: its
// engine, its fallback and a socket it bound. What the layer reads from the
// response writer, what a panic does on its way out and which requests reach it
// at all are properties of that assembly, and a router written in a test would
// only hold what the test already assumed of it.

// entryName is the one endpoint every assembly here declares.
const entryName = "api"

// jsonConfig is a reading face that answers every decode from one JSON
// document. The entry module's carrier is not exported, so the document names
// its keys the way the module's own configuration does.
type jsonConfig string

func (c jsonConfig) Decode(_ string, target any) error {
	return json.Unmarshal([]byte(c), target)
}

// entryLogger is the logging capability over a logger the case holds, which the
// entry module injects into every request's context.
type entryLogger struct{ lg *slog.Logger }

func (l entryLogger) Named(string) *slog.Logger { return l.lg }
func (entryLogger) Redaction() log.Redaction    { return nil }

// syncBuffer is a buffer the request goroutines and the case both touch.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// freeAddress is a loopback address nothing is listening on. The entry module
// takes its address from configuration and does not report the one it bound, so
// the case picks the port first.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("picking a free port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("releasing the picked port: %v", err)
	}
	return addr
}

// entryHost is a running entry module as a client sees it.
type entryHost struct {
	base string
	// logs is what the injected logger wrote, which includes the entry
	// module's own records.
	logs   *syncBuffer
	client *nethttp.Client
}

// do sends one request and returns the response, or the error a client gets
// when the server gave it no response at all.
func (h *entryHost) do(t *testing.T, method, path string, header ...string) (*nethttp.Response, error) {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), method, h.base+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	return h.client.Do(req)
}

// status sends one request and returns the status code the client received. The
// body is read to its end before the call returns, so a response the server cut
// short does not leave the connection in an unknown state.
func (h *entryHost) status(t *testing.T, method, path string, header ...string) int {
	t.Helper()
	resp, err := h.do(t, method, path, header...)
	if err != nil {
		t.Fatalf("%s %s got no response: %v", method, path, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// serve assembles the entry module with the layer p builds on its endpoint,
// beside whatever register puts there, binds a real port and runs fn against it.
// The drive ends, and the endpoint with it, when fn returns.
func serve(t *testing.T, p *providers, register func(ep speedhttp.Endpoint), fn func(h *entryHost)) {
	t.Helper()
	logs := &syncBuffer{}
	addr := freeAddress(t)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(jsonConfig(`{"endpoints":{"`+entryName+`":{"address":"`+addr+`"}}}`), config.ModuleName)
	reg.Supply(entryLogger{lg: slog.New(slog.NewJSONHandler(logs, nil))}, log.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(core.Module{
		Name:     "test.registrant",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint(entryName)
			if err != nil {
				return err
			}
			ep.Use(p.layer(entryName))
			register(ep)
			return nil
		},
	})

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: []string{speedhttp.ModuleName, "test.registrant"},
		Action: func(context.Context, *core.Registry) error {
			fn(&entryHost{
				base: "http://" + addr,
				logs: logs,
				client: &nethttp.Client{
					Timeout:   5 * time.Second,
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the entry module's drive failed: %v", err)
	}
}
