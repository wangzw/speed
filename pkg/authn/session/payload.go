package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/vislake/speed/pkg/authn"
)

// maxDataBytes is the most the data column of a row may hold, in bytes. The
// table refuses a longer text as well, and a payload is refused here first so
// that the failure is an invalid argument and not a statement the database
// rejected.
const maxDataBytes = 8192

// maxTokenLen is the longest cookie value that is looked at. A token this module
// issues is 43 characters, so a longer value is no session's.
const maxTokenLen = 256

// payloadKey is the one key a payload is kept under in the library's session
// values.
const payloadKey = "p"

// maxMethods is the most authentication methods a session records.
const maxMethods = 8

// errCorrupt marks a stored payload that does not decode, or that disagrees with
// the row it sits in. Such a row never authenticates anybody.
var errCorrupt = errors.New("session: the stored payload is not one this module wrote")

// payload is what the data column holds, in the JSON the codec writes: the facts
// a verifying backend builds an identity from that are not columns of their own.
// The session identifier, the account and the kind repeat the row's, which is
// how a payload moved onto another row is told apart.
type payload struct {
	SID        string            `json:"sid"`
	Account    string            `json:"account"`
	Kind       string            `json:"kind"`
	Methods    []authn.Method    `json:"methods"`
	Assurance  authn.Assurance   `json:"assurance"`
	IDPAssured bool              `json:"idp_assured,omitempty"`
	AuthTime   time.Time         `json:"auth_time"`
	Provider   string            `json:"provider,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Expires    time.Time         `json:"exp,omitzero"`
	Idle       time.Time         `json:"idle,omitzero"`
}

// jsonCodec is the library's codec, replaced so that the stored form is JSON and
// not gob: no type has to be registered, and the decoder has a smaller surface
// to be fed a stored value.
type jsonCodec struct{}

var _ scs.Codec = jsonCodec{}

// envelope is the document the codec writes.
type envelope struct {
	Deadline time.Time                  `json:"deadline"`
	Values   map[string]json.RawMessage `json:"values"`
}

// Encode writes the deadline and the values as a JSON document. The only value
// this module puts is the payload.
func (jsonCodec) Encode(deadline time.Time, values map[string]any) ([]byte, error) {
	env := struct {
		Deadline time.Time      `json:"deadline"`
		Values   map[string]any `json:"values"`
	}{Deadline: deadline.UTC(), Values: values}
	return json.Marshal(env)
}

// Decode reads the document back. The payload is returned as a payload; a value
// under any other key is kept as the raw JSON it was.
func (jsonCodec) Decode(b []byte) (time.Time, map[string]any, error) {
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return time.Time{}, nil, errCorrupt
	}
	values := make(map[string]any, len(env.Values))
	for k, raw := range env.Values {
		if k != payloadKey {
			values[k] = raw
			continue
		}
		var p payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return time.Time{}, nil, errCorrupt
		}
		values[k] = p
	}
	return env.Deadline, values, nil
}

// encodePayload writes p as the document the data column holds.
func encodePayload(p payload) ([]byte, error) {
	return jsonCodec{}.Encode(p.Expires, map[string]any{payloadKey: p})
}

// decodePayload reads the document of a data column and returns the payload in
// it. A document without one is ok false.
func decodePayload(b []byte) (payload, bool, error) {
	_, values, err := jsonCodec{}.Decode(b)
	if err != nil {
		return payload{}, false, err
	}
	p, ok := values[payloadKey].(payload)
	return p, ok, nil
}

// storeKey is the key a token is kept under in the store: the SHA-256 of the
// token in the encoding the token itself has. It is the library's own function
// of a token, which the library hands the store when HashTokenInStore is set,
// and this module computes it for the rotation, which does not go through the
// library; a test holds the two equal.
func storeKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newToken makes a token in the form the library makes them: 32 random bytes in
// unpadded URL-safe base64.
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: drawing a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// checkMethods holds a method set to the shape a principal's is held to:
// non-empty, every entry in the closed set, no repeats, at most maxMethods.
func checkMethods(methods []authn.Method) error {
	if len(methods) == 0 || len(methods) > maxMethods {
		return fmt.Errorf("%w: the method set is empty or too large", authn.ErrInvalidArgument)
	}
	seen := make(map[authn.Method]struct{}, len(methods))
	for _, m := range methods {
		if !m.Valid() {
			return fmt.Errorf("%w: a method is outside the closed set", authn.ErrInvalidArgument)
		}
		if _, twice := seen[m]; twice {
			return fmt.Errorf("%w: a method is named twice", authn.ErrInvalidArgument)
		}
		seen[m] = struct{}{}
	}
	return nil
}

// checkAssurance refuses a level that is not stated: a session records the level
// its authentication reached.
func checkAssurance(a authn.Assurance) error {
	if a != authn.AssuranceSingle && a != authn.AssuranceMulti {
		return fmt.Errorf("%w: the assurance level is not one a session records", authn.ErrInvalidArgument)
	}
	return nil
}

// checkProvider holds the provider to the shape the gateway admits: empty, or
// UUID text in its canonical form.
func checkProvider(s string) error {
	if s == "" {
		return nil
	}
	if len(s) != 36 {
		return fmt.Errorf("%w: the provider is not UUID text", authn.ErrInvalidArgument)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return fmt.Errorf("%w: the provider is not UUID text", authn.ErrInvalidArgument)
			}
		default:
			if !lowerHexDigit(c) {
				return fmt.Errorf("%w: the provider is not UUID text", authn.ErrInvalidArgument)
			}
		}
	}
	return nil
}

// lowerHexDigit reports whether c is one of the lower-case hexadecimal digits a
// canonical UUID is written with.
func lowerHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
