package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

const (
	localeEN i18n.Locale = "en-US"
	localeZH i18n.Locale = "zh-CN"
)

type fakeNegotiator struct {
	locale i18n.Locale
	ok     bool
	called bool
	header string
}

func (f *fakeNegotiator) Negotiate(acceptLanguage string) (i18n.Locale, bool) {
	f.called = true
	f.header = acceptLanguage
	return f.locale, f.ok
}

func ginContext(ctx context.Context, acceptLanguage string) *gin.Context {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	req = req.WithContext(ctx)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// TestNegotiationLayerWritesTheNegotiatedLocale checks that the layer writes a
// matched locale, keeps one present and writes nothing on no match.
func TestNegotiationLayerWritesTheNegotiatedLocale(t *testing.T) {
	cases := []struct {
		name    string
		base    i18n.Locale
		header  string
		matched i18n.Locale
		ok      bool
		called  bool
		want    i18n.Locale
		wantAny bool
	}{
		{
			name:    "writes the matched locale",
			header:  "zh-CN,zh;q=0.9",
			matched: localeZH,
			ok:      true,
			called:  true,
			want:    localeZH,
			wantAny: true,
		},
		{
			name:    "leaves a locale the context already carries",
			base:    localeEN,
			header:  "zh-CN,zh;q=0.9",
			matched: localeZH,
			ok:      true,
			called:  false,
			want:    localeEN,
			wantAny: true,
		},
		{
			name:    "writes nothing when the negotiator reports no match",
			header:  "fr-FR",
			matched: localeZH,
			ok:      false,
			called:  true,
			wantAny: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			neg := &fakeNegotiator{locale: tc.matched, ok: tc.ok}
			ctx := context.Background()
			if tc.base != "" {
				ctx = i18n.WithLocale(ctx, tc.base)
			}
			c := ginContext(ctx, tc.header)

			negotiationLayer(neg).Handler(c)

			if neg.called != tc.called {
				t.Errorf("the negotiator was called=%v, want %v", neg.called, tc.called)
			}
			if tc.called && neg.header != tc.header {
				t.Errorf("the negotiator was handed %q, want the request's Accept-Language %q", neg.header, tc.header)
			}
			got, ok := i18n.FromContext(c.Request.Context())
			if ok != tc.wantAny {
				t.Fatalf("FromContext reported present=%v, want %v", ok, tc.wantAny)
			}
			if tc.wantAny && got != tc.want {
				t.Errorf("the context locale is %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProfileLayerFillsFromTheIdentity checks that the profile layer copies the
// identity's locale and zone into the context only where it carries neither.
func TestProfileLayerFillsFromTheIdentity(t *testing.T) {
	attrs := map[string]string{
		i18n.LocaleAttributeKey:   string(localeZH),
		i18n.TimeZoneAttributeKey: "Asia/Shanghai",
	}
	cases := []struct {
		name       string
		base       func(context.Context) context.Context
		wantLocale i18n.Locale
		wantZone   string
	}{
		{
			name:       "fills both when the context carries neither",
			base:       func(ctx context.Context) context.Context { return ctx },
			wantLocale: localeZH,
			wantZone:   "Asia/Shanghai",
		},
		{
			name:       "keeps a locale already present and still fills the zone",
			base:       func(ctx context.Context) context.Context { return i18n.WithLocale(ctx, localeEN) },
			wantLocale: localeEN,
			wantZone:   "Asia/Shanghai",
		},
		{
			name:       "keeps a zone already present and still fills the locale",
			base:       func(ctx context.Context) context.Context { return i18n.WithTimeZone(ctx, "UTC") },
			wantLocale: localeZH,
			wantZone:   "UTC",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idCtx := authntest.Context(t, context.Background(), authntest.WithAttributes(attrs))
			c := ginContext(tc.base(idCtx), "")

			profileLayer().Handler(c)

			got, ok := i18n.FromContext(c.Request.Context())
			if !ok || got != tc.wantLocale {
				t.Errorf("the context locale is %q (present=%v), want %q", got, ok, tc.wantLocale)
			}
			zone, ok := i18n.TimeZoneFromContext(c.Request.Context())
			if !ok || zone != tc.wantZone {
				t.Errorf("the context time zone is %q (present=%v), want %q", zone, ok, tc.wantZone)
			}
		})
	}
}

// TestProfileLayerLeavesAContextWithoutAnIdentityAlone checks that a context
// with no identity passes through unwritten.
func TestProfileLayerLeavesAContextWithoutAnIdentityAlone(t *testing.T) {
	c := ginContext(context.Background(), "")

	profileLayer().Handler(c)

	if _, ok := i18n.FromContext(c.Request.Context()); ok {
		t.Error("the profile layer wrote a locale onto a context with no identity")
	}
	if _, ok := i18n.TimeZoneFromContext(c.Request.Context()); ok {
		t.Error("the profile layer wrote a time zone onto a context with no identity")
	}
}

// TestDeclaredLayers checks the stage, order, anchor and capability each layer
// declares.
func TestDeclaredLayers(t *testing.T) {
	neg := negotiationLayer(&fakeNegotiator{})

	if neg.Handler == nil {
		t.Error("the negotiation layer has no handler")
	}
	if neg.Name != "i18n:accept-language" {
		t.Errorf("negotiation Name = %q, want %q", neg.Name, "i18n:accept-language")
	}
	if neg.Stage != speedhttp.StageObserve {
		t.Errorf("negotiation Stage = %v, want StageObserve", neg.Stage)
	}
	if neg.Order != -100 {
		t.Errorf("negotiation Order = %v, want -100", neg.Order)
	}
	if len(neg.Provides) != 1 || neg.Provides[0] != core.Token((*i18n.LocaleResolver)(nil)) {
		t.Errorf("negotiation Provides = %v, want the (*i18n.LocaleResolver)(nil) token", neg.Provides)
	}

	prof := profileLayer()

	if prof.Handler == nil {
		t.Error("the profile layer has no handler")
	}
	if prof.Name != "i18n:profile-locale" {
		t.Errorf("profile Name = %q, want %q", prof.Name, "i18n:profile-locale")
	}
	if prof.Stage != speedhttp.StageIdentify {
		t.Errorf("profile Stage = %v, want StageIdentify", prof.Stage)
	}
	if len(prof.After) != 1 || prof.After[0] != core.Token((*authn.Gate)(nil)) {
		t.Errorf("profile After = %v, want the (*authn.Gate)(nil) token", prof.After)
	}
}
