package sms

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyUsesTheTableAndDefaultsToUncertain holds the two facts of the
// exact-code helper: a code the table names becomes its sentinel, and any other
// code becomes the uncertain sentinel, with the code carried into the text.
func TestClassifyUsesTheTableAndDefaultsToUncertain(t *testing.T) {
	table := map[string]error{
		"Rejected":   ErrRejected,
		"Throttled":  ErrThrottled,
		"Unroutable": ErrUnroutable,
	}

	for code, want := range map[string]error{
		"Rejected":       ErrRejected,
		"Throttled":      ErrThrottled,
		"Unroutable":     ErrUnroutable,
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

// TestTheUncertainShapesWrapTheSentinel holds that the two ready-made shapes are
// both uncertain and both name the carrier.
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
