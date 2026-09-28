package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/db"
)

// TestOpenRefusesABlankLocator pins Open to the rule the delivered locator is
// held to. This engine's driver reads an empty locator as a temporary
// database private to each connection, so over a pool a table created on one
// connection is missing on the others; a caller that means an in-memory
// database writes :memory:, and a blank one is refused.
func TestOpenRefusesABlankLocator(t *testing.T) {
	instance := deliver(t, filepath.Join(t.TempDir(), "delivered.db"))
	for _, blank := range []string{"", "  "} {
		handle, err := instance.Open(t.Context(), blank)
		if err == nil {
			_ = db.Close(handle)
			t.Errorf("Open(%q) returned a handle", blank)
			continue
		}
		if !errors.Is(err, db.ErrConnectFailed) {
			t.Errorf("Open(%q) gave %v, want ErrConnectFailed", blank, err)
		}
	}
}
