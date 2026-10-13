package http

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	i18ncatalog "github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/log"
)

// The two locales the tests below carry messages in. They are written out here
// rather than through a language-matching library: the localizer is a stub, and
// the layer that settles the locale reads the Accept-Language header verbatim.
const (
	localeEN = i18n.Locale("en-US")
	localeZH = i18n.Locale("zh-CN")
)

// stubLocalizer renders a message from an in-memory index, the way the engine
// does: it reads the locale from the context, falls back to a default when the
// context carries none or the locale misses the key, and hands the key itself
// back for a key no locale carries.
type stubLocalizer struct {
	index         map[i18n.Locale]map[i18n.Key]string
	defaultLocale i18n.Locale
}

func (s stubLocalizer) Localize(ctx context.Context, key i18n.Key, _ i18n.Args) string {
	locale, ok := i18n.FromContext(ctx)
	if !ok {
		locale = s.defaultLocale
	}
	if text, ok := s.index[locale][key]; ok {
		return text
	}
	if locale != s.defaultLocale {
		if text, ok := s.index[s.defaultLocale][key]; ok {
			return text
		}
	}
	return string(key)
}

func (s stubLocalizer) Translate(locale i18n.Locale, key i18n.Key, _ i18n.Args) (string, bool) {
	text, ok := s.index[locale][key]
	return text, ok
}

var _ i18n.Localizer = stubLocalizer{}

// acceptLanguageLayer settles the request's locale from its Accept-Language
// header, the way the i18n capability's observable layer does. It stands outside
// the recovery and the body limit — it is an observing layer — so all four
// refusals this module writes see the locale it set.
func acceptLanguageLayer() Middleware {
	return Middleware{
		Name:     "test:accept-language",
		Stage:    StageObserve,
		Position: PositionFirst,
		Handler: func(c *gin.Context) {
			if tag := c.GetHeader("Accept-Language"); tag != "" {
				c.Request = c.Request.WithContext(i18n.WithLocale(c.Request.Context(), i18n.Locale(tag)))
			}
			c.Next()
		},
	}
}

// assembleLocalized assembles one endpoint the way Serve does, with localizer as
// the Localizer capability and the accept-language layer in front of the chain,
// so a test can drive a refusal in a chosen language.
func assembleLocalized(t *testing.T, settings endpointSettings, localizer i18n.Localizer, register func(Endpoint)) nethttp.Handler {
	t.Helper()
	logger, _ := newRecordingLogger()
	handler, err := assembleEndpointWith(t, settings, nil, logger, nil, localizer, func(e Endpoint) {
		e.Use(acceptLanguageLayer())
		register(e)
	})
	if err != nil {
		t.Fatalf("assembling endpoint %q: %v", settings.name, err)
	}
	return handler
}

