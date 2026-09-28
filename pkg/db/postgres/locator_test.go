package postgres_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestOpenRefusesABlankLocatorWhateverTheEnvironmentSays pins Open to the
// rule the delivered locator is held to. Handed an empty string, this engine's
// driver does not fail: it assembles a locator out of the PG* variables of the
// process environment, the local socket and the user the process runs as, and
// connects to whatever that names — a caller whose replica locator was left
// empty gets a handle on some other database with no error. Here the
// environment names a database that answers, and Open still refuses.
func TestOpenRefusesABlankLocatorWhateverTheEnvironmentSays(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssembly(t, dsn)
	if err != nil {
		t.Fatalf("starting a host on the fixture: %v", err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("reading the fixture's locator: %v", err)
	}
	password, _ := parsed.User.Password()
	t.Setenv("PGHOST", parsed.Hostname())
	t.Setenv("PGPORT", parsed.Port())
	t.Setenv("PGUSER", parsed.User.Username())
	t.Setenv("PGPASSWORD", password)
	t.Setenv("PGDATABASE", strings.TrimPrefix(parsed.Path, "/"))
	t.Setenv("PGSSLMODE", "disable")

	for _, blank := range []string{"", "   "} {
		handle, err := capability.Open(t.Context(), blank)
		if err == nil {
			_ = db.Close(handle)
			t.Errorf("Open(%q) connected to the database the environment names, so a locator left empty "+
				"reaches a database nobody passed", blank)
			continue
		}
		if !errors.Is(err, db.ErrConnectFailed) {
			t.Errorf("Open(%q) gave %v, want ErrConnectFailed", blank, err)
		}
	}
}
