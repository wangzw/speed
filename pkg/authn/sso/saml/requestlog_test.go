package saml

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// TestTheMetadataRefreshRecordsEachRequest checks that every metadata refresh request leaves one record.
func TestTheMetadataRefreshRecordsEachRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	idp := newIDP(t)
	providers := newFakeProviders(providerRow(providerConfigJSON(t, idp, nil)))
	reg := buildRegistry(t, providers)
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants(),
		Action: func(ctx context.Context, reg *core.Registry) error {
			methods := core.ResolveAll[authn.LoginMethod](reg)
			if len(methods) != 1 {
				return fmt.Errorf("the drive delivered %d login methods, want this module's one", len(methods))
			}
			p, ok := methods[0].(*product)
			if !ok {
				return fmt.Errorf("the delivered method is %T, not this module's product", methods[0])
			}
			if _, err := p.fetchMetadata(log.WithLogger(ctx, logger),
				"http://idp.example.com/metadata?token=TOKEN-SECRET"); err == nil {
				return fmt.Errorf("the metadata fetch accepted a request that is not https")
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
		"host=idp.example.com",
		"path=/metadata",
		"outcome=failed",
		"duration_ms=",
		"cause=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the outbound record carries no %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"TOKEN-SECRET", "token=", "?"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the outbound record carries %q, which comes from the query: %s", unwanted, out)
		}
	}
}
