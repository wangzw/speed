package postgres

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

// secrecyReader builds the real configuration loader over a JSON primary source
// holding body, with this module and the log module registered so their declared
// items are known.
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDEVENTPOSTGRESTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	reg.Register(log.Module())
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}

// TestTheConnectionStringIsRegisteredAsARedactionRule holds the credential this
// backend carries in its connection string to being masked wherever it turns up
// in a log line, through the real logging chain: the log module is driven beside
// this one writing JSON to a file, and a record that carries the connection
// string is written through the delivered capability.
//
// The assertion is that the destination shows the mask, not merely that it lacks
// the connection string: it might never have reached the record at all, and an
// absence alone would pass for the wrong reason.
func TestTheConnectionStringIsRegisteredAsARedactionRule(t *testing.T) {
	const secret = "postgres://user:event-postgres-password-5c1d@127.0.0.1:1/db"
	path := filepath.Join(t.TempDir(), "log.json")
	body := fmt.Sprintf(`{"event.postgres":{"dsn":%q},`+
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
			// server. The address the connection string names need not answer for
			// the rule to be in place, so the connection failure is set aside: what
			// this case observes is the rule the registration left behind.
			_, _ = Module().New(context.Background(), reg)
			lg, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return err
			}
			// The record carries the value as an independent attribute, which is
			// the only shape the redaction layer judges. The write happens while
			// the destination is open: the drive closes it after the action
			// returns.
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
		t.Errorf("the destination carries no %q, so the connection string was not masked:\n%s", log.MaskedText, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the connection string reached the destination in the clear:\n%s", out)
	}
}
