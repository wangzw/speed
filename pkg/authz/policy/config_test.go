package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/authztest"
)

// The configuration cases hold the load-timeout's default and its refusal of a
// non-positive value, and the refresh interval's range. They drive the real
// loader over a real body, so the item reads as an assembly reads it.

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

// The refresh interval is a duration: zero is legal (it is the "off" value the
// design gives it) and a negative one is not.
func TestTheRefreshIntervalDomain(t *testing.T) {
	if !refreshInDomain(0) {
		t.Errorf("a zero refresh interval was refused, want it legal: zero is the off value")
	}
	if !refreshInDomain(time.Minute) {
		t.Errorf("a positive refresh interval was refused")
	}
	if refreshInDomain(-time.Second) {
		t.Errorf("a negative refresh interval was accepted, want it refused")
	}
	if settingsDefaults.RefreshInterval != 0 {
		t.Errorf("the refresh interval's declared default is %s, want zero (off)", settingsDefaults.RefreshInterval)
	}
}
