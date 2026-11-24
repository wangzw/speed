package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const entryName = "api"

type jsonConfig string

func (c jsonConfig) Decode(_ string, target any) error {
	return json.Unmarshal([]byte(c), target)
}

type entryLogger struct{ lg *slog.Logger }

func (l entryLogger) Named(string) *slog.Logger { return l.lg }
func (entryLogger) Redaction() log.Redaction    { return nil }

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

func listeningAddress(t *testing.T, logs *syncBuffer, listener string) string {
	t.Helper()
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record["msg"] != "an HTTP listener is listening" || record["listener"] != listener {
			continue
		}
		address, ok := record["address"].(string)
		if !ok {
			t.Fatalf("the listening record for %q carries no address: %v", listener, record)
		}
		return address
	}
	t.Fatalf("the entry module wrote no listening record for %q; its log is:\n%s", listener, logs.String())
	return ""
}

type entryHost struct {
	base   string
	logs   *syncBuffer
	client *nethttp.Client
}

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

func serve(t *testing.T, p *providers, register func(ep speedhttp.Endpoint), fn func(h *entryHost)) {
	t.Helper()
	logs := &syncBuffer{}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(jsonConfig(`{"listeners":{"`+entryName+`":{"address":"127.0.0.1:0"}},`+
		`"endpoints":{"`+entryName+`":{"listener":"`+entryName+`"}}}`), config.ModuleName)
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
			p.attach(ep, entryName)
			register(ep)
			return nil
		},
	})

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: []string{speedhttp.ModuleName, "test.registrant"},
		Action: func(context.Context, *core.Registry) error {
			fn(&entryHost{
				base: "http://" + listeningAddress(t, logs, entryName),
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
