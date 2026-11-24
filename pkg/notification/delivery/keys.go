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

func channelKey(id tenant.ID, d notification.Dispatch, channel notification.Channel) (string, error) {
	return deriveKey(id, d, &channel)
}

func dispatchKey(id tenant.ID, d notification.Dispatch) (string, error) {
	return deriveKey(id, d, nil)
}

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
