package github

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// TestTheOutboundClientRecordsEachRequest holds that the client this module
// assembles writes one record per outbound request, through the logger the
// request's context carries.
//
// The product is taken from a real registry driven to the stage where the
// instance exists, so the assertion is on what the module's own New assembles: a
// case that wrapped a client by hand would pass with the wrap removed from New
// and hold nothing about this module.
//
// The request names a scheme the restricted client refuses before it opens a
// connection. That is deliberate: the record covers the call the method made,
// whether or not it reached the provider, and this failure is what a case can
// drive without a certificate the production client would reject.
//
// The query carries a value shaped like an authorization code and one shaped
// like a state, and the read's header carries a value shaped like a token.
// None may reach the record, which is what keeps a code, a token or a state out
// of every destination the logging chain writes to.
func TestTheOutboundClientRecordsEachRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			delivered, err := core.Resolve[authn.LoginMethod](reg)
			if err != nil {
				return fmt.Errorf("resolving the login method: %w", err)
			}
			m, ok := delivered.(*method)
			if !ok {
				return fmt.Errorf("the delivered method is %T, not this module's product", delivered)
			}
			req, err := http.NewRequestWithContext(log.WithLogger(t.Context(), logger), http.MethodGet,
				"http://api.github.com/user?code=CODE-SECRET&state=STATE-SECRET", nil)
			if err != nil {
				return fmt.Errorf("building the request: %w", err)
			}
			req.Header.Set("Authorization", "Bearer TOKEN-SECRET")
			if _, err := m.client.Do(req); err == nil {
				return fmt.Errorf("the restricted client accepted a request that is not https")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the registry to the instance: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		`msg="log: an outbound HTTP request failed"`,
		"method=GET",
		"host=api.github.com",
		"path=/user",
		"outcome=failed",
		"duration_ms=",
		"cause=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the outbound record carries no %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"CODE-SECRET", "STATE-SECRET", "TOKEN-SECRET", "code=", "state=", "?"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the outbound record carries %q, which comes from the request: %s", unwanted, out)
		}
	}
}
