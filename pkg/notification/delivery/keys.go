package delivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// keyField names one field of the object the idempotency key is derived from.
// The key is the SHA-256 of a canonical encoding of exactly these fields, plus
// the channel when the key is one channel's.
const (
	fieldTenant         = "tenant"
	fieldTypeKey        = "type_key"
	fieldRecipientClass = "recipient_class"
	fieldUserID         = "user_id"
	fieldChannel        = "channel"
	fieldLocale         = "locale"
	fieldOccurrenceID   = "occurrence_id"
	fieldParams         = "params"
)

// channelKey derives the idempotency key of one channel's attempt at delivering
// one dispatch:
//
//	hex(sha256(canonicalJSON{tenant, type_key, recipient_class, user_id,
//	    channel, locale, occurrence_id, params}))
//
// The derivation is contract-frozen: the same key converges two attempts at one
// logical event into one ledger row, and the inbox derives the same value for
// the in_app channel with its own copy of the formula.
func channelKey(id tenant.ID, d notification.Dispatch, channel notification.Channel) (string, error) {
	return deriveKey(id, d, &channel)
}

// dispatchKey derives the idempotency key of the enqueued job for one dispatch:
// the channel key with the channel dimension dropped. Two dispatches of one
// logical event — same tenant, type, recipient, language, occurrence and
// parameters — therefore converge into one job, and the queue's own
// (tenant_id, idempotency_key) uniqueness is what settles them into one row.
func dispatchKey(id tenant.ID, d notification.Dispatch) (string, error) {
	return deriveKey(id, d, nil)
}

// deriveKey builds the object and hashes its canonical encoding. A nil channel
// leaves that field out.
func deriveKey(id tenant.ID, d notification.Dispatch, channel *notification.Channel) (string, error) {
	fields := map[string]any{
		fieldTenant:         string(id),
		fieldTypeKey:        d.TypeKey,
		fieldRecipientClass: string(d.Recipient.Class),
		fieldUserID:         d.Recipient.UserID,
		fieldLocale:         d.Locale,
		fieldOccurrenceID:   d.OccurrenceID,
		fieldParams:         d.Params,
	}
	if channel != nil {
		fields[fieldChannel] = string(*channel)
	}
	canonical, err := canonicalJSON(fields)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON renders a value as a stable text: object keys sorted, values
// normalized through one JSON round trip so a value held in memory and the same
// value read back from a decoded payload encode alike. Two callers that pass
// equal values therefore always get the same text, which is what makes the
// derived key the same.
func canonicalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%s: encoding a key's fields: %w", ModuleName, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var normalized any
	if err := dec.Decode(&normalized); err != nil {
		return "", fmt.Errorf("%s: reading back a key's fields: %w", ModuleName, err)
	}
	var b strings.Builder
	if err := writeCanonical(&b, normalized); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeCanonical writes one normalized JSON value. The value comes from a
// json.Decoder with UseNumber, so its dynamic type is one of nil, bool, string,
// json.Number, []any and map[string]any.
func writeCanonical(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		encoded, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("%s: encoding a key's text field: %w", ModuleName, err)
		}
		b.Write(encoded)
	case json.Number:
		b.WriteString(t.String())
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encoded, err := json.Marshal(k)
			if err != nil {
				return fmt.Errorf("%s: encoding a key's field name: %w", ModuleName, err)
			}
			b.Write(encoded)
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("%s: a value of type %T cannot be canonicalized for an idempotency key", ModuleName, v)
	}
	return nil
}