// serveLocalizedOnce sends one request through an assembled handler, with the
// Accept-Language header the test chose, and returns what it wrote.
func serveLocalizedOnce(handler nethttp.Handler, method, path, lang string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

// titleOf decodes a problem details body and returns its title member.
func titleOf(t *testing.T, body string) string {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal([]byte(body), &members); err != nil {
		t.Fatalf("the body %q is not a problem details object: %v", body, err)
	}
	title, _ := members["title"].(string)
	return title
}

// refusalCase is one of the four refusals this module writes itself, with the
// route set and the request that triggers it.
type refusalCase struct {
	name     string
	status   int
	method   string
	path     string
	maxBody  int64
	register func(Endpoint)
	body     func() io.Reader
}

// refusalCases is the four refusals: the no-route 404, the method-not-allowed
// 405, the body-too-large 413 and the recovered-panic 500.
func refusalCases() []refusalCase {
	return []refusalCase{
		{
			name: "the 404", status: nethttp.StatusNotFound,
			method: nethttp.MethodGet, path: "/nowhere",
			register: func(Endpoint) {},
		},
		{
			name: "the 405", status: nethttp.StatusMethodNotAllowed,
			method: nethttp.MethodPost, path: "/things",
			register: func(e Endpoint) { e.Route("GET", "/things", filler) },
		},
		{
			name: "the 413", status: nethttp.StatusRequestEntityTooLarge, maxBody: 16,
			method: nethttp.MethodPost, path: "/things",
			register: func(e Endpoint) { e.Route("POST", "/things", filler) },
			body:     func() io.Reader { return strings.NewReader(strings.Repeat("x", 64)) },
		},
		{
			name: "the 500", status: nethttp.StatusInternalServerError,
			method: nethttp.MethodGet, path: "/things",
			register: func(e Endpoint) { e.Route("GET", "/things", panicking("boom")) },
		},
	}
}

// settingsFor is the endpoint settings one refusal case needs: the shared
// test settings, with the body limit overridden where the case drives a 413.
func settingsFor(c refusalCase) endpointSettings {
	s := testSettings("public")
	if c.maxBody > 0 {
		s.maxBodyBytes = c.maxBody
	}
	return s
}

// refusalCatalog is the http.<status> catalog the tests below carry, in two
// locales, mirroring this module's own embedded catalog.
func refusalCatalog() stubLocalizer {
	return stubLocalizer{
		defaultLocale: localeEN,
		index: map[i18n.Locale]map[i18n.Key]string{
			localeEN: {
				refusalKey(nethttp.StatusNotFound):              "Not Found",
				refusalKey(nethttp.StatusMethodNotAllowed):      "Method Not Allowed",
				refusalKey(nethttp.StatusRequestEntityTooLarge): "Payload Too Large",
				refusalKey(nethttp.StatusInternalServerError):   "Internal Server Error",
			},
			localeZH: {
				refusalKey(nethttp.StatusNotFound):              "未找到",
				refusalKey(nethttp.StatusMethodNotAllowed):      "方法不被允许",
				refusalKey(nethttp.StatusRequestEntityTooLarge): "请求体过大",
				refusalKey(nethttp.StatusInternalServerError):   "服务器内部错误",
			},
		},
	}
}

// TestARefusalIsRenderedInTheAssembledLocalizersLanguage pins that each of the
// four refusals this module writes itself carries a title in the request's
// language, rendered from the module's own catalog through the assembled
// localizer. The request asks for zh-CN, and every refusal answers with the
// text this module's catalog carries for that locale rather than the standard
// library's English.
func TestARefusalIsRenderedInTheAssembledLocalizersLanguage(t *testing.T) {
	localizer := refusalCatalog()
	for _, c := range refusalCases() {
		t.Run(c.name, func(t *testing.T) {
			handler := assembleLocalized(t, settingsFor(c), localizer, c.register)
			var body io.Reader
			if c.body != nil {
				body = c.body()
			}
			recorder := serveLocalizedOnce(handler, c.method, c.path, "zh-CN", body)

			if recorder.Code != c.status {
				t.Fatalf("the refusal was answered %d, want %d", recorder.Code, c.status)
			}
			want := localizer.index[localeZH][refusalKey(c.status)]
			if got := problemBodyOf(t, recorder)["title"]; got != want {
				t.Errorf("the title is %v, want the %q the assembled localizer's zh-CN catalog "+
					"carries for the %d", got, want, c.status)
			}
		})
	}
}

// TestTheModuleTakesUpTheLocalizerCapability is the same rendering over a whole
// assembly: a probe delivers i18n.Localizer, this module's New resolves it, and
// a refusal answered on a real connection carries the text of that localizer.
// The request names no language, so the localizer falls back to its default —
// which the test sets to zh-CN — which is how the answer is told from the
// standard library's English.
func TestTheModuleTakesUpTheLocalizerCapability(t *testing.T) {
	localizer := stubLocalizer{
		defaultLocale: localeZH,
		index: map[i18n.Locale]map[i18n.Key]string{
			localeZH: {refusalKey(nethttp.StatusNotFound): "未找到"},
		},
	}
	addr := serveLocalizer(t, localizer, oneEndpoint("public"), func(e Endpoint) {
		e.Route("GET", "/things", filler)
	})

	got, err := getResponse(context.Background(), addr, "/nowhere")
	if err != nil {
		t.Fatalf("the request for a path that matches no route failed: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Fatalf("the refusal was answered %d, want %d", got.status, nethttp.StatusNotFound)
	}
	if title := titleOf(t, got.body); title != "未找到" {
		t.Errorf("the title is %q, want %q: the module did not take up the delivered localizer",
			title, "未找到")
	}
}

// TestARefusalFallsBackToTheStandardStatusTextWithoutALocalizer pins the other
// side: with no localizer assembled, each refusal answers with net/http's
// standard text for its status, the text an assembly without the i18n
// capability has always written. The accept-language layer still settles a
// locale, which is exactly the point — the locale alone renders nothing.
func TestARefusalFallsBackToTheStandardStatusTextWithoutALocalizer(t *testing.T) {
	for _, c := range refusalCases() {
		t.Run(c.name, func(t *testing.T) {
			handler := assembleLocalized(t, settingsFor(c), nil, c.register)
			var body io.Reader
			if c.body != nil {
				body = c.body()
			}
			recorder := serveLocalizedOnce(handler, c.method, c.path, "zh-CN", body)

			if recorder.Code != c.status {
				t.Fatalf("the refusal was answered %d, want %d", recorder.Code, c.status)
			}
			want := nethttp.StatusText(c.status)
			if got := problemBodyOf(t, recorder)["title"]; got != want {
				t.Errorf("with no localizer assembled the title is %v, want the standard text %q",
					got, want)
			}
		})
	}
}

// TestAnAssemblyWithNoLocalizerStillStarts pins that the optional Localizer is
// no precondition: a run that delivers none starts and serves, and its
// no-route refusal answers over a real connection with the standard text.
func TestAnAssemblyWithNoLocalizerStillStarts(t *testing.T) {
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/things", filler)
	})

	got, err := getResponse(context.Background(), addr, "/nowhere")
	if err != nil {
		t.Fatalf("the request failed, so the run did not serve without a localizer: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Errorf("the refusal was answered %d, want %d", got.status, nethttp.StatusNotFound)
	}
	if title := titleOf(t, got.body); title != nethttp.StatusText(nethttp.StatusNotFound) {
		t.Errorf("the title is %q, want the standard text %q", title, nethttp.StatusText(nethttp.StatusNotFound))
	}
}

// TestARefusalFallsBackWhenTheCatalogDoesNotCarryItsKey pins the boundary of the
// fallback: a key the assembled catalog carries is rendered, and a key it does
// not carry falls back to the standard text rather than to the key itself or an
// empty title. The same localizer serves both — it carries http.500 and nothing
// else — so the 500 proves the localizer is engaged while the 404 proves the
// miss is handled.
func TestARefusalFallsBackWhenTheCatalogDoesNotCarryItsKey(t *testing.T) {
	localizer := stubLocalizer{
		defaultLocale: localeEN,
		index: map[i18n.Locale]map[i18n.Key]string{
			localeEN: {refusalKey(nethttp.StatusInternalServerError): "a configured title"},
		},
	}

	carried := assembleLocalized(t, testSettings("public"), localizer, func(e Endpoint) {
		e.Route("GET", "/things", panicking("boom"))
	})
	recorder := serveLocalizedOnce(carried, nethttp.MethodGet, "/things", "en-US", nil)
	if got := problemBodyOf(t, recorder)["title"]; got != "a configured title" {
		t.Fatalf("the 500 title is %v, want the %q the catalog carries for its key", got, "a configured title")
	}

	missing := assembleLocalized(t, testSettings("public"), localizer, func(Endpoint) {})
	recorder = serveLocalizedOnce(missing, nethttp.MethodGet, "/nowhere", "en-US", nil)
	want := nethttp.StatusText(nethttp.StatusNotFound)
	got, present := problemBodyOf(t, recorder)["title"]
	if got != want {
		t.Errorf("a key no catalog carries produced the title %v (present: %v), want %q: the "+
			"refusal falls back to the standard text rather than to the key or to nothing",
			got, present, want)
	}
}

// TestProblemCarriesNoLocalizationInputs pins that the wire contract no longer
// carries a message key and its arguments. They were inputs to a responder that
// rendered the language at the edge; the module that produces a problem now
// renders its own title, so the fields are gone and Title carries finished text.
func TestProblemCarriesNoLocalizationInputs(t *testing.T) {
	present := map[string]bool{}
	for field := range reflect.TypeFor[Problem]().Fields() {
		present[field.Name] = true
	}
	for _, gone := range []string{"Key", "Args"} {
		if present[gone] {
			t.Errorf("Problem still carries the field %q, a localization input the producing module "+
				"now renders itself instead of putting on the wire", gone)
		}
	}
}

// serveLocalizer runs a real assembly with one endpoint, "public", the routes
// the test registers and localizer delivered as the Localizer capability, and
// returns the address it bound. It is serveRoutes with the localizer under the
// test's control, so a refusal can be observed on a real connection after the
// module resolved the capability through the registry.
func serveLocalizer(t *testing.T, localizer i18n.Localizer, cfg moduleConfig, register func(Endpoint)) string {
	t.Helper()
	logger, _ := newRecordingLogger()
	modules := []core.Module{
		Module(),
		(&stageProbe{
			name:     "probe.delivers-the-logger",
			provides: []core.Provision{{Token: (*log.Logger)(nil)}},
			product:  stubLogger{logger: logger},
		}).module(),
		(&stageProbe{
			name:     "probe.delivers-the-localizer",
			provides: []core.Provision{{Token: (*i18n.Localizer)(nil), Exclusive: true}},
			product:  localizer,
		}).module(),
	}
	serving := make(chan struct{})
	modules = append(modules, (&stageProbe{
		name:     "probe.registers-the-routes",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			e, err := endpointFrom(reg, "public")
			if err != nil {
				return err
			}
			e.Use(acceptLanguageLayer())
			register(e)
			return nil
		},
		onServe: func(*core.Registry) error {
			close(serving)
			return nil
		},
	}).module())

	a := startAssembly(t, cfg, modules...)
	a.waitForOrEnd(t, serving, "the endpoint to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatalf("finding the address the endpoint bound: %v", err)
	}
	return addr
}

