package admin

import (
	"encoding/json"
	"testing"
)

// problemDoc is the Problem Details body a refusal writes, decoded so a case can
// hold the type and not only the status.
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

// object decodes a response body into its top-level object, so a case can hold
// the keys an answer carries rather than a substring of it.
func object(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", body, err)
	}
	return m
}

// decodeInto decodes a response body into target, failing the case when it is not
// the shape asked for.
func decodeInto(t *testing.T, body string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), target); err != nil {
		t.Fatalf("the body %q did not decode into %T: %v", body, target, err)
	}
}

// realmChallenge is the WWW-Authenticate value the admission and authorization
// layers write for a no-identity 401: both carriers, each naming the endpoint as
// its realm.
func realmChallenge(endpoint string) string {
	return `Bearer realm="` + endpoint + `", Cookie realm="` + endpoint + `"`
}

// keysOf returns the JSON keys of a decoded object, for a case that holds the
// exact key set of a view.
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// hasKey reports whether a JSON object body carries the top-level key name. It
// is false for a body that is not an object.
func hasKey(t *testing.T, body, name string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, ok := m[name]
	return ok
}
