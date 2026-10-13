package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	return mustEngineWithOverlay(t, catalogs, profiles, "", def)
}

// mustEngineWithOverlay builds the engine over a named overlay directory and a
// named configured default, or fails the test. An empty directory is the unset
// configuration.
func mustEngineWithOverlay(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, dir string, def i18n.Locale) *engine {
	t.Helper()
	e, err := newEngine(catalogs, profiles, def, dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	return e
}

// overlayDir writes one {tag}.json file per entry under a fresh temporary
// directory and returns the directory, standing in for a configured
// catalogs-dir on disk.
func overlayDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
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
			_, err := newEngine(tt.catalogs, nil, localeEN, "")
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
	if _, err := newEngine(catalogs, nil, localeEN, ""); err != nil {
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

// TestModuleReadsTheConfiguredOverlayIntoTheEngine holds the configuration-to-
// engine wiring for the overlay: driven through the real Module() over a reader
// carrying catalogs-dir and a default-locale that only the overlay supplies, the
// engine renders the overlay's message. New has to carry the decoded
// cfg.CatalogsDir into the engine; dropping it would leave the declared catalogs
// alone and fail here.
func TestModuleReadsTheConfiguredOverlayIntoTheEngine(t *testing.T) {
	dir := overlayDir(t, map[string]string{"ja-JP.json": `{"app.ja":"こんにちは"}`})
	reg := core.New()
	reg.Register(core.Module{
		Name: "app",
		Resources: []any{
			i18n.Catalog{FS: mapFS(map[string]string{"locales/en-US.json": `{"app.en":"Hello"}`}), Pattern: "locales/*.json"},
		},
	})
	reg.Register(Module())
	reg.Register(config.Module())
	body := fmt.Sprintf(`{"i18n":{"default-locale":"ja-JP","catalogs-dir":%q}}`, dir)
	reg.Supply(authntest.ConfigReaderAll(t, body, Module()), config.ModuleName)

	product, err := Module().New(context.Background(), reg)
	if err != nil {
		t.Fatalf("Module().New: %v", err)
	}
	resolver, ok := product.(i18n.Resolver)
	if !ok {
		t.Fatalf("the engine does not implement i18n.Resolver: %T", product)
	}
	if got := resolver.Default(); got != localeJA {
		t.Errorf("Default() = %q, want the overlay-supplied default %q", got, localeJA)
	}
	localizer, ok := product.(i18n.Localizer)
	if !ok {
		t.Fatalf("the engine does not implement i18n.Localizer: %T", product)
	}
	if got := localizer.Localize(context.Background(), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize = %q, want the overlay's message", got)
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
	_, err := newEngine(catalogs, nil, localeJA, "")
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

// TestOverlayOverridesADeclaredMessage holds that a message the overlay directory
// carries wins over the same key a declared catalog carries: the index is built
// from the declared catalogs and then the overlay, and the overlay's entry is
// written last. It covers both render paths — Localize (with fallback) and
// Translate (without) — so the override is in the index, not in the fallback.
func TestOverlayOverridesADeclaredMessage(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.greet":"Hello from the binary"}`,
	})}
	dir := overlayDir(t, map[string]string{
		"en-US.json": `{"app.greet":"Hello from the directory"}`,
	})
	e := mustEngineWithOverlay(t, catalogs, nil, dir, localeEN)
	if got := e.Localize(context.Background(), "app.greet", nil); got != "Hello from the directory" {
		t.Errorf("Localize = %q, want the overlay's message", got)
	}
	if got, ok := e.Translate(localeEN, "app.greet", nil); !ok || got != "Hello from the directory" {
		t.Errorf("Translate = %q, %v; want the overlay's message, true", got, ok)
	}
}

// TestOverlayIntroducesANewLocale holds that a locale only the overlay carries
// enters the engine's supported set: a request that prefers it negotiates to it
// and renders in it, though no declared catalog names it. The engine supports
// whatever the merged index carries, not a fixed pair.
func TestOverlayIntroducesANewLocale(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
	})}
	dir := overlayDir(t, map[string]string{
		"ja-JP.json": `{"app.ja":"こんにちは"}`,
	})
	e := mustEngineWithOverlay(t, catalogs, nil, dir, localeEN)
	if locale, ok := e.Negotiate("ja-JP,ja;q=0.9"); !ok || locale != localeJA {
		t.Errorf("Negotiate(\"ja-JP,ja;q=0.9\") = %q, %v; want %q, true", locale, ok, localeJA)
	}
	if got := e.Localize(i18n.WithLocale(context.Background(), localeJA), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize in the overlay's locale = %q, want %q", got, "こんにちは")
	}
}

