package wechat

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/log"
)

func capturedLogs(t *testing.T, action func(ctx context.Context)) string {
	t.Helper()
	var buf bytes.Buffer
	ctx := log.WithLogger(t.Context(), slog.New(slog.NewTextHandler(&buf, nil)))
	action(ctx)
	return buf.String()
}

// TestTheDefaultClientRecordsEachExchange holds one record per exchange, success and failure, through the context's logger.
func TestTheDefaultClientRecordsEachExchange(t *testing.T) {
	t.Run("an answered exchange", func(t *testing.T) {
		srv, pool, _ := tokenServer(t, http.StatusOK, `{"openid":"OPEN-1"}`)
		c := New(Options{
			AppID: testAppID, AppSecret: testAppSecret,
			TokenEndpoint: srv.URL + "/sns/oauth2/access_token",
			AllowLoopback: true, RootCAs: pool,
		})

		out := capturedLogs(t, func(ctx context.Context) {
			if _, err := c.Exchange(ctx, "CODE-SECRET"); err != nil {
				t.Fatalf("Exchange: %v", err)
			}
		})
		for _, want := range []string{
			`msg="log: an outbound HTTP request completed"`,
			"method=GET",
			"host=" + strings.TrimPrefix(srv.URL, "https://"),
			"path=/sns/oauth2/access_token",
			"http_status=200",
			"outcome=ok",
			"duration_ms=",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the record carries no %q: %s", want, out)
			}
		}
		assertNoExchangeSecret(t, out)
	})

	t.Run("a refusal the provider stated", func(t *testing.T) {
		srv, pool, _ := tokenServer(t, http.StatusBadGateway, `{}`)
		c := New(Options{
			AppID: testAppID, AppSecret: testAppSecret,
			TokenEndpoint: srv.URL + "/sns/oauth2/access_token",
			AllowLoopback: true, RootCAs: pool,
		})

		out := capturedLogs(t, func(ctx context.Context) {
			if _, err := c.Exchange(ctx, "CODE-SECRET"); err == nil {
				t.Fatal("Exchange succeeded, want an outage")
			}
		})
		for _, want := range []string{
			`msg="log: an outbound HTTP request failed"`,
			"path=/sns/oauth2/access_token",
			"http_status=502",
			"outcome=failed",
			"duration_ms=",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the record carries no %q: %s", want, out)
			}
		}
		assertNoExchangeSecret(t, out)
	})

	t.Run("a call that never reached the provider", func(t *testing.T) {
		srv, _, _ := tokenServer(t, http.StatusOK, `{"openid":"OPEN-1"}`)
		c := New(Options{
			AppID: testAppID, AppSecret: testAppSecret,
			TokenEndpoint: srv.URL + "/sns/oauth2/access_token",
		})

		out := capturedLogs(t, func(ctx context.Context) {
			if _, err := c.Exchange(ctx, "CODE-SECRET"); err == nil {
				t.Fatal("Exchange succeeded, want the restricted client to refuse loopback")
			}
		})
		for _, want := range []string{
			`msg="log: an outbound HTTP request failed"`,
			"outcome=failed",
			"cause=",
			"duration_ms=",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the record carries no %q: %s", want, out)
			}
		}
		assertNoExchangeSecret(t, out)
	})
}

func assertNoExchangeSecret(t *testing.T, out string) {
	t.Helper()
	for _, unwanted := range []string{testAppSecret, "CODE-SECRET", "secret=", "code=", "appid=", "?"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the record carries %q, which comes from the query: %s", unwanted, out)
		}
	}
}
