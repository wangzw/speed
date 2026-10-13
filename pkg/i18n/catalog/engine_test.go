package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// The tags these cases write by name. The engine no longer exports them, so the
// test names them itself.
const (
	localeEN i18n.Locale = "en-US"
	localeZH i18n.Locale = "zh-CN"
	localeJA i18n.Locale = "ja-JP"
)

// mapFS builds an in-memory filesystem from file name to contents.
func mapFS(files map[string]string) fstest.MapFS {
	fsys := make(fstest.MapFS, len(files))
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

// catalogOf declares one module's catalog: every file lives under "locales".
func catalogOf(module string, files map[string]string) core.Resource[i18n.Catalog] {
	return core.Resource[i18n.Catalog]{
		Module: module,
		Value:  i18n.Catalog{FS: mapFS(files), Pattern: "locales/*.json"},
	}
}

// twoLocales carries English and Chinese, one distinct key each so the two
// locales never share a key. A key shared across locales is a case of its own,
// in TestLoadMultiLocaleCatalog; keeping it out of the shared fixture leaves the
// rendering and negotiation cases independent of it.
var twoLocales = catalogOf("app", map[string]string{
	"locales/en-US.json": `{"app.en":"Hello"}`,
	"locales/zh-CN.json": `{"app.zh":"你好"}`,
})

// mustEngine builds the engine with the unset configuration's default (en-US)
// or fails the test.
func mustEngine(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile) *engine {
	t.Helper()
	return mustEngineWithDefault(t, catalogs, profiles, localeEN)
}

// mustEngineWithDefault builds the engine over a named configured default, or
// fails the test.
func mustEngineWithDefault(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, def i18n.Locale) *engine {
	t.Helper()
	e, err := newEngine(catalogs, profiles, def)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	return e
}

func TestLocalize(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{twoLocales}
	tests := []struct {
		name string
		ctx  context.Context
		key  i18n.Key
		want string
	}{
		{"the context locale is used", i18n.WithLocale(context.Background(), localeZH), "app.zh", "你好"},
		{"the default locale is the fallback", i18n.WithLocale(context.Background(), localeZH), "app.en", "Hello"},
		{"a key no catalog carries is written back", i18n.WithLocale(context.Background(), localeZH), "app.missing", "app.missing"},
		{"an unnamed context uses the default locale", context.Background(), "app.en", "Hello"},
		{"a key the default locale lacks is written back", context.Background(), "app.zh", "app.zh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngine(t, catalogs, nil)
			if got := e.Localize(tt.ctx, tt.key, nil); got != tt.want {
				t.Errorf("Localize(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestTranslate(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{twoLocales}
	tests := []struct {
		name   string
		locale i18n.Locale
		key    i18n.Key
		want   string
		wantOK bool
	}{
		{"renders the named locale", localeZH, "app.zh", "你好", true},
		{"does not fall back to the default", localeZH, "app.en", "", false},
		{"renders the default locale", localeEN, "app.en", "Hello", true},
		{"reports an unknown key", localeEN, "app.missing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngine(t, catalogs, nil)
			got, ok := e.Translate(tt.locale, tt.key, nil)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Translate(%q, %q) = %q, %v; want %q, %v", tt.locale, tt.key, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestLocalizeInterpolates(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.greet":"Hello {{name}}, you have {{count}} messages"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	ctx := context.Background()
	tests := []struct {
		name string
		args i18n.Args
		want string
	}{
		{"replaces every named placeholder", i18n.Args{"name": "Ada", "count": "3"}, "Hello Ada, you have 3 messages"},
		{"leaves a missing placeholder as written", i18n.Args{"name": "Ada"}, "Hello Ada, you have {{count}} messages"},
		{"no args leave placeholders as written", nil, "Hello {{name}}, you have {{count}} messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Localize(ctx, "app.greet", tt.args); got != tt.want {
				t.Errorf("Localize = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInterpolate(t *testing.T) {
	tests := []struct {
		name    string
		message string
		args    i18n.Args
		want    string
	}{
		{"replaces a placeholder", "{{a}}", i18n.Args{"a": "x"}, "x"},
		{"replaces a repeated placeholder", "{{a}}{{a}}", i18n.Args{"a": "x"}, "xx"},
		{"leaves a name with no value as written", "{{a}}", i18n.Args{"b": "x"}, "{{a}}"},
		{"leaves a message without a placeholder unchanged", "plain", i18n.Args{"a": "x"}, "plain"},
		{"no args leave the message unchanged", "{{a}}", nil, "{{a}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interpolate(tt.message, tt.args); got != tt.want {
				t.Errorf("interpolate(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

func TestNegotiateNoMatch(t *testing.T) {
	e := mustEngine(t, []core.Resource[i18n.Catalog]{twoLocales}, nil)
	tests := []struct {
		name   string
		header string
	}{
		{"an empty header", ""},
		{"only unsupported languages", "de-DE, fr;q=0.8"},
		{"a zero-q entry is dropped", "zh-CN;q=0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if locale, ok := e.Negotiate(tt.header); ok {
				t.Errorf("Negotiate(%q) = %q, %v; want no match", tt.header, locale, ok)
			}
		})
	}
}

func TestNegotiateQValueOrdering(t *testing.T) {
	// The supported set is built from a map, so the matcher's internal order
	// varies from build to build. The negotiated locale must not: build the
	// engine repeatedly and require the same answer every time.
	catalogs := []core.Resource[i18n.Catalog]{twoLocales}
	for i := range 64 {
		e := mustEngine(t, catalogs, nil)
		if locale, ok := e.Negotiate("zh-CN;q=0.9, en;q=1.0"); !ok || locale != localeEN {
			t.Fatalf("build %d: Negotiate(\"zh-CN;q=0.9, en;q=1.0\") = %q, %v; want en-US, true", i, locale, ok)
		}
		if locale, ok := e.Negotiate("en;q=0.9, zh-CN;q=1.0"); !ok || locale != localeZH {
			t.Fatalf("build %d: Negotiate(\"en;q=0.9, zh-CN;q=1.0\") = %q, %v; want zh-CN, true", i, locale, ok)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		catalogs []core.Resource[i18n.Catalog]
		want     error
		contains []string
	}{
		{
			name: "a key two modules declare names both",
			catalogs: []core.Resource[i18n.Catalog]{
				catalogOf("alpha", map[string]string{"locales/en-US.json": `{"shared.key":"A"}`}),
				catalogOf("beta", map[string]string{"locales/en-US.json": `{"shared.key":"B"}`}),
			},
			want:     ErrDuplicateKey,
			contains: []string{"shared.key", `"alpha"`, `"beta"`},
		},
		{
			name: "a file that is not a JSON object",
			catalogs: []core.Resource[i18n.Catalog]{
				catalogOf("app", map[string]string{"locales/en-US.json": `{not json}`}),
			},
			want: ErrInvalidCatalog,
		},
		{
			name: "no catalog carries the default locale",
			catalogs: []core.Resource[i18n.Catalog]{
				catalogOf("app", map[string]string{"locales/zh-CN.json": `{"app.zh":"你好"}`}),
			},
			want: ErrNoCatalog,
		},
		{
			name: "the pattern matches no file",
			catalogs: []core.Resource[i18n.Catalog]{
				{Module: "app", Value: i18n.Catalog{FS: mapFS(map[string]string{"other/x.json": `{}`}), Pattern: "locales/*.json"}},
			},
			want: ErrInvalidCatalog,
		},
		{
			name: "a catalog names no filesystem",
			catalogs: []core.Resource[i18n.Catalog]{
				{Module: "app", Value: i18n.Catalog{Pattern: "locales/*.json"}},
			},
			want: ErrInvalidCatalog,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newEngine(tt.catalogs, nil, localeEN)
			if !errors.Is(err, tt.want) {
				t.Fatalf("newEngine error = %v, want %v", err, tt.want)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err.Error(), want)
				}
			}
		})
	}
}

func TestLoadMultiLocaleCatalog(t *testing.T) {
	// One module ships one key in every locale it supports. That is a single
	// declaration by one module and must load; only a key shared between two
	// modules is a conflict.
	catalogs := []core.Resource[i18n.Catalog]{
		catalogOf("app", map[string]string{
			"locales/en-US.json": `{"app.hello":"Hello"}`,
			"locales/zh-CN.json": `{"app.hello":"你好"}`,
		}),
	}
	if _, err := newEngine(catalogs, nil, localeEN); err != nil {
		t.Fatalf("a module's own multi-locale catalog must load: %v", err)
	}
}

// stubProfile is an account's stored preferences for the resolver cases.
type stubProfile struct {
	locale i18n.Locale
	zone   string
}

func (p stubProfile) LocaleFor(context.Context, string) (i18n.Locale, bool) {
	return p.locale, p.locale != ""
}

func (p stubProfile) TimeZoneFor(context.Context, string) (string, bool) {
	return p.zone, p.zone != ""
}

func TestLocaleFor(t *testing.T) {
	profile := stubProfile{locale: localeZH, zone: "Asia/Shanghai"}
	tests := []struct {
		name     string
		ctx      context.Context
		profiles []i18n.Profile
		want     i18n.Locale
	}{
		{"the context wins over a profile", i18n.WithLocale(context.Background(), localeEN), []i18n.Profile{profile}, localeEN},
		{"the profile is used when the context names none", context.Background(), []i18n.Profile{profile}, localeZH},
		{"the default when there is no profile", context.Background(), nil, localeEN},
		{"the default when no profile carries one", context.Background(), []i18n.Profile{stubProfile{}}, localeEN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngine(t, nil, tt.profiles)
			if got := e.LocaleFor(tt.ctx, "acct"); got != tt.want {
				t.Errorf("LocaleFor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimeZoneFor(t *testing.T) {
	profile := stubProfile{locale: localeZH, zone: "Asia/Shanghai"}
	tests := []struct {
		name     string
		ctx      context.Context
		profiles []i18n.Profile
		want     string
	}{
		{"the context wins over a profile", i18n.WithTimeZone(context.Background(), "UTC"), []i18n.Profile{profile}, "UTC"},
		{"the profile is used when the context names none", context.Background(), []i18n.Profile{profile}, "Asia/Shanghai"},
		{"empty when there is no profile", context.Background(), nil, ""},
		{"empty when no profile carries one", context.Background(), []i18n.Profile{stubProfile{}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngine(t, nil, tt.profiles)
			if got := e.TimeZoneFor(tt.ctx, "acct"); got != tt.want {
				t.Errorf("TimeZoneFor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModuleCollectsDeclaredCatalogs(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "app",
		Resources: []any{
			i18n.Catalog{FS: mapFS(map[string]string{"locales/en-US.json": `{"app.en":"Hello"}`}), Pattern: "locales/*.json"},
		},
	})
	reg.Register(Module())
	// The default locale is read from configuration, so the module resolves a
	// reader in New: supply one over the module's own schema, as a host does.
	reg.Register(config.Module())
	reg.Supply(authntest.ConfigReaderAll(t, "{}", Module()), config.ModuleName)

	product, err := Module().New(context.Background(), reg)
	if err != nil {
		t.Fatalf("Module().New: %v", err)
	}
	localizer, ok := product.(i18n.Localizer)
	if !ok {
		t.Fatalf("the engine does not implement i18n.Localizer: %T", product)
	}
	if got := localizer.Localize(context.Background(), "app.en", nil); got != "Hello" {
		t.Errorf("Localize = %q, want %q", got, "Hello")
	}
}

// TestModuleReadsTheConfiguredDefaultIntoTheEngine holds the configuration-to-
// engine wiring: driven through the real Module() over a reader configured with
// default-locale ja-JP and catalogs that carry ja-JP and en-US, the engine
// resolves a request naming none to ja-JP. New has to carry the decoded
// cfg.DefaultLocale into the engine; a hardcoded en-US would resolve here to
// en-US and fail this case.
func TestModuleReadsTheConfiguredDefaultIntoTheEngine(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "app",
		Resources: []any{
			i18n.Catalog{FS: mapFS(map[string]string{
				"locales/en-US.json": `{"app.en":"Hello"}`,
				"locales/ja-JP.json": `{"app.ja":"こんにちは"}`,
			}), Pattern: "locales/*.json"},
		},
	})
	reg.Register(Module())
	reg.Register(config.Module())
	reg.Supply(authntest.ConfigReaderAll(t, `{"i18n":{"default-locale":"ja-JP"}}`, Module()), config.ModuleName)

	product, err := Module().New(context.Background(), reg)
	if err != nil {
		t.Fatalf("Module().New: %v", err)
	}
	resolver, ok := product.(i18n.Resolver)
	if !ok {
		t.Fatalf("the engine does not implement i18n.Resolver: %T", product)
	}
	if got := resolver.Default(); got != localeJA {
		t.Errorf("Default() = %q, want the configured default %q", got, localeJA)
	}
	if got := resolver.LocaleOf(context.Background()); got != localeJA {
		t.Errorf("LocaleOf(a request naming none) = %q, want %q", got, localeJA)
	}
}

// TestEngineNegotiatesACatalogDefinedThirdLanguage holds that the supported set
// is the set of locales the catalogs carry and not a fixed pair: a catalog in
// three languages negotiates the third — ja-JP, neither English nor Chinese —
// for a request that prefers it, and renders in it. The engine hard-codes no
// language, so a run gains one by declaring a catalog, not by a code change.
func TestEngineNegotiatesACatalogDefinedThirdLanguage(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
		"locales/zh-CN.json": `{"app.zh":"你好"}`,
		"locales/ja-JP.json": `{"app.ja":"こんにちは"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	if locale, ok := e.Negotiate("ja-JP,ja;q=0.9"); !ok || locale != localeJA {
		t.Errorf("Negotiate(\"ja-JP,ja;q=0.9\") = %q, %v; want %q, true", locale, ok, localeJA)
	}
	if got := e.Localize(i18n.WithLocale(context.Background(), localeJA), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize in the third language = %q, want %q", got, "こんにちは")
	}
}

// TestEngineRunsWithANonEnglishConfiguredDefault holds that the default is a
// configured value and not a compile-time constant: over a catalog that carries
// ja-JP, a run configured with default-locale ja-JP resolves a request naming
// none, an account with no stored preference, the Localize fallback and
// Resolver.Default to ja-JP.
func TestEngineRunsWithANonEnglishConfiguredDefault(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
		"locales/ja-JP.json": `{"app.ja":"こんにちは"}`,
	})}
	e := mustEngineWithDefault(t, catalogs, nil, localeJA)

	if got := e.Default(); got != localeJA {
		t.Errorf("Default() = %q, want %q", got, localeJA)
	}
	if got := e.LocaleOf(context.Background()); got != localeJA {
		t.Errorf("LocaleOf(a request naming none) = %q, want %q", got, localeJA)
	}
	if got := e.LocaleFor(context.Background(), "acct"); got != localeJA {
		t.Errorf("LocaleFor(no stored preference) = %q, want %q", got, localeJA)
	}
	// A key only the configured default carries, requested in a locale that
	// misses it, is rendered from the default — here ja-JP, not English.
	if got := e.Localize(i18n.WithLocale(context.Background(), localeEN), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize fallback = %q, want %q", got, "こんにちは")
	}
}

// TestCatalogsMissingTheConfiguredDefaultFailStartup holds that startup refuses a
// set of catalogs that does not carry the configured default: with default-locale
// ja-JP and no ja-JP catalog, the engine stops with ErrNoCatalog naming ja-JP,
// ahead of any request.
func TestCatalogsMissingTheConfiguredDefaultFailStartup(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
	})}
	_, err := newEngine(catalogs, nil, localeJA)
	if !errors.Is(err, ErrNoCatalog) {
		t.Fatalf("newEngine error = %v, want ErrNoCatalog", err)
	}
	if !strings.Contains(err.Error(), string(localeJA)) {
		t.Errorf("the failure %q does not name the configured default %q", err.Error(), string(localeJA))
	}
}

// TestAnUnsetDefaultLocaleResolvesToEnglish holds the unset case through the
// configuration path: a configuration that names no default-locale decodes to
// en-US, the schema's default, and a run on it resolves there.
func TestAnUnsetDefaultLocaleResolvesToEnglish(t *testing.T) {
	cfg, err := readConfig(authntest.ConfigReaderAll(t, "{}", Module()))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.DefaultLocale != localeEN {
		t.Errorf("the unset default resolved to %q, want %q", cfg.DefaultLocale, localeEN)
	}
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
	})}
	e := mustEngineWithDefault(t, catalogs, nil, cfg.DefaultLocale)
	if got := e.Default(); got != localeEN {
		t.Errorf("Default() = %q, want %q", got, localeEN)
	}
}
