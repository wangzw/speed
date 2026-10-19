package selfservice_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestABodyOverTheCapabilityLimitIsRefusedBeforeTheHandler holds the 64 KiB cap
// on a write route: a valid body padded past the limit is a 400 bad-request and
// the write is never asked for.
func TestABodyOverTheCapabilityLimitIsRefusedBeforeTheHandler(t *testing.T) {
	h := newHarness(t)
	padded := `{"kind":"username","value":"newname"}` + strings.Repeat(" ", 70_000)
	w := h.post("/identifiers", padded)
	if w.Code != http.StatusBadRequest || problemType(t, w) != "bad-request" {
		t.Fatalf("answer = %d %s, want 400 bad-request", w.Code, w.Body)
	}
	if h.log.count("admin.AddIdentifier") != 0 {
		t.Error("an oversized body reached the write")
	}
}

// TestTheImpersonationCheckComesBeforeTheBody holds the order a body precheck
// restores: an impersonating identity is refused 403 before the body is read, so
// a malformed body is not answered as the request's own fault.
func TestTheImpersonationCheckComesBeforeTheBody(t *testing.T) {
	h := newHarness(t, func(c *config) { c.impersonable = true })
	h.impersonating()
	w := h.put("/password", `{"new_password":`)
	if w.Code != http.StatusForbidden || problemType(t, w) != "impersonation-restricted" {
		t.Fatalf("answer = %d %s, want 403 impersonation-restricted", w.Code, w.Body)
	}
	if h.log.count("passwords.Set") != 0 {
		t.Error("an impersonating request reached the password write")
	}
}
