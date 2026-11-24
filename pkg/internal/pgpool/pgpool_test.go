package pgpool

import (
	"context"
	"strings"
	"testing"
)

// TestAnEmptyConnectionStringIsRefused covers the fallback this shared unit must not take.
func TestAnEmptyConnectionStringIsRefused(t *testing.T) {
	pool, err := Connect(context.Background(), "probe", Config{})
	if err == nil {
		pool.Close()
		t.Fatal("an empty connection string built a pool; the driver's fallback is the libpq " +
			"defaults, which name no database this run was pointed at")
	}
}

// The dsn item's description is what the help output prints for it, and it is the only place an
// operator is told what leaving the key out does.
func TestDSNDescriptionStatesTheStartupFails(t *testing.T) {
	description := Items()[DSNKey].Description
	if description == "" {
		t.Fatalf("Items() gives no description for %q, and the help output prints one", DSNKey)
	}
	if strings.Contains(description, "without this backend") {
		t.Errorf("the description of %q still promises that leaving it out runs the assembly "+
			"without this backend: %q", DSNKey, description)
	}
	for _, want := range []string{"fails the startup", "command tree"} {
		if !strings.Contains(description, want) {
			t.Errorf("the description of %q does not say %q: %q", DSNKey, want, description)
		}
	}
}
