package mail

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyUsesTheTableAndDefaultsToUncertain checks that a table code becomes
// its sentinel and any other code becomes ErrUncertain, with the code in the text.
func TestClassifyUsesTheTableAndDefaultsToUncertain(t *testing.T) {
	table := map[string]error{"Rejected": ErrRejected, "Throttled": ErrThrottled}

	for code, want := range map[string]error{
		"Rejected":       ErrRejected,
		"Throttled":      ErrThrottled,
		"SomethingNoOne": ErrUncertain,
		"":               ErrUncertain,
	} {
		err := Classify(table, "carrier", code)
		if !errors.Is(err, want) {
			t.Errorf("Classify(%q) = %v, want %v", code, err, want)
		}
		if code != "" && !strings.Contains(err.Error(), code) {
			t.Errorf("Classify(%q) = %q, does not name the code", code, err.Error())
		}
	}
}

// TestTheUncertainShapesWrapTheSentinel checks that Unsettled and NoStatus both
// wrap ErrUncertain and name the carrier.
func TestTheUncertainShapesWrapTheSentinel(t *testing.T) {
	for _, err := range []error{Unsettled("carrier"), NoStatus("carrier")} {
		if !errors.Is(err, ErrUncertain) {
			t.Errorf("%v does not wrap ErrUncertain", err)
		}
		if !strings.Contains(err.Error(), "carrier") {
			t.Errorf("%q does not name the carrier", err.Error())
		}
	}
}
