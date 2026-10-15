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

// The credentials the case configures, none of them written by any other case,
// so a record that carries one into the destination is unmistakable.
const (
	rootKeySecret    = "db-root-key-4b7e1c9a-2f5d-4a80-9c31-77a2e6d0b4f1"
	retiredKeySecret = "db-retired-key-9c2e5a71-3b6d-4f08-a1e7-55c3b8d9f204"
)

// writingCredentialModule is a module that takes the database capability up — so
// that it is constructed after the database module and its registration has run
// — and writes one record per value through the assembly's logging capability,
// each carrying that value.
//
// It writes in New, which the drive reaches before any module's Init, so the
// records are on the destination before the case reads it.
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
				// The record carries the value as an independent attribute, which
				// is the only shape the redaction layer judges.
				lg.Named("probe").Info("a request carried a credential", "value", value)
			}
			return nil, nil
		},
	}
}

// TestTheCredentialsAreRegisteredAsRedactionRules holds the connection locator,
// the encryption key and the retired keys to being masked wherever they turn up
// in a log line, through the real logging chain: the database module is driven
// beside the log module writing JSON to a file, and a record that carries each
// value is written through the delivered capability.
//
// The assertion is that the destination shows the mask for each value, not
// merely that it lacks it: a value might never have reached the record at all,
// and an absence alone would pass for the wrong reason.
func TestTheCredentialsAreRegisteredAsRedactionRules(t *testing.T) {
	dir := t.TempDir()
	// The locator of the sqlite handle this case opens: it is the connection
	// locator the section carries, and the case writes it into a record of its
	// own so that the registration covering it is observed rather than assumed.
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
