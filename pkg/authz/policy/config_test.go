package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/authztest"
)

// The configuration cases hold the load-timeout's default and its refusal of a
// non-positive value, and the refresh interval's default of zero and its
// refusal of a negative value. They drive the real loader over a real body, so
// the items read as an assembly reads them.

// With no configuration the load timeout is the declared default, and a
// non-positive value is refused: no load finishes in zero time, and a negative
// one names no duration at all.
func TestTheLoadTimeoutDefaultsAndRefusesNonPositive(t *testing.T) {
	reader := authztest.ConfigReader(t, Module(), "{}")
	res, err := readConfig(reader)
	if err != nil {
		t.Fatalf("readConfig with no configuration: %v", err)
	}
	if res.loadTimeout != 5*time.Second {
		t.Errorf("the load timeout without configuration is %s, want the declared default 5s", res.loadTimeout)
	}

	for _, value := range []string{"0s", "-1s"} {
		body := `{"authz":{"policy":{"load-timeout":"` + value + `"}}}`
		reader := authztest.ConfigReader(t, Module(), body)
		if _, err := readConfig(reader); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("readConfig with load-timeout %s gave %v, want ErrInvalidConfig", value, err)
		}
	}
}

// The refresh interval is configuration: with no configuration it is zero,
// which turns the periodic reload off, and a negative value names no duration
// and is refused.
func TestTheRefreshIntervalDefaultsOffAndRefusesNegative(t *testing.T) {
	reader := authztest.ConfigReader(t, Module(), "{}")
	res, err := readConfig(reader)
	if err != nil {
		t.Fatalf("readConfig with no configuration: %v", err)
	}
	if res.refreshInterval != 0 {
		t.Errorf("the refresh interval without configuration is %s, want the declared default zero (off)",
			res.refreshInterval)
	}

	reader = authztest.ConfigReader(t, Module(), `{"authz":{"policy":{"refresh-interval":"-1s"}}}`)
	if _, err := readConfig(reader); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("readConfig with refresh-interval -1s gave %v, want ErrInvalidConfig", err)
	}
}
