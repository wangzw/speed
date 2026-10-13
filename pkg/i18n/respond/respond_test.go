package respond

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// catalog is the message set the tests render against: a typed key and a
// parameterised one in both locales, and the status-derived keys the responder
// ships for a problem that names no message.
var catalog = fakeLocalizer{
	i18n.LocaleEN: {
		"authn.login-failed": "Login failed",
		"authn.retry-later":  "Try again in {{seconds}} seconds",
		"http.404":           "Not Found",
		"http.429":           "Too Many Requests",
	},
	i18n.LocaleZH: {
		"authn.login-failed": "登录失败",
		"authn.retry-later":  "请在 {{seconds}} 秒后重试",
		"http.404":           "未找到",
		"http.429":           "请求过于频繁",
	},
}

// fakeLocalizer is a fixed catalog of locale to key to message. The responder
// reaches it only through Translate, so Localize is never on this path.
type fakeLocalizer map[i18n.Locale]map[i18n.Key]string

func (fakeLocalizer) Localize(context.Context, i18n.Key, i18n.Args) string {
	panic("test localizer: the responder renders through Translate, not Localize")
}

// Translate renders key in locale, interpolating {{name}} from args and leaving
// a name with no value as written, the way the engine does.
func (f fakeLocalizer) Translate(locale i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	messages, ok := f[locale]
	if !ok {
		return "", false
	}
	message, ok := messages[key]
	if !ok {
		return "", false
	}
	for name, value := range args {
		message = strings.ReplaceAll(message, "{{"+name+"}}", value)
	}
	return message, true
}

// fakeResolver answers with the locale written into the context, or Default,
// the way the engine's resolver does. The responder reads only LocaleOf.
type fakeResolver struct{}

func (fakeResolver) LocaleOf(ctx context.Context) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	return i18n.Default
}

func (fakeResolver) TimeZoneOf(context.Context) string             { return "" }
func (fakeResolver) LocaleFor(context.Context, string) i18n.Locale { return i18n.Default }
func (fakeResolver) TimeZoneFor(context.Context, string) string    { return "" }

// render runs p through a responder over catalog with the request's locale set
// to locale. Headers in before are set on the response writer first, so a test
// can pin which of them survive.
func render(t *testing.T, locale i18n.Locale, p speedhttp.Problem, before nethttp.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodGet, "/thing", nil)
	req = req.WithContext(i18n.WithLocale(req.Context(), locale))
	rec := httptest.NewRecorder()
	for name, values := range before {
		for _, value := range values {
			rec.Header().Add(name, value)
		}
	}
	r := &responder{localizer: catalog, resolver: fakeResolver{}}
	r.RespondError(rec, req, p)
	return rec
}

// decodeBody reads the recorded body as a problem details object.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) problemBody {
	t.Helper()
	var body problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response body %q: %v", rec.Body.String(), err)
	}
	return body
}

// TestALocalizedTitleKeepsTypeAndStatusVerbatim pins the shape of a rendered
// error: the title is the request's language and Content-Language names it,
// while the machine-readable type and the status are written exactly as the
// refusing layer set them.
func TestALocalizedTitleKeepsTypeAndStatusVerbatim(t *testing.T) {
	rec := render(t, i18n.LocaleZH, speedhttp.Problem{
		Type:   "urn:speed:authn:login-failed",
		Status: nethttp.StatusUnauthorized,
	}, nil)

	if rec.Code != nethttp.StatusUnauthorized {
		t.Errorf("response status: got %d, want %d", rec.Code, nethttp.StatusUnauthorized)
	}
	header := rec.Result().Header
	if got := header.Get("Content-Language"); got != "zh-CN" {
		t.Errorf("Content-Language: got %q, want %q", got, "zh-CN")
	}
	if got := header.Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/problem+json")
	}
	body := decodeBody(t, rec)
	if body.Title != "登录失败" {
		t.Errorf("title: got %q, want %q", body.Title, "登录失败")
	}
	if body.Type != "urn:speed:authn:login-failed" {
		t.Errorf("type: got %q, want %q", body.Type, "urn:speed:authn:login-failed")
	}
	if body.Status != nethttp.StatusUnauthorized {
		t.Errorf("body status: got %d, want %d", body.Status, nethttp.StatusUnauthorized)
	}
}

// TestTheDefaultShapeWritesTheEnglishStatusText is the baseline the localized
// title is a change from: with no responder delivered, the http module writes
// the standard library's status text, which is English whatever the request's
// language, and names no Content-Language.
func TestTheDefaultShapeWritesTheEnglishStatusText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(nethttp.MethodGet, "/thing", nil)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})

	if got := decodeBody(t, rec).Title; got != "Not Found" {
		t.Errorf("default title: got %q, want %q", got, "Not Found")
	}
	if got := rec.Result().Header.Get("Content-Language"); got != "" {
		t.Errorf("default Content-Language: got %q, want none", got)
	}
}

