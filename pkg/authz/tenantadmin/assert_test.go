package tenantadmin

import (
	"encoding/json"
	"testing"
)

// problemDoc is the Problem Details body a refusal writes.
type problemDoc struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// decodeProblem decodes a problem body, failing the case when it is not one.
func decodeProblem(t *testing.T, body string) problemDoc {
	t.Helper()
	var p problemDoc
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("the body %q is not a problem object: %v", body, err)
	}
	return p
}

// object decodes a response body into its top-level object.
func object(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", body, err)
	}
	return m
}

// decodeInto decodes a response body into target.
func decodeInto(t *testing.T, body string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), target); err != nil {
		t.Fatalf("the body %q did not decode into %T: %v", body, target, err)
	}
}

// realmChallenge is the WWW-Authenticate value the admission and authorization
// layers write for a no-identity 401.
func realmChallenge(endpoint string) string {
	return `Bearer realm="` + endpoint + `", Cookie realm="` + endpoint + `"`
}

// keysOf returns the JSON keys of a decoded object.
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// hasKey reports whether a JSON object body carries the top-level key name.
func hasKey(t *testing.T, body, name string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, ok := m[name]
	return ok
}
