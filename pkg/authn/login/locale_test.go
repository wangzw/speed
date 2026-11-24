package login

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLocaleOfPassesTheRequestLanguageThrough holds that localeOf takes the settled context locale, or the first Accept-Language tag as written.
func TestLocaleOfPassesTheRequestLanguageThrough(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"en-US,en;q=0.9", "en-US"},
		{"en", "en"},
		{"zh-Hans-CN", "zh-Hans-CN"},
		{"ja-JP", "ja-JP"},
		{"fr-FR", "fr-FR"},
		{"de,en;q=0.5", "de"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Accept-Language", tc.header)
			}
			c := &call{ctx: req.Context(), req: req}
			if got := localeOf(c); got != tc.want {
				t.Errorf("localeOf(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestTheLanguageReachesTheFlowRecord holds that the language read at start is the one the flow record keeps for a later SMS send.
func TestTheLanguageReachesTheFlowRecord(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`, "Accept-Language", "ja-JP,en;q=0.9")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := flowCookie(t, rec)
	if got := flowRecordOf(t, r, cookie).Locale; got != "ja-JP" {
		t.Errorf("the flow record locale is %q, want %q", got, "ja-JP")
	}
}
