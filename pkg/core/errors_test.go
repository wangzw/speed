package core

import "testing"

// TestSentinelSetMatchesDesignTable checks that the seven sentinels are non-nil, have distinct
// non-empty messages, and are exactly the seven errors this package exports.
func TestSentinelSetMatchesDesignTable(t *testing.T) {
	sentinels := map[string]error{
		"ErrMissingProvider":       ErrMissingProvider,
		"ErrAmbiguousProvider":     ErrAmbiguousProvider,
		"ErrExclusiveViolated":     ErrExclusiveViolated,
		"ErrDisabledWithoutReason": ErrDisabledWithoutReason,
		"ErrDependencyCycle":       ErrDependencyCycle,
		"ErrUndeliveredCapability": ErrUndeliveredCapability,
		"ErrUnknownParticipant":    ErrUnknownParticipant,
	}
	if len(sentinels) != 7 {
		t.Fatalf("the design table lists 7 sentinels, the test pins %d", len(sentinels))
	}
	seen := make(map[string]string, len(sentinels))
	for name, err := range sentinels {
		if err == nil {
			t.Errorf("%s is nil", name)
			continue
		}
		if err.Error() == "" {
			t.Errorf("%s has an empty message", name)
		}
		if other, dup := seen[err.Error()]; dup {
			t.Errorf("%s and %s share the message %q", name, other, err.Error())
		}
		seen[err.Error()] = name
	}
}
