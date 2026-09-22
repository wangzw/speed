package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt separates the two ways a
// module whose New did not run leaves the instance nil. One module has no New
// callback at all; the other has one and it returned no product. Both leave
// assertDelivered with a nil instance, and the fixing actions differ — one
// module has to gain a callback, the other has to correct what its callback
// returns — so a message that reports a missing callback for both sends half
// its readers after a callback that is not absent.
func TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt(t *testing.T) {
	for _, tc := range []struct {
		why    string
		module core.Module
		want   []string
		// absent is a fragment the message must not carry, because it names
		// the other way of arriving here.
		absent string
	}{
		{
			why: "a New callback that returned no product",
			module: core.Module{
				Name:     "hollow",
				Provides: []core.Provision{{Token: (*Reader)(nil)}},
				New:      func(context.Context, *core.Registry) (any, error) { return nil, nil },
			},
			want:   []string{"hollow", "Reader", "returned no product"},
			absent: "has no New callback",
		},
		{
			why: "a module with no New callback",
			module: core.Module{
				Name:     "resources-only",
				Provides: []core.Provision{{Token: (*Reader)(nil)}},
			},
			want:   []string{"resources-only", "Reader", "has no New callback"},
			absent: "returned no product",
		},
	} {
		reg := core.New()
		register(reg, tc.module)

		err := runToFailure(t, reg)
		if !errors.Is(err, core.ErrUndeliveredCapability) {
			t.Fatalf("%s: Run returned %v, want ErrUndeliveredCapability", tc.why, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not mention %q", tc.why, err, want)
			}
		}
		if strings.Contains(err.Error(), tc.absent) {
			t.Errorf("%s: error %q describes the other way of arriving here with %q",
				tc.why, err, tc.absent)
		}
	}
}