// TestOverlaySuppliesTheConfiguredDefault holds that the default-locale check
// runs after the merge: a declared catalog carries only zh-CN while the
// configured default is ja-JP, which only the overlay carries. Because the
// overlay is merged first, the run starts instead of failing with ErrNoCatalog,
// and the overlay's locale is the fallback every message relies on.
func TestOverlaySuppliesTheConfiguredDefault(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/zh-CN.json": `{"app.zh":"你好"}`,
	})}
	dir := overlayDir(t, map[string]string{
		"ja-JP.json": `{"app.ja":"こんにちは"}`,
	})
	// newEngine builds and does not fail, where a set that carries no ja-JP would
	// stop with ErrNoCatalog.
	e := mustEngineWithOverlay(t, catalogs, nil, dir, localeJA)
	if got := e.Default(); got != localeJA {
		t.Errorf("Default() = %q, want the configured default %q", got, localeJA)
	}
	if got, ok := e.Translate(localeJA, "app.ja", nil); !ok || got != "こんにちは" {
		t.Errorf("Translate in the overlay-supplied default = %q, %v; want %q, true", got, ok, "こんにちは")
	}
	// A key the requested locale misses falls back to the overlay-supplied
	// default, so the overlay is a real fallback target, not only an added locale.
	if got := e.Localize(i18n.WithLocale(context.Background(), localeZH), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize fallback to the overlay-supplied default = %q, want %q", got, "こんにちは")
	}
}

// TestOverlayMalformedFileFailsStartup holds that a file in the configured
// directory the loader cannot read as a catalog fails startup under
// ErrInvalidOverlay — a sentinel distinct from ErrInvalidCatalog, and naming the
// directory and the file. The two ways a file is malformed are a body that is
// not a JSON object of string to string and a base name that is not a language
// tag.
func TestOverlayMalformedFileFailsStartup(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
	})}
	tests := []struct {
		name string
		file string
		body string
	}{
		{"a body that is not a JSON object", "zh-CN.json", `{not json}`},
		{"a base name that is not a language tag", "1234.json", `{"app.x":"y"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := overlayDir(t, map[string]string{tt.file: tt.body})
			_, err := newEngine(catalogs, nil, localeEN, dir)
			if !errors.Is(err, ErrInvalidOverlay) {
				t.Fatalf("newEngine error = %v, want ErrInvalidOverlay", err)
			}
			if errors.Is(err, ErrInvalidCatalog) {
				t.Errorf("the overlay failure must be distinct from ErrInvalidCatalog: %v", err)
			}
			if !strings.Contains(err.Error(), tt.file) {
				t.Errorf("the failure %q does not name the file %q", err.Error(), tt.file)
			}
		})
	}
}

// TestNoOverlayLeavesBehaviourAsBefore holds that every way of naming no overlay
// — the item unset, a path that is not an existing directory, an existing
// directory holding no *.json file, a path naming a file — leaves the engine
// built from the declared catalogs alone. In particular the loader's "matched no
// file" error, an error for a declared catalog, must not surface for the overlay
// source.
func TestNoOverlayLeavesBehaviourAsBefore(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{twoLocales}
	empty := t.TempDir()
	noJSON := overlayDir(t, map[string]string{"readme.txt": "not a catalog"})
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing the file case: %v", err)
	}
	tests := []struct {
		name string
		dir  string
	}{
		{"the item is unset", ""},
		{"a path that is not an existing directory", filepath.Join(t.TempDir(), "missing")},
		{"an existing but empty directory", empty},
		{"a directory holding no *.json file", noJSON},
		{"a path naming a file, not a directory", file},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngineWithOverlay(t, catalogs, nil, tt.dir, localeEN)
			if got := e.Localize(i18n.WithLocale(context.Background(), localeZH), "app.zh", nil); got != "你好" {
				t.Errorf("Localize = %q, want %q", got, "你好")
			}
			if got := e.Default(); got != localeEN {
				t.Errorf("Default() = %q, want %q", got, localeEN)
			}
			// No overlay adds a locale the declared catalogs do not carry.
			if _, ok := e.Translate(localeJA, "app.ja", nil); ok {
				t.Error("an overlay-less run carries a locale no declared catalog does")
			}
			if locale, ok := e.Negotiate("ja-JP,ja;q=0.9"); ok {
				t.Errorf("Negotiate matched %q with no overlay", locale)
			}
		})
	}
}
