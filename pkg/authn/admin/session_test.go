package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

const (
	sessionOwn   = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c1a")
	sessionOther = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c1b")
	sessionThird = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c1c")
)

type sessionRowIDs struct {
	Sessions []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	} `json:"sessions"`
}

func decodeSessionRows(t *testing.T, body []byte) sessionRowIDs {
	t.Helper()
	var rows sessionRowIDs
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decoding the session list: %v", err)
	}
	return rows
}

func (r sessionRowIDs) currentRows() []string {
	var out []string
	for _, row := range r.Sessions {
		if row.Current {
			out = append(out, row.ID)
		}
	}
	return out
}

// TestOnlyTheCallersOwnSessionIsMarkedCurrent checks that only the caller's own session is marked current.
func TestOnlyTheCallersOwnSessionIsMarkedCurrent(t *testing.T) {
	t.Run("the caller's own account", func(t *testing.T) {
		r := newRig(t)
		r.sessions.put(authn.SessionInfo{ID: sessionOwn, Account: adminID, Kind: authn.SessionBrowser})
		r.sessions.put(authn.SessionInfo{ID: sessionOther, Account: adminID, Kind: authn.SessionBrowser})
		r.sessions.put(authn.SessionInfo{ID: sessionThird, Account: adminID, Kind: authn.SessionBrowser})
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(adminID)+"/sessions", nil,
			fresh(authntest.WithSession(sessionOwn))...)
		if rec.Code != http.StatusOK {
			t.Fatalf("listing the caller's own account answered %d: %s", rec.Code, rec.Body.String())
		}
		rows := decodeSessionRows(t, rec.Body.Bytes())
		if got := rows.currentRows(); len(got) != 1 || got[0] != string(sessionOwn) {
			t.Errorf("the listing marks %v current, want only the caller's session %q", got, sessionOwn)
		}
	})

	t.Run("another account", func(t *testing.T) {
		r := newRig(t)
		r.sessions.put(authn.SessionInfo{ID: sessionOther, Account: userID, Kind: authn.SessionBrowser})
		r.sessions.put(authn.SessionInfo{ID: sessionThird, Account: userID, Kind: authn.SessionBrowser})
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID)+"/sessions", nil,
			fresh(authntest.WithSession(sessionOwn))...)
		if rec.Code != http.StatusOK {
			t.Fatalf("listing another account answered %d: %s", rec.Code, rec.Body.String())
		}
		rows := decodeSessionRows(t, rec.Body.Bytes())
		if got := rows.currentRows(); len(got) != 0 {
			t.Errorf("listing another account marks %v current, want none", got)
		}
	})

	t.Run("a token-kind caller session", func(t *testing.T) {
		r := newRig(t)
		r.sessions.put(authn.SessionInfo{ID: sessionOther, Account: adminID, Kind: authn.SessionBrowser})
		r.sessions.put(authn.SessionInfo{ID: sessionOwn, Account: adminID, Kind: authn.SessionToken})
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(adminID)+"/sessions", nil,
			fresh(authntest.WithSession(sessionOwn))...)
		if rec.Code != http.StatusOK {
			t.Fatalf("listing a token-kind caller's account answered %d: %s", rec.Code, rec.Body.String())
		}
		rows := decodeSessionRows(t, rec.Body.Bytes())
		if got := rows.currentRows(); len(got) != 1 || got[0] != string(sessionOwn) {
			t.Errorf("the listing marks %v current, want only the token-kind caller's session %q", got, sessionOwn)
		}
	})
}

// TestTheSessionListDeclaresTheCurrentFlag checks that the declared operation carries the current flag as a required boolean.
func TestTheSessionListDeclaresTheCurrentFlag(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Type any `json:"type"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	schema := doc.Components.Schemas["AuthnAdminSession"]
	required := false
	for _, name := range schema.Required {
		if name == "current" {
			required = true
		}
	}
	if !required {
		t.Error("AuthnAdminSession does not declare current as a required field")
	}
	if prop, ok := schema.Properties["current"]; !ok || prop.Type != "boolean" {
		t.Errorf("AuthnAdminSession declares current as %+v, want a boolean", prop)
	}
}
