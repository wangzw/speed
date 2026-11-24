package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/db"
)

// TestOpenRefusesABlankLocator pins Open to the rule the delivered locator is held to.
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
