package event_test

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/event"
)

type deal struct {
	ID string `json:"id"`
}

// TestDecodePayloadReportsAMissingPayload pins the typed path's answer to
// an event that carries nothing.
func TestDecodePayloadReportsAMissingPayload(t *testing.T) {
	for name, payload := range map[string]any{
		"nil":         nil,
		"nil pointer": (*deal)(nil),
		"nil map":     map[string]any(nil),
		"nil slice":   []string(nil),
	} {
		var d deal
		if err := event.DecodePayload(payload, &d); !errors.Is(err, event.ErrNoPayload) {
			t.Errorf("decoding a %s payload: %v, want ErrNoPayload", name, err)
		}
	}
}

// TestDecodePayloadStillDecodesAnEmptyValue keeps the refusal to what carries
// nothing: an empty object or a zero value is a payload the publisher sent.
func TestDecodePayloadStillDecodesAnEmptyValue(t *testing.T) {
	for name, payload := range map[string]any{
		"empty object": map[string]any{},
		"zero struct":  deal{},
	} {
		var d deal
		if err := event.DecodePayload(payload, &d); err != nil {
			t.Errorf("decoding an %s: %v", name, err)
		}
	}
}
