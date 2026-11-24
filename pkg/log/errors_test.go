package log

import (
	"errors"
	"fmt"
	"testing"
)

var sentinels = map[string]error{
	"ErrInvalidLevel":       ErrInvalidLevel,
	"ErrUnknownDestination": ErrUnknownDestination,
	"ErrUnknownFormat":      ErrUnknownFormat,
	"ErrMissingPath":        ErrMissingPath,
	"ErrInvalidFileParam":   ErrInvalidFileParam,
	"ErrConflictingOutput":  ErrConflictingOutput,
	"ErrOutputUnavailable":  ErrOutputUnavailable,
}

// TestSentinelsAreDistinct checks that the seven sentinels are non-nil, have messages, and a
// wrapped one matches only itself.
func TestSentinelsAreDistinct(t *testing.T) {
	if len(sentinels) != 7 {
		t.Fatalf("the design table lists 7 sentinels, the test pins %d", len(sentinels))
	}
	for name, err := range sentinels {
		if err == nil {
			t.Fatalf("%s is nil", name)
		}
		if err.Error() == "" {
			t.Errorf("%s has an empty message", name)
		}
		wrapped := fmt.Errorf("assembling output 0: %w", err)
		for otherName, other := range sentinels {
			match := errors.Is(wrapped, other)
			if otherName == name && !match {
				t.Errorf("a wrapped %s does not match %s", name, name)
			}
			if otherName != name && match {
				t.Errorf("a wrapped %s also matches %s", name, otherName)
			}
		}
	}
}
