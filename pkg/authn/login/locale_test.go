package login

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// TestLocaleOfMapsAcceptLanguageToTheClosedSet holds the engine's mapping of the
// request's Accept-Language to the closed set the SMS templates use: the first
// tag's primary subtag names the language, a tag the set does not name reads as
// nothing, and the empty header reads as nothing. The empty result is what the
// default further down reads.
func TestLocaleOfMapsAcceptLanguageToTheClosedSet(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"en-US,en;q=0.9", authn.LocaleEN},
		{"en", authn.LocaleEN},
		{"zh-Hans-CN", authn.LocaleZH},
		{"zh-CN,zh;q=0.9", authn.LocaleZH},
		{"ZH", authn.LocaleZH},
		{"fr-FR", ""},
		{"de,en;q=0.5", ""},
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
// maps from the start request's Accept-Language is the one the flow record keeps,
// which is what a later SMS send reads.
func TestTheLanguageReachesTheFlowRecord(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`, "Accept-Language", "en-US,en;q=0.9")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := flowCookie(t, rec)
	if got := flowRecordOf(t, r, cookie).Locale; got != authn.LocaleEN {
		t.Errorf("the flow record locale is %q, want %q", got, authn.LocaleEN)
	}
}
