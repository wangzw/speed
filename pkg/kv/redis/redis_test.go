package redis

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/kv"
)

// TestStanceFailsTheStartupWithoutAServer checks that naming the implementation with no address
// fails, naming the addr key.
func TestStanceFailsTheStartupWithoutAServer(t *testing.T) {
	cfg := speedredis.Defaults()
	_, err := stance(&cfg)
	if err == nil {
		t.Fatal("naming this implementation without a server started anyway")
	}
	if want := ConfigNamespace + ".addr"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

// TestStanceEnablesOnceAServerIsNamed checks that an address makes the stance StateEnabled.
func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	cfg := speedredis.Defaults()
	cfg.Addr = "127.0.0.1:6379"
	got, err := stance(&cfg)
	if err != nil {
		t.Fatalf("stance with an address: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with an address = %v, want StateEnabled: two configured backends have to "+
			"collide at startup rather than one winning silently", got.State)
	}
}

// TestOnlyTheServersNonNumericWordingBecomesTheSentinel covers the one mapping this backend has to
// make.
func TestOnlyTheServersNonNumericWordingBecomesTheSentinel(t *testing.T) {
	if got := asNotNumeric(nil); got != nil {
		t.Fatalf("asNotNumeric(nil) = %v, want nil", got)
	}
	conn := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	if got := asNotNumeric(conn); !errors.Is(got, conn) {
		t.Fatalf("a connection failure was rewritten as %v", got)
	}
	other := errors.New("ERR unknown command")
	if got := asNotNumeric(other); !errors.Is(got, other) {
		t.Fatalf("an unrelated server error was rewritten as %v", got)
	}
	nonNumeric := errors.New("ERR value is not a valid float")
	if got := asNotNumeric(nonNumeric); !errors.Is(got, kv.ErrNotNumeric) {
		t.Fatalf("the server's non-numeric complaint was not mapped onto the sentinel: %v", got)
	}
}