// TestFallbacks pins the order a title's source is chosen in: a message key, a
// given title, a key derived from the type, and the standard status text last.
func TestFallbacks(t *testing.T) {
	cases := []struct {
		name   string
		p      speedhttp.Problem
		locale i18n.Locale
		want   string
	}{
		{
			name:   "a message key no catalog carries falls to the standard text",
			p:      speedhttp.Problem{Key: "authn.gone", Status: nethttp.StatusNotFound},
			locale: i18n.LocaleZH,
			want:   "Not Found",
		},
		{
			name:   "a type whose derived key no catalog carries falls to the standard text",
			p:      speedhttp.Problem{Type: "urn:speed:authn:gone", Status: nethttp.StatusNotFound},
			locale: i18n.LocaleZH,
			want:   "Not Found",
		},
		{
			// about:blank is the RFC 9457 default type, which says the status
			// code is the whole story; the status-derived key answers it.
			name:   "the RFC 9457 default type takes the status-derived key",
			p:      speedhttp.Problem{Type: "about:blank", Status: nethttp.StatusNotFound},
			locale: i18n.LocaleZH,
			want:   "未找到",
		},
		{
			name:   "a type the built-in catalog carries is rendered from it",
			p:      speedhttp.Problem{Type: "urn:speed:http:404", Status: nethttp.StatusNotFound},
			locale: i18n.LocaleZH,
			want:   "未找到",
		},
		{
			// The module ships this catalog so that a problem which names no
			// type still renders a localized title rather than the standard
			// library's English.
			name:   "a problem that names no type renders the status-derived key",
			p:      speedhttp.Problem{Status: nethttp.StatusNotFound},
			locale: i18n.LocaleZH,
			want:   "未找到",
		},
		{
			name:   "a problem that already carries a title keeps it",
			p:      speedhttp.Problem{Type: "urn:speed:authn:login-failed", Title: "Already set", Status: nethttp.StatusUnauthorized},
			locale: i18n.LocaleZH,
			want:   "Already set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := render(t, tc.locale, tc.p, nil)
			if got := decodeBody(t, rec).Title; got != tc.want {
				t.Errorf("title: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAParameterisedTitle pins that a problem naming a key with Args interpolates
// them, and that a name with no argument is left in the text rather than dropped.
func TestAParameterisedTitle(t *testing.T) {
	t.Run("fills the placeholders from Args", func(t *testing.T) {
		rec := render(t, i18n.LocaleZH, speedhttp.Problem{
			Key:    "authn.retry-later",
			Args:   map[string]string{"seconds": "30"},
			Status: nethttp.StatusTooManyRequests,
		}, nil)
		if got, want := decodeBody(t, rec).Title, "请在 30 秒后重试"; got != want {
			t.Errorf("title: got %q, want %q", got, want)
		}
	})
	t.Run("leaves a placeholder with no argument as written", func(t *testing.T) {
		rec := render(t, i18n.LocaleZH, speedhttp.Problem{
			Key:    "authn.retry-later",
			Status: nethttp.StatusTooManyRequests,
		}, nil)
		if got, want := decodeBody(t, rec).Title, "请在 {{seconds}} 秒后重试"; got != want {
			t.Errorf("title: got %q, want %q", got, want)
		}
	})
}

// TestHeadersKeptAndDropped pins that a header the caller set before the call
// stays, except the two that would describe a body this response does not write.
func TestHeadersKeptAndDropped(t *testing.T) {
	before := nethttp.Header{}
	before.Set("WWW-Authenticate", `Bearer realm="speed"`)
	before.Set("Retry-After", "120")
	before.Set("Content-Length", "42")
	before.Set("Content-Encoding", "gzip")

	rec := render(t, i18n.LocaleZH, speedhttp.Problem{
		Type:   "urn:speed:authn:login-failed",
		Status: nethttp.StatusUnauthorized,
	}, before)
	header := rec.Result().Header

	for _, name := range []string{"WWW-Authenticate", "Retry-After"} {
		if got := header.Get(name); got == "" {
			t.Errorf("%s: got %q, want it kept", name, got)
		}
	}
	for _, name := range []string{"Content-Length", "Content-Encoding"} {
		if got := header.Get(name); got != "" {
			t.Errorf("%s: got %q, want it dropped", name, got)
		}
	}
}

// TestAStatusOutsideTheRangeIsWrittenAs500 pins that a value outside 200 through
// 599 is not a final status and is written as 500, in the response and the body
// alike, while a final status is written verbatim.
func TestAStatusOutsideTheRangeIsWrittenAs500(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{0, nethttp.StatusInternalServerError},
		{199, nethttp.StatusInternalServerError},
		{600, nethttp.StatusInternalServerError},
		{999, nethttp.StatusInternalServerError},
		{nethttp.StatusOK, nethttp.StatusOK},
		{599, 599},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			rec := render(t, i18n.Default, speedhttp.Problem{Title: "given", Status: tc.status}, nil)
			if rec.Code != tc.want {
				t.Errorf("response status: got %d, want %d", rec.Code, tc.want)
			}
			if got := decodeBody(t, rec).Status; got != tc.want {
				t.Errorf("body status: got %d, want %d", got, tc.want)
			}
		})
	}
}
