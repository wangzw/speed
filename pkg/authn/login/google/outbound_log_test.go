package google

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// TestTheOutboundClientRecordsEachRequest holds one record per outbound request through the context's logger.
func TestTheOutboundClientRecordsEachRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	err := driveWith(t, validReader(), func(_ context.Context, reg *core.Registry) error {
		methods := core.ResolveAll[authn.LoginMethod](reg)
		if len(methods) != 1 {
			return fmt.Errorf("the drive delivered %d login methods, want this module's one", len(methods))
		}
		p, ok := methods[0].(*product)
		if !ok {
			return fmt.Errorf("the delivered method is %T, not this module's product", methods[0])
		}
		req, err := http.NewRequestWithContext(log.WithLogger(t.Context(), logger), http.MethodGet,
			"http://accounts.example.com/token?code=CODE-SECRET&state=STATE-SECRET", nil)
		if err != nil {
			return fmt.Errorf("building the request: %w", err)
		}
		if _, err := p.client.Do(req); err == nil {
			return fmt.Errorf("the restricted client accepted a request that is not https")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("driving the registry to the instance: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		`msg="log: an outbound HTTP request failed"`,
		"method=GET",
		"host=accounts.example.com",
		"path=/token",
		"outcome=failed",
		"duration_ms=",
		"cause=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the outbound record carries no %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"CODE-SECRET", "STATE-SECRET", "code=", "state=", "?"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the outbound record carries %q, which comes from the query: %s", unwanted, out)
		}
	}
}
