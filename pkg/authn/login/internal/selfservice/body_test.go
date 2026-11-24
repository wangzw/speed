package selfservice_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestABodyOverTheCapabilityLimitIsRefusedBeforeTheHandler holds that a body padded past 64 KiB answers 400 and the write is never asked.
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

// TestTheFreshnessCheckComesBeforeTheBody holds that an unmet freshness is a 401 before the body is read.
func TestTheFreshnessCheckComesBeforeTheBody(t *testing.T) {
	h := newHarness(t, func(c *config) { c.impersonable = true })
	h.impersonating()
	h.as(append(single(time.Hour), authntest.WithActor("admin-1"), authntest.WithScope("tenant-a"))...)
	w := h.put("/password", `{"new_password":`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("answer = %d %s, want the step-up 401", w.Code, w.Body)
	}
	if problemType(t, w) != "insufficient_user_authentication" {
		t.Errorf("answer = %s, want insufficient_user_authentication", w.Body)
	}
	if h.log.count("passwords.Set") != 0 {
		t.Error("a stale request reached the password write")
	}
}
