package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/kv"
)

// TestStanceFailsTheStartupWithoutAConnectionString pins the rule that a module
// named in the command tree has to start: this one reaches PostgreSQL at a
// locator, so being named without one stops the startup and names the input it
// stopped on. A stand-down in its place would bring the process up with the
// capability silently missing.
func TestStanceFailsTheStartupWithoutAConnectionString(t *testing.T) {
	_, err := stance(&pgpool.Config{})
	if err == nil {
		t.Fatal("naming this implementation without a connection string started anyway")
	}
	if want := ConfigNamespace + "." + pgpool.DSNKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

func TestStanceEnablesOnceAConnectionStringIsGiven(t *testing.T) {
	got, err := stance(&pgpool.Config{DSN: "postgres://localhost/x"})
	if err != nil {
		t.Fatalf("stance with a locator: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a locator = %v, want StateEnabled", got.State)
	}
}

// TestOnlyTheServersNonNumericWordingBecomesTheSentinel covers the one mapping
// this backend has to make, and the narrowness that keeps a connection failure
// from being read as a bad value.
func TestOnlyTheServersNonNumericComplaintsBecomeTheSentinel(t *testing.T) {
	if got := asNotNumeric(nil, "k"); got != nil {
		t.Fatalf("asNotNumeric(nil) = %v, want nil", got)
	}
	conn := errors.New("failed to connect to `host=127.0.0.1`: connection refused")
	if got := asNotNumeric(conn, "k"); !errors.Is(got, conn) {
		t.Fatalf("a connection failure was rewritten as %v", got)
	}
	other := &pgconn.PgError{Code: "42P01", Message: `relation "kv_entries" does not exist`}
	if got := asNotNumeric(other, "k"); !errors.Is(got, other) {
		t.Fatalf("an unrelated server error was rewritten as %v", got)
	}
	for _, complaint := range []*pgconn.PgError{
		{Code: invalidTextRepresentation, Message: `invalid input syntax for type numeric: "not a number"`},
		{Code: characterNotInRepertoire, Message: `invalid byte sequence for encoding "UTF8": 0xff`},
	} {
		got := asNotNumeric(complaint, "k")
		if !errors.Is(got, kv.ErrNotNumeric) {
			t.Fatalf("the server's complaint %q was not mapped onto the sentinel: %v", complaint.Message, got)
		}
		if strings.Contains(got.Error(), "not a number") || strings.Contains(got.Error(), "0xff") {
			t.Fatalf("the sentinel carries the server's quotation of the stored value: %v", got)
		}
	}
}
