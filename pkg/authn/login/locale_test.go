package login

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLocaleOfPassesTheRequestLanguageThrough holds the engine's mapping of the
// request's language: the locale the i18n layer settled into the context, or,
// with no i18n layer, the first Accept-Language tag as written. It is not held to
// a set — the SMS module resolves it against the languages its carrier declares —
// so a tag of any language passes through, and an empty request names none.
func TestLocaleOfPassesTheRequestLanguageThrough(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"en-US,en;q=0.9", "en-US"},
		{"en", "en"},
		// A tag the old closed set did not hold is neither mapped nor dropped: it
		// is passed through for the SMS module to resolve.
		{"zh-Hans-CN", "zh-Hans-CN"},
		{"ja-JP", "ja-JP"},
		{"fr-FR", "fr-FR"},
		{"de,en;q=0.5", "de"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				c.Request.Header.Set("Accept-Language", tc.header)
			}
			if got := localeOf(c); got != tc.want {
				t.Errorf("localeOf(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestTheLanguageReachesTheFlowRecord holds the wiring: the language the engine
// reads off the start request is the one the flow record keeps, which is what a
// later SMS send reads and resolves.
func TestTheLanguageReachesTheFlowRecord(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`, "Accept-Language", "ja-JP,en;q=0.9")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := flowCookie(t, rec)
	if got := flowRecordOf(t, r, cookie).Locale; got != "ja-JP" {
		t.Errorf("the flow record locale is %q, want %q", got, "ja-JP")
	}
}