// stubResolver is an i18n.Resolver whose answers a test drives: LocaleOf
// reports the locale the context carries, and the default when the context
// carries none, which is the resolution the engine performs.
type stubResolver struct{ defaultLocale i18n.Locale }

func (s stubResolver) Default() i18n.Locale { return s.defaultLocale }

func (s stubResolver) LocaleOf(ctx context.Context) i18n.Locale {
	if locale, ok := i18n.FromContext(ctx); ok {
		return locale
	}
	return s.defaultLocale
}

func (s stubResolver) TimeZoneOf(context.Context) string { return "" }

func (s stubResolver) LocaleFor(ctx context.Context, _ string) i18n.Locale { return s.LocaleOf(ctx) }

func (s stubResolver) TimeZoneFor(context.Context, string) string { return "" }

var _ i18n.Resolver = stubResolver{}

// TestTheDefaultProblemNamesTheResponsesLanguage pins that the default problem
// shape names the language it wrote in a Content-Language header: with a
// resolver assembled the header carries the locale the resolver resolves for
// the request, which is the locale the title was rendered in. A client that
// asked for one language and was answered in another — a fallback — is thus
// told which it got. With no resolver assembled the header is absent, and the
// refusal is still written.
func TestTheDefaultProblemNamesTheResponsesLanguage(t *testing.T) {
	logger, _ := newRecordingLogger()
	localizer := refusalCatalog()
	resolver := stubResolver{defaultLocale: localeEN}

	named, err := assembleEndpointWithResolver(t, testSettings("public"), nil, logger, nil, localizer, resolver,
		func(e Endpoint) { e.Use(acceptLanguageLayer()) })
	if err != nil {
		t.Fatalf("assembling endpoint: %v", err)
	}
	recorder := serveLocalizedOnce(named, nethttp.MethodGet, "/nowhere", "zh-CN", nil)
	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("the refusal was answered %d, want %d", recorder.Code, nethttp.StatusNotFound)
	}
	if got := recorder.Header().Get("Content-Language"); got != "zh-CN" {
		t.Errorf("Content-Language is %q, want %q: the default shape did not name the "+
			"language it wrote the body in", got, "zh-CN")
	}

	unnamed, err := assembleEndpointWithResolver(t, testSettings("public"), nil, logger, nil, localizer, nil,
		func(e Endpoint) { e.Use(acceptLanguageLayer()) })
	if err != nil {
		t.Fatalf("assembling endpoint without a resolver: %v", err)
	}
	recorder = serveLocalizedOnce(unnamed, nethttp.MethodGet, "/nowhere", "zh-CN", nil)
	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("with no resolver the refusal was answered %d, want %d", recorder.Code, nethttp.StatusNotFound)
	}
	if got := recorder.Header().Get("Content-Language"); got != "" {
		t.Errorf("with no resolver assembled Content-Language is %q, want it absent", got)
	}
}

