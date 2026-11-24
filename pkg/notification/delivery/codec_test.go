package delivery

// The queue codec protects a time parameter's type across the job
// payload: a {{name, time, layout}} segment in an email catalog needs a
// time.Time, and encoding/json would otherwise write it as a bare string.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/notification"
)

func codecRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, func(h *host) {
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelInApp},
			Unsubscribable:  true,
		}}
	})
}

// TestValidateRefusesAParameterTheQueueCodecCannotCarry holds the
// reserved shapes.
func TestValidateRefusesAParameterTheQueueCodecCannotCarry(t *testing.T) {
	instant := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	instantPtr := &instant
	cases := map[string]any{
		"pointer to an instant":         instantPtr,
		"slice of instants":             []time.Time{instant},
		"map of instants":               map[string]time.Time{"at": instant},
		"slice of instant pointers":     []*time.Time{instantPtr},
		"map of instant pointers":       map[string]*time.Time{"at": instantPtr},
		"typed slice of strings":        []string{"a"},
		"typed map of strings":          map[string]string{"a": "b"},
		"slice of instant slices":       [][]time.Time{{instant}},
		"map of instant slices":         map[string][]time.Time{"at": {instant}},
		"a struct the codec cannot see": struct{ At time.Time }{At: instant},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			r := codecRig(t)
			d := dispatch("u1", "occ-1", map[string]any{"value": value})
			if _, err := r.p.validate(d); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("validate ended with %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// TestValidateRefusesTheReservedTimeMarkerKeyAtAnyDepth holds that a
// producer cannot hand the codec a map carrying the reserved key.
func TestValidateRefusesTheReservedTimeMarkerKeyAtAnyDepth(t *testing.T) {
	marked := instantText(time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC))
	cases := map[string]map[string]any{
		"top-level parameter map": {deliveryTimeMarker: marked},
		"nested map":              {"value": map[string]any{deliveryTimeMarker: marked}},
		"nested map beside a key": {"value": map[string]any{deliveryTimeMarker: marked, "other": 1}},
		"inside a slice":          {"value": []any{map[string]any{deliveryTimeMarker: marked}}},
		"deeper still":            {"value": []any{map[string]any{"inner": map[string]any{deliveryTimeMarker: marked}}}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			r := codecRig(t)
			if _, err := r.p.validate(dispatch("u1", "occ-1", params)); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("validate ended with %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// TestAReservedKeyIsReportedAsTheUnreadableShape holds the sentinel even
// for a type whose whitelist does not list the reserved name.
func TestAReservedKeyIsReportedAsTheUnreadableShape(t *testing.T) {
	r := newRig(t, nil) // the default declaration carries a whitelist
	params := map[string]any{
		deliveryTimeMarker: "2026-11-20T09:30:00Z",
		"invoice_id":       "inv-1",
	}
	_, err := r.p.validate(dispatch("u1", "occ-1", params))
	if !errors.Is(err, notification.ErrInvalidRequest) {
		t.Fatalf("validate ended with %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, notification.ErrParamsNotAllowed) {
		t.Errorf("validate answered the whitelist sentinel, want the codec's reserved key to be the refusal")
	}
}

// TestTheQueueCodecRoundTripsTimeParametersAtAnyDepth holds the allowed
// shapes.
func TestTheQueueCodecRoundTripsTimeParametersAtAnyDepth(t *testing.T) {
	instant := time.Date(2026, 11, 20, 9, 30, 0, 123456789, time.UTC)
	d := dispatch("u1", "occ-1", map[string]any{
		"top": instant,
		"nested": map[string]any{
			"at": instant,
		},
		"list": []any{
			instant,
			map[string]any{"at": instant},
		},
	})
	payload, err := json.Marshal(deliveryPayload{Dispatch: d})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}
	back, _, err := decodeDispatch(payload)
	if err != nil {
		t.Fatalf("decoding the payload: %v", err)
	}

	assertInstant := func(name string, value any) {
		t.Helper()
		at, ok := value.(time.Time)
		if !ok {
			t.Fatalf("%s decoded as %T, want a time.Time", name, value)
		}
		if !at.Equal(instant) {
			t.Errorf("%s decoded as %s, want %s", name, at, instant)
		}
	}
	assertInstant("top", back.Params["top"])
	nested, ok := back.Params["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested decoded as %T, want map[string]any", back.Params["nested"])
	}
	assertInstant("nested.at", nested["at"])
	list, ok := back.Params["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("list decoded as %#v, want two items", back.Params["list"])
	}
	assertInstant("list[0]", list[0])
	item, ok := list[1].(map[string]any)
	if !ok {
		t.Fatalf("list[1] decoded as %T, want map[string]any", list[1])
	}
	assertInstant("list[1].at", item["at"])
}

// TestAReservedObjectWithMoreThanOneKeyStaysAMapAndIsRefused holds the
// codec's own report.
func TestAReservedObjectWithMoreThanOneKeyStaysAMapAndIsRefused(t *testing.T) {
	r := codecRig(t)
	d := dispatch("u1", "occ-1", map[string]any{
		"value": map[string]any{deliveryTimeMarker: "2026-11-20T09:30:00Z", "other": 1},
	})
	payload, err := json.Marshal(deliveryPayload{Dispatch: d})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}
	back, _, err := decodeDispatch(payload)
	if err != nil {
		t.Fatalf("decoding the payload: %v", err)
	}
	if _, isMap := back.Params["value"].(map[string]any); !isMap {
		t.Fatalf("the multi-key reserved object decoded as %T, want a map left untouched", back.Params["value"])
	}
	if _, err := r.p.validate(back); !errors.Is(err, notification.ErrInvalidRequest) {
		t.Fatalf("validate ended with %v, want ErrInvalidRequest", err)
	}
}

func instantText(at time.Time) string {
	return at.Format(time.RFC3339Nano)
}
