package delivery

import (
	"encoding/json"
	"testing"

	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// canonicalJSON sorts object keys and normalizes numbers, so equal values always
// encode to the same text.
func TestCanonicalJSONIsStable(t *testing.T) {
	cases := map[string]struct {
		value any
		want  string
	}{
		"keys are sorted": {
			value: map[string]any{"b": 1, "a": "x"},
			want:  `{"a":"x","b":1}`,
		},
		"a whole number is its decimal text": {
			value: map[string]any{"n": 100},
			want:  `{"n":100}`,
		},
		"a float that is whole reads the same": {
			value: map[string]any{"n": float64(100)},
			want:  `{"n":100}`,
		},
		"nested objects are canonical too": {
			value: map[string]any{"outer": map[string]any{"y": true, "x": "s"}},
			want:  `{"outer":{"x":"s","y":true}}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := canonicalJSON(tc.value)
			if err != nil {
				t.Fatalf("canonicalJSON failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("canonicalJSON = %s, want %s", got, tc.want)
			}
		})
	}
}

// A value held in memory and the same value read back from a decoded payload
// encode alike, which is what makes the derived key the same on both sides of a
// job.
func TestCanonicalJSONTreatsARoundTripTheSame(t *testing.T) {
	original := map[string]any{"amount": 100, "invoice_id": "inv-1", "paid": true}
	before, err := canonicalJSON(original)
	if err != nil {
		t.Fatalf("canonicalJSON failed: %v", err)
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("encoding the round trip failed: %v", err)
	}
	var decoded any
	if decodeErr := json.Unmarshal(encoded, &decoded); decodeErr != nil {
		t.Fatalf("decoding the round trip failed: %v", decodeErr)
	}
	after, err := canonicalJSON(decoded)
	if err != nil {
		t.Fatalf("canonicalJSON failed: %v", err)
	}
	if before != after {
		t.Errorf("the round trip changed the canonical form:\n before %s\n after  %s", before, after)
	}
}

// The dispatch key is the channel key with the channel dimension dropped: it
// differs from any channel key, and it is the same for the same inputs.
func TestTheDispatchKeyDropsTheChannelDimension(t *testing.T) {
	d := dispatch("u1", "occ-1", map[string]any{"invoice_id": "inv-1", "amount": 100})
	id := tenant.ID("t1")

	dispatchKeyOne, err := dispatchKey(id, d)
	if err != nil {
		t.Fatalf("deriving the dispatch key failed: %v", err)
	}
	dispatchKeyTwo, err := dispatchKey(id, d)
	if err != nil {
		t.Fatalf("deriving the dispatch key failed: %v", err)
	}
	if dispatchKeyOne != dispatchKeyTwo {
		t.Error("the dispatch key is not stable across calls")
	}
	for _, channel := range []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS} {
		key, err := channelKey(id, d, channel)
		if err != nil {
			t.Fatalf("deriving the %s channel key failed: %v", channel, err)
		}
		if key == dispatchKeyOne {
			t.Errorf("the dispatch key equals the %s channel key", channel)
		}
	}
}

// Each channel has its own key: the same dispatch on two channels derives two
// different keys.
func TestEachChannelDerivesItsOwnKey(t *testing.T) {
	d := dispatch("u1", "occ-1", map[string]any{"invoice_id": "inv-1"})
	id := tenant.ID("t1")

	inApp, err := channelKey(id, d, notification.ChannelInApp)
	if err != nil {
		t.Fatalf("deriving the in_app key failed: %v", err)
	}
	email, err := channelKey(id, d, notification.ChannelEmail)
	if err != nil {
		t.Fatalf("deriving the email key failed: %v", err)
	}
	if inApp == email {
		t.Error("two channels derived the same key")
	}
}

// The key carries the tenant and the recipient: changing either derives a
// different key.
func TestTheKeyCarriesTheTenantAndTheRecipient(t *testing.T) {
	base := dispatch("u1", "occ-1", map[string]any{"invoice_id": "inv-1"})
	one, err := dispatchKey(tenant.ID("t1"), base)
	if err != nil {
		t.Fatalf("deriving the key failed: %v", err)
	}
	otherTenant, err := dispatchKey(tenant.ID("t2"), base)
	if err != nil {
		t.Fatalf("deriving the key failed: %v", err)
	}
	if one == otherTenant {
		t.Error("two tenants derived the same key")
	}
	otherRecipient := dispatch("u2", "occ-1", map[string]any{"invoice_id": "inv-1"})
	changed, err := dispatchKey(tenant.ID("t1"), otherRecipient)
	if err != nil {
		t.Fatalf("deriving the key failed: %v", err)
	}
	if one == changed {
		t.Error("two recipients derived the same key")
	}
}
