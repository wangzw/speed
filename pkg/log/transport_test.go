package log

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func captureTransportLogs(t *testing.T, action func(ctx context.Context)) string {
	t.Helper()
	var buf bytes.Buffer
	ctx := WithLogger(t.Context(), slog.New(slog.NewTextHandler(&buf, nil)))
	action(ctx)
	return buf.String()
}

// TestTransportRecordsAnAnsweredRequest holds the per-request record for the statuses a request
// can end on: the outcome, the status, the endpoint and the time.
func TestTransportRecordsAnAnsweredRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		outcome string
		message string
	}{
		{"a 200", http.StatusOK, outcomeOK, "log: an outbound HTTP request completed"},
		{"a redirect", http.StatusFound, outcomeFailed, "log: an outbound HTTP request failed"},
		{"a server error", http.StatusInternalServerError, outcomeFailed, "log: an outbound HTTP request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			out := captureTransportLogs(t, func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet,
					server.URL+"/token?code=CODE-SECRET&state=STATE-SECRET", nil)
				if err != nil {
					t.Fatalf("building the request: %v", err)
				}
				client := &http.Client{
					Transport: Transport(http.DefaultTransport),
					CheckRedirect: func(*http.Request, []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("the call failed: %v", err)
				}
				_ = resp.Body.Close()
			})

			host := strings.TrimPrefix(server.URL, "http://")
			for _, want := range []string{
				`msg="` + tc.message + `"`,
				"method=GET",
				"host=" + host,
				"path=/token",
				"http_status=" + strconv.Itoa(tc.status),
				"outcome=" + tc.outcome,
				"duration_ms=",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the record carries no %q: %s", want, out)
				}
			}
			for _, unwanted := range []string{"CODE-SECRET", "STATE-SECRET", "code=", "state=", "?"} {
				if strings.Contains(out, unwanted) {
					t.Errorf("the record carries %q, which comes from the query: %s", unwanted, out)
				}
			}
			if got := strings.Count(out, "\n"); got != 1 {
				t.Errorf("the call wrote %d records, want one: %s", got, out)
			}
		})
	}
}

// TestTransportRecordsANetworkFailure holds the other ending: a call that produces no response at
// all.
func TestTransportRecordsANetworkFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving an address: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the reserved listener: %v", err)
	}

	out := captureTransportLogs(t, func(ctx context.Context) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+addr+"/token?code=CODE-SECRET&state=STATE-SECRET", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		client := &http.Client{Transport: Transport(http.DefaultTransport)}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			t.Fatal("the call to a closed port succeeded")
		}
	})

	for _, want := range []string{
		`msg="log: an outbound HTTP request failed"`,
		"method=POST",
		"host=" + addr,
		"path=/token",
		"outcome=failed",
		"duration_ms=",
		"cause=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the record carries no %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"CODE-SECRET", "STATE-SECRET", "code=", "state=", "?"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the record carries %q, which comes from the query: %s", unwanted, out)
		}
	}
}
