package otlp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// otlpReader builds the real configuration loader over a JSON primary source
// holding body, with this module and the log module registered so their declared
// items are known.
func otlpReader(t *testing.T, body string) config.Reader {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDOTLPTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	reg.Register(log.Module())
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}

// TestTheHeaderValuesAreRegisteredAsRedactionRules holds the values a header
// carries to being masked wherever they turn up in a log line, through the real
// logging chain: this module is driven beside the log module writing JSON to a
// file, and a record that carries a header value is written through the
// delivered capability.
//
// The assertion is that the destination shows the mask, not merely that it lacks
// the value: the value might never have reached the record at all, and an
// absence alone would pass for the wrong reason.
func TestTheHeaderValuesAreRegisteredAsRedactionRules(t *testing.T) {
	withoutOverriddenVariables(t)
	const secret = "collector-credential-7c4e1a9b"
	path := filepath.Join(t.TempDir(), "log.json")
	body := fmt.Sprintf(`{"observability":{"otlp":{"endpoint":"collector.example:4317",`+
		`"headers":["x-api-key=%s"]}},`+
		`"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`, secret, path)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(otlpReader(t, body), config.ModuleName)
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, log.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			lg, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return err
			}
			// Written while the destination is open: the drive closes it after
			// the action returns.
			lg.Named("probe").Info("a request carried a header value", "value", secret)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	out := string(written)
	if !strings.Contains(out, log.MaskedText) {
		t.Errorf("the destination carries no %q, so the header value was not masked:\n%s", log.MaskedText, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the header value reached the destination in the clear:\n%s", out)
	}
}