// TestTheEmbeddedCatalogCarriesOneKeySetForEveryLocale holds that every locale
// file this module ships carries the same keys, and that they are exactly the
// refusals it renders.
//
// Rendering alone cannot catch a key that drifted in one file: the en-US texts
// are net/http's standard phrases, which is also what a refusal falls back to
// on a miss, so a renamed en-US key answers the same text and the rendering
// test would pass. The key sets are therefore compared as sets, every file
// against the four refusals, which also refuses a key this module never
// renders.
func TestTheEmbeddedCatalogCarriesOneKeySetForEveryLocale(t *testing.T) {
	want := map[string]bool{}
	for _, status := range []int{
		nethttp.StatusNotFound,
		nethttp.StatusMethodNotAllowed,
		nethttp.StatusRequestEntityTooLarge,
		nethttp.StatusInternalServerError,
	} {
		want[string(refusalKey(status))] = true
	}
	names, err := fs.Glob(localesFS, messageCatalog.Pattern)
	if err != nil {
		t.Fatalf("the catalog pattern %q is malformed: %v", messageCatalog.Pattern, err)
	}
	if len(names) == 0 {
		t.Fatalf("the catalog pattern %q matched no file", messageCatalog.Pattern)
	}
	for _, name := range names {
		raw, err := fs.ReadFile(localesFS, name)
		if err != nil {
			t.Fatalf("reading the embedded %s: %v", name, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s is not a JSON object of message text: %v", name, err)
		}
		for key := range want {
			if _, ok := entries[key]; !ok {
				t.Errorf("%s carries no entry for %q, which this module renders", name, key)
			}
		}
		for key := range entries {
			if !want[key] {
				t.Errorf("%s carries %q, which this module never renders", name, key)
			}
		}
	}
}

// TestTheEmbeddedCatalogRendersThroughTheRealEngine drives this module's own
// embedded catalog through the real i18n engine, assembled the way a host puts
// it together: http.Module() and the engine's catalog module take part in one
// registry, the engine merges the catalog the module declares as an
// i18n.Catalog resource, and a refusal answered on a real connection carries
// the text the module's own locales/*.json carries for the locale the request
// asked for. The stub localizers the other tests use exercise this module's
// rendering point, not the catalog file: a malformed locales/*.json or a key
// the code builds and this file does not carry would pass every one of them and
// fail here for the locale it drives. What it cannot see is a key that drifted
// in another locale's file alone — the en-US texts are net/http's own, which is
// also what a miss falls back to — so the key sets are compared across files in
// TestTheEmbeddedCatalogCarriesOneKeySetForEveryLocale.
func TestTheEmbeddedCatalogRendersThroughTheRealEngine(t *testing.T) {
	addr := serveWithRealEngine(t, func(e Endpoint) {
		e.Route("GET", "/things", filler)
	})

	got, err := requestWithLanguage(context.Background(), addr, "/nowhere", "zh-CN")
	if err != nil {
		t.Fatalf("the request for a path that matches no route failed: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Fatalf("the refusal was answered %d, want %d", got.status, nethttp.StatusNotFound)
	}
	if title := titleOf(t, got.body); title != "未找到" {
		t.Errorf("the 404 title is %q, want %q: the module's embedded catalog did not render "+
			"through the real engine", title, "未找到")
	}
}

// serveWithRealEngine assembles this module together with the real i18n engine
// and its catalog module — the way the catalog package's own module-level tests
// assemble it and the way a host does — and serves one endpoint, so a refusal
// can be observed on a real connection with the module's own embedded catalog
// behind it. It returns the address the endpoint bound.
func serveWithRealEngine(t *testing.T, register func(Endpoint)) string {
	t.Helper()
	// Both schemas are declared to the real loader, because both modules read
	// their own section of one configuration document.
	reader := authntest.ConfigReaderAll(t,
		`{"http":{"endpoints":{"public":{"address":"127.0.0.1:0"}}}}`,
		Module(), i18ncatalog.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	reg.Register(i18ncatalog.Module())
	serving := make(chan struct{})
	reg.Register((&stageProbe{
		name:     "probe.registers-the-routes",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			e, err := endpointFrom(reg, "public")
			if err != nil {
				return err
			}
			e.Use(acceptLanguageLayer())
			register(e)
			return nil
		},
		onServe: func(*core.Registry) error {
			close(serving)
			return nil
		},
	}).module())

	a := runAssembly(t, reg)
	a.waitForOrEnd(t, serving, "the endpoint to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatalf("finding the address the endpoint bound: %v", err)
	}
	return addr
}

// requestWithLanguage issues one GET with an Accept-Language header against a
// bound endpoint, so a test can drive a refusal in a language the real engine
// negotiates. It is getResponse with the one header set.
func requestWithLanguage(ctx context.Context, addr, path, lang string) (answer, error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return answer{}, err
	}
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	client := &nethttp.Client{
		Timeout:   waitDeadline,
		Transport: &nethttp.Transport{DisableKeepAlives: true, DisableCompression: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	body, bodyErr := io.ReadAll(resp.Body)
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body), bodyErr: bodyErr}, nil
}
