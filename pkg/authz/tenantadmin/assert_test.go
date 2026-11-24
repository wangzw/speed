package tenantadmin

import (
	"encoding/json"
	"testing"
)

type problemDoc struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func decodeProblem(t *testing.T, body string) problemDoc {
	t.Helper()
	var p problemDoc
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("the body %q is not a problem object: %v", body, err)
	}
	return p
}

func object(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", body, err)
	}
	return m
}

func decodeInto(t *testing.T, body string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), target); err != nil {
		t.Fatalf("the body %q did not decode into %T: %v", body, target, err)
	}
}

func realmChallenge(endpoint string) string {
	return `Bearer realm="` + endpoint + `", Cookie realm="` + endpoint + `"`
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasKey(t *testing.T, body, name string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, ok := m[name]
	return ok
}
