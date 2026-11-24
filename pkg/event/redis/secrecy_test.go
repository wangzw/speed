package redis

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

func secrecyReader(t *testing.T, body string) config.Reader {
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDEVENTREDISTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	reg.Register(log.Module())
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}

// TestThePasswordIsRegisteredAsARedactionRule holds that the password is
// masked wherever it appears in a log line.
func TestThePasswordIsRegisteredAsARedactionRule(t *testing.T) {
	const secret = "event-redis-password-6e9b"
	path := filepath.Join(t.TempDir(), "log.json")
	body := fmt.Sprintf(`{"event.redis":{"addr":"127.0.0.1:1","password":%q},`+
		`"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`, secret, path)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(secrecyReader(t, body), config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{log.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			// This module registers its credential in New, before it reaches the
			// server.
			_, _ = Module().New(context.Background(), reg)
			lg, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return err
			}
			// The record carries the value as an independent attribute, which is the
			// only shape the redaction layer judges.
			lg.Named("probe").Info("a request carried a credential", "value", secret)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the log module: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	out := string(written)
	if !strings.Contains(out, log.MaskedText) {
		t.Errorf("the destination carries no %q, so the password was not masked:\n%s", log.MaskedText, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the password reached the destination in the clear:\n%s", out)
	}
}
