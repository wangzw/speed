package db_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
)

// The credentials the case configures, none of them written by any other case, so a
// record that carries one into the destination is unmistakable.
const (
	rootKeySecret    = "db-root-key-4b7e1c9a-2f5d-4a80-9c31-77a2e6d0b4f1"
	retiredKeySecret = "db-retired-key-9c2e5a71-3b6d-4f08-a1e7-55c3b8d9f204"
)

func writingCredentialModule(values ...string) core.Module {
	return core.Module{
		Name: "case.credential-writer",
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			lg, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, nil
			}
			for _, value := range values {
				lg.Named("probe").Info("a request carried a credential", "value", value)
			}
			return nil, nil
		},
	}
}

// TestTheCredentialsAreRegisteredAsRedactionRules holds the connection locator, the encryption key
// and the retired keys to being masked wherever they turn up in a log line.
func TestTheCredentialsAreRegisteredAsRedactionRules(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "host.db")
	path := filepath.Join(dir, "log.json")
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q,"encryption-key":%q,"encryption-retired-keys":[%q]}},`+
		`"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`,
		dsn, rootKeySecret, retiredKeySecret, path)

	h, err := startHost(t, sqliteSpec(), body, log.Module(),
		writingCredentialModule(dsn, rootKeySecret, retiredKeySecret))
	if err != nil {
		t.Fatalf("starting a host over the log module: %v", err)
	}
	defer h.finish(t)

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	out := string(written)
	for _, secret := range []string{dsn, rootKeySecret, retiredKeySecret} {
		if strings.Contains(out, secret) {
			t.Errorf("the credential %q reached the destination in the clear:\n%s", secret, out)
		}
	}
	if got := strings.Count(out, log.MaskedText); got < 3 {
		t.Errorf("the destination carries %q %d times, want it once for each of the three credentials:\n%s",
			log.MaskedText, got, out)
	}
}
