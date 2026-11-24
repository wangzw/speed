package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

func requireNullField(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if string(raw) != "null" {
		t.Errorf("%s = %s, want null for an absent instant", name, raw)
	}
}

func requireUTCInstant(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if !strings.HasSuffix(string(raw), `Z"`) {
		t.Errorf("%s = %s, want an RFC 3339 instant in UTC ending in Z", name, raw)
	}
}

// TestARuntimeTemplateWritesItsInstantsInUTC checks that a stored non-UTC value leaves as UTC with a trailing Z.
func TestARuntimeTemplateWritesItsInstantsInUTC(t *testing.T) {
	at := time.Date(2027, 3, 4, 5, 6, 7, 0, time.FixedZone("UTC+8", 8*60*60))
	r := newRig(t)
	r.registry.records = []sms.Record{{
		Name: "login-code", Carrier: "stub-carrier", Kind: sms.KindVerification,
		Source: sms.SourceRuntime, Enabled: true, CreatedAt: at, UpdatedAt: at,
	}}
	w := r.do(http.MethodGet, pathTemplates, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var body struct {
		Templates []map[string]json.RawMessage `json:"templates"`
	}
	decode(t, w, &body)
	if len(body.Templates) != 1 {
		t.Fatalf("templates = %+v, want one", body.Templates)
	}
	requireUTCInstant(t, body.Templates[0], "created_at")
	requireUTCInstant(t, body.Templates[0], "updated_at")
}

// TestAConfiguredTemplateWritesNullTimes checks that a configured template writes both write instants as null.
func TestAConfiguredTemplateWritesNullTimes(t *testing.T) {
	r := newRig(t)
	r.registry.records = []sms.Record{{
		Name: "login-code", Carrier: "stub-carrier", Kind: sms.KindVerification,
		Source: sms.SourceConfig, Enabled: true,
	}}
	w := r.do(http.MethodGet, pathTemplates, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var body struct {
		Templates []map[string]json.RawMessage `json:"templates"`
	}
	decode(t, w, &body)
	if len(body.Templates) != 1 {
		t.Fatalf("templates = %+v, want one", body.Templates)
	}
	requireNullField(t, body.Templates[0], "created_at")
	requireNullField(t, body.Templates[0], "updated_at")
}
