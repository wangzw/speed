package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

const (
	localeEN i18n.Locale = "en-US"
	localeZH i18n.Locale = "zh-CN"
	localeJA i18n.Locale = "ja-JP"
	localeDE i18n.Locale = "de-DE"
	localeRU i18n.Locale = "ru-RU"
)

func mapFS(files map[string]string) fstest.MapFS {
	fsys := make(fstest.MapFS, len(files))
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func catalogOf(module string, files map[string]string) core.Resource[i18n.Catalog] {
	return core.Resource[i18n.Catalog]{
		Module: module,
		Value:  i18n.Catalog{FS: mapFS(files), Pattern: "locales/*.json"},
	}
}

var twoLocales = catalogOf("app", map[string]string{
	"locales/en-US.json": `{"app.en":"Hello"}`,
	"locales/zh-CN.json": `{"app.zh":"你好"}`,
})

func mustEngine(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile) *engine {
	t.Helper()
	return mustEngineWithDefault(t, catalogs, profiles, localeEN)
}

func mustEngineWithDefault(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, def i18n.Locale) *engine {
	t.Helper()
	return mustEngineWithOverlay(t, catalogs, profiles, "", def)
}

func mustEngineWithOverlay(t *testing.T, catalogs []core.Resource[i18n.Catalog], profiles []i18n.Profile, dir string, def i18n.Locale) *engine {
	t.Helper()
	e, err := newEngine(catalogs, profiles, def, dir)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	return e
}

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

// TestLocalize checks the locale lookup and the default and key fallbacks.
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

// TestTranslate checks that Translate renders the named locale and does not fall
// back.
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

// TestLocalizeInterpolates checks named placeholder substitution and that a
// missing one is left as written.
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
		{"replaces every named placeholder", i18n.Args{"name": i18n.String("Ada"), "count": i18n.String("3")}, "Hello Ada, you have 3 messages"},
		{"leaves a missing placeholder as written", i18n.Args{"name": i18n.String("Ada")}, "Hello Ada, you have {{count}} messages"},
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

// TestRender checks placeholder interpolation, the missing-parameter rule and a
// message without a placeholder.
func TestRender(t *testing.T) {
	e := mustEngine(t, nil, nil)
	tests := []struct {
		name    string
		message string
		args    i18n.Args
		want    string
	}{
		{"replaces a placeholder", "{{a}}", i18n.Args{"a": i18n.String("x")}, "x"},
		{"replaces a repeated placeholder", "{{a}}{{a}}", i18n.Args{"a": i18n.String("x")}, "xx"},
		{"leaves a name with no value as written", "{{a}}", i18n.Args{"b": i18n.String("x")}, "{{a}}"},
		{"leaves a message without a placeholder unchanged", "plain", i18n.Args{"a": i18n.String("x")}, "plain"},
		{"no args leave the message unchanged", "{{a}}", nil, "{{a}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseMessage(tt.message)
			if err != nil {
				t.Fatalf("parseMessage(%q): %v", tt.message, err)
			}
			if got := e.render(context.Background(), m, tt.args, localeEN); got != tt.want {
				t.Errorf("render(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

// TestMarkedRenderWritesATimeMarker checks that a marked render writes a marker
// for a time segment and strips the delimiters from a string parameter.
func TestMarkedRenderWritesATimeMarker(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.due":"Due {{at, time, 2006-01-02}}; {{name}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	ctx := i18n.WithTimeRender(
		i18n.WithTimeZone(context.Background(), "Asia/Shanghai"),
		i18n.TimeRenderMarked,
	)
	got := e.Localize(ctx, "app.due", i18n.Args{
		"at":   i18n.Time(instant),
		"name": i18n.String("Acme \uE000time:forged\uE001 Ltd"),
	})
	want := "Due " + i18n.MarkedTime("at") + "; Acme time:forged Ltd"
	if got != want {
		t.Errorf("marked render = %q, want %q", got, want)
	}
	if open := strings.Count(got, "\uE000"); open != 1 {
		t.Errorf("marked render = %q carries %d opening delimiters, want the genuine marker's one", got, open)
	}
}

// TestMarkedRenderLeavesTheFormattedModeDefault checks that a context naming no
// mode still formats the instant.
func TestMarkedRenderLeavesTheFormattedModeDefault(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.due":"Due {{at, time, 2006-01-02}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	got := e.Localize(i18n.WithTimeZone(context.Background(), "Asia/Shanghai"), "app.due",
		i18n.Args{"at": i18n.Time(instant)})
	if want := "Due 2026-11-20"; got != want {
		t.Errorf("formatted render = %q, want %q", got, want)
	}
}

// TestTranslateStaysFormatted checks that Translate never writes a marker.
func TestTranslateStaysFormatted(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.due":"Due {{at, time, 2006-01-02}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	got, ok := e.Translate(localeEN, "app.due", i18n.Args{"at": i18n.Time(instant)})
	if !ok {
		t.Fatal("Translate did not find the key")
	}
	if want := "Due 2026-11-20"; got != want {
		t.Errorf("Translate = %q, want %q", got, want)
	}
}

// TestPluralSelectsByCount checks that a plural segment selects one or other by
// the count and formats # for the locale.
func TestPluralSelectsByCount(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.items":"You have {{count, plural, one=# item, other=# items}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	ctx := context.Background()
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"a count of one selects one", 1, "You have 1 item"},
		{"a count of several selects other", 5, "You have 5 items"},
		{"a count of zero selects other", 0, "You have 0 items"},
		{"# is the count grouped for the locale", 1234567, "You have 1,234,567 items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Localize(ctx, "app.items", i18n.Args{"count": i18n.Int(tt.count)}); got != tt.want {
				t.Errorf("Localize(count=%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

// TestPluralSelectsACategoryBeyondOneAndOther checks that the engine uses the
// language's own CLDR categories, not an English one/other split.
func TestPluralSelectsACategoryBeyondOneAndOther(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/ru-RU.json": `{"app.items":"{{count, plural, one=# товар, few=# товара, many=# товаров, other=# товара}}"}`,
	})}
	e := mustEngineWithDefault(t, catalogs, nil, localeRU)
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"one", 1, "1 товар"},
		{"few", 2, "2 товара"},
		{"many", 5, "5 товаров"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Localize(context.Background(), "app.items", i18n.Args{"count": i18n.Int(tt.count)}); got != tt.want {
				t.Errorf("Localize(count=%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

// TestPluralFallsBackToOther checks that a missing category falls back to other.
func TestPluralFallsBackToOther(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/ru-RU.json": `{"app.items":"{{count, plural, one=# item, other=# items}}"}`,
	})}
	e := mustEngineWithDefault(t, catalogs, nil, localeRU)
	if got := e.Localize(context.Background(), "app.items", i18n.Args{"count": i18n.Int(2)}); got != "2 items" {
		t.Errorf("Localize(count=2) = %q, want the other branch %q", got, "2 items")
	}
}

// TestPluralWithNoIntegerValueIsLeftAsWritten checks that a plural segment with a
// missing or non-integer parameter is written back as it stood.
func TestPluralWithNoIntegerValueIsLeftAsWritten(t *testing.T) {
	const raw = "{{count, plural, one=# item, other=# items}}"
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.items":"` + raw + `"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	ctx := context.Background()
	if got := e.Localize(ctx, "app.items", nil); got != raw {
		t.Errorf("Localize(no count) = %q, want %q", got, raw)
	}
	if got := e.Localize(ctx, "app.items", i18n.Args{"count": i18n.String("many")}); got != raw {
		t.Errorf("Localize(a non-integer count) = %q, want %q", got, raw)
	}
}

// TestPluralNegativeCountSelectsByMagnitudeAndPrintsTheSign checks that a negative
// count selects by its magnitude and prints with its sign.
func TestPluralNegativeCountSelectsByMagnitudeAndPrintsTheSign(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.items":"{{count, plural, one=# item, other=# items}}","app.value":"{{value}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	ctx := context.Background()
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"a count of -1 selects one and prints the sign", -1, "-1 item"},
		{"a count of -5 selects other and prints the sign", -5, "-5 items"},
		{"a negative count with many digits groups with the sign", -1234567, "-1,234,567 items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Localize(ctx, "app.items", i18n.Args{"count": i18n.Int(tt.count)}); got != tt.want {
				t.Errorf("Localize(count=%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
	if got := e.Localize(ctx, "app.value", i18n.Args{"value": i18n.Int(-1234567)}); got != "-1,234,567" {
		t.Errorf("Localize(a negative integer) = %q, want %q", got, "-1,234,567")
	}
}

// TestNumberIsFormattedForTheMessageLocale checks that a number is grouped and
// separated for the message's language.
func TestNumberIsFormattedForTheMessageLocale(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.total":"Total: {{total}}"}`,
		"locales/de-DE.json": `{"app.total":"Summe: {{total}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	tests := []struct {
		name   string
		locale i18n.Locale
		value  i18n.Value
		want   string
	}{
		{"an integer groups per en-US", localeEN, i18n.Int(1234567), "Total: 1,234,567"},
		{"the same integer groups per de-DE", localeDE, i18n.Int(1234567), "Summe: 1.234.567"},
		{"a decimal separates per en-US", localeEN, i18n.Float(1234.5), "Total: 1,234.5"},
		{"the same decimal separates per de-DE", localeDE, i18n.Float(1234.5), "Summe: 1.234,5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithLocale(context.Background(), tt.locale)
			if got := e.Localize(ctx, "app.total", i18n.Args{"total": tt.value}); got != tt.want {
				t.Errorf("Localize = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLargeFloatRendersAGroupedDecimalNotAnExponent checks that a float at or
// above 1e6 renders as a grouped decimal.
func TestLargeFloatRendersAGroupedDecimalNotAnExponent(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.total":"Total: {{total}}"}`,
		"locales/de-DE.json": `{"app.total":"Summe: {{total}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	tests := []struct {
		name   string
		locale i18n.Locale
		total  float64
		want   string
	}{
		{"en-US groups a million", localeEN, 1000000, "Total: 1,000,000"},
		{"de-DE groups a million", localeDE, 1000000, "Summe: 1.000.000"},
		{"en-US groups ten million", localeEN, 10000000, "Total: 10,000,000"},
		{"de-DE groups ten million", localeDE, 10000000, "Summe: 10.000.000"},
		{"en-US groups a million and then some", localeEN, 1234567, "Total: 1,234,567"},
		{"de-DE groups a million and then some", localeDE, 1234567, "Summe: 1.234.567"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithLocale(context.Background(), tt.locale)
			if got := e.Localize(ctx, "app.total", i18n.Args{"total": i18n.Float(tt.total)}); got != tt.want {
				t.Errorf("Localize = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTimeFormRendersInTheZoneOfTheContext checks that a time segment formats the
// instant in the zone the context names.
func TestTimeFormRendersInTheZoneOfTheContext(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"At {{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		zone string
		want string
	}{
		{"UTC renders the instant as it stands", "UTC", "At 2026-01-02 15:04:05"},
		{"Asia/Shanghai shifts it eight hours ahead", "Asia/Shanghai", "At 2026-01-02 23:04:05"},
		{"America/New_York shifts it five hours behind", "America/New_York", "At 2026-01-02 10:04:05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithTimeZone(context.Background(), tt.zone)
			if got := e.Localize(ctx, "app.when", i18n.Args{"at": i18n.Time(instant)}); got != tt.want {
				t.Errorf("Localize(zone=%q) = %q, want %q", tt.zone, got, tt.want)
			}
		})
	}
}

// TestTimeFormReadsAnUnsetOrUnloadableZoneAsUTC checks that an unset or
// unloadable zone renders as UTC.
func TestTimeFormReadsAnUnsetOrUnloadableZoneAsUTC(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.FixedZone("Fake/Zone", 8*60*60))
	const want = "2026-01-02 07:04:05"
	if got := e.Localize(context.Background(), "app.when", i18n.Args{"at": i18n.Time(instant)}); got != want {
		t.Errorf("Localize(no zone) = %q, want %q (UTC)", got, want)
	}
	ctx := i18n.WithTimeZone(context.Background(), "Not/AZone")
	if got := e.Localize(ctx, "app.when", i18n.Args{"at": i18n.Time(instant)}); got != want {
		t.Errorf("Localize(an unloadable zone) = %q, want %q (UTC)", got, want)
	}
}

// TestTimeFormReadsTheGoLocalNameAsUTC checks that the engine answers Go's
// "Local" name as UTC on the read path.
func TestTimeFormReadsTheGoLocalNameAsUTC(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	if loc := e.location("Local"); loc != time.UTC {
		t.Errorf("location(%q) = %v, want UTC: the host's own zone must not be reachable from a name in a context",
			"Local", loc)
	}
	instant := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.FixedZone("Fake/Zone", 8*60*60))
	ctx := i18n.WithTimeZone(context.Background(), "Local")
	if got, want := e.Localize(ctx, "app.when", i18n.Args{"at": i18n.Time(instant)}), "2026-01-02 07:04:05"; got != want {
		t.Errorf("Localize(a context naming Local) = %q, want %q (UTC)", got, want)
	}
}

// TestTimeFormLayoutMayContainAComma checks that a comma inside the layout is not
// treated as a separator.
func TestTimeFormLayoutMayContainAComma(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, Mon, 02 Jan 2006 15:04}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, time.January, 2, 15, 4, 0, 0, time.UTC)
	if got := e.Localize(context.Background(), "app.when", i18n.Args{"at": i18n.Time(instant)}); got != "Fri, 02 Jan 2026 15:04" {
		t.Errorf("Localize = %q, want %q", got, "Fri, 02 Jan 2026 15:04")
	}
}

// TestTimeFormWithANonTimeValueIsLeftAsWritten checks that a time segment with a
// missing or non-time parameter is written back as it stood.
func TestTimeFormWithANonTimeValueIsLeftAsWritten(t *testing.T) {
	const raw = "{{at, time, 2006-01-02}}"
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"` + raw + `"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	ctx := context.Background()
	if got := e.Localize(ctx, "app.when", nil); got != raw {
		t.Errorf("Localize(no value) = %q, want %q", got, raw)
	}
	if got := e.Localize(ctx, "app.when", i18n.Args{"at": i18n.String("x")}); got != raw {
		t.Errorf("Localize(a non-time value) = %q, want %q", got, raw)
	}
}

// TestTimeFormWithAnEmptyLayoutFailsStartup checks that a time segment with an
// empty layout fails startup under ErrInvalidMessage naming the key.
func TestTimeFormWithAnEmptyLayoutFailsStartup(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, }}"}`,
	})}
	_, err := newEngine(catalogs, nil, localeEN, "")
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("newEngine error = %v, want ErrInvalidMessage", err)
	}
	if !strings.Contains(err.Error(), "app.when") {
		t.Errorf("the failure %q does not name the key", err.Error())
	}
}

// TestTimeFormRendersConcurrently checks that concurrent time-form renders are
// safe, exercising the zone cache.
func TestTimeFormRendersConcurrently(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	instant := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	zones := []string{"UTC", "Asia/Shanghai", "America/New_York", "Europe/Paris"}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		zone := zones[i%len(zones)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := i18n.WithTimeZone(context.Background(), zone)
			_ = e.Localize(ctx, "app.when", i18n.Args{"at": i18n.Time(instant)})
		}()
	}
	wg.Wait()
}

// TestMalformedMessageFailsStartup checks that each malformed plural segment
// fails startup under ErrInvalidMessage naming the key and the module.
func TestMalformedMessageFailsStartup(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		contains []string
	}{
		{"an unknown category", `{{count, plural, one=# a, lots=# b, other=# c}}`, []string{"lots"}},
		{"no other branch", `{{count, plural, one=# a}}`, []string{"other"}},
		{"a duplicated category", `{{count, plural, one=# a, one=# b, other=# c}}`, []string{"one"}},
		{"a nested placeholder", `{{count, plural, one={{name}} item, other=# items}}`, []string{"nested placeholder"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
				"locales/en-US.json": fmt.Sprintf(`{"app.items":%q}`, tt.message),
			})}
			_, err := newEngine(catalogs, nil, localeEN, "")
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("newEngine error = %v, want ErrInvalidMessage", err)
			}
			if errors.Is(err, ErrInvalidCatalog) {
				t.Errorf("the message failure must be distinct from ErrInvalidCatalog: %v", err)
			}
			if !strings.Contains(err.Error(), "app.items") {
				t.Errorf("the failure %q does not name the key", err.Error())
			}
			if !strings.Contains(err.Error(), `"app"`) {
				t.Errorf("the failure %q does not name the module", err.Error())
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the failure %q does not name %q", err.Error(), want)
				}
			}
		})
	}
}

// TestMalformedOverlayMessageFailsStartup checks that a malformed overlay message
// fails startup naming the key and the directory.
func TestMalformedOverlayMessageFailsStartup(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.en":"Hello"}`,
	})}
	dir := overlayDir(t, map[string]string{
		"en-US.json": `{"app.items":"{{count, plural, one=# a}}"}`,
	})
	_, err := newEngine(catalogs, nil, localeEN, dir)
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("newEngine error = %v, want ErrInvalidMessage", err)
	}
	if !strings.Contains(err.Error(), "app.items") {
		t.Errorf("the failure %q does not name the key", err.Error())
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the failure %q does not name the directory %q", err.Error(), dir)
	}
}

// TestNegotiateNoMatch checks that an empty header, unsupported languages and a
// zero-q entry all report no match.
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

// TestNegotiateQValueOrdering checks that q-value ordering is stable across
// engine builds.
func TestNegotiateQValueOrdering(t *testing.T) {
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

// TestLoadErrors checks the startup failures of a declared catalog and that each
// names what it must.
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

// TestLoadMultiLocaleCatalog checks that one module carrying one key in several
// locales loads.
func TestLoadMultiLocaleCatalog(t *testing.T) {
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

type stubProfile struct {
	locale i18n.Locale
	zone   string
}

func (p stubProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return p.locale, p.locale != "", nil
}

func (p stubProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return p.zone, p.zone != "", nil
}

type failingProfile struct {
	err error
}

func (p failingProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, p.readErr()
}

func (p failingProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return "", false, p.readErr()
}

func (p failingProfile) readErr() error {
	if p.err != nil {
		return p.err
	}
	return errors.New("the account store is unavailable")
}

// TestLocaleFor checks the context, profile, default and failed-read answers.
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
		{"the default when a profile's read fails", context.Background(), []i18n.Profile{failingProfile{}}, localeEN},
		{"a later profile answers after an earlier one fails", context.Background(), []i18n.Profile{failingProfile{}, profile}, localeZH},
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

// TestTimeZoneFor checks the context, profile and no-preference answers.
func TestTimeZoneFor(t *testing.T) {
	profile := stubProfile{locale: localeZH, zone: "Asia/Shanghai"}
	tests := []struct {
		name     string
		ctx      context.Context
		profiles []i18n.Profile
		want     string
		wantOK   bool
	}{
		{"the context wins over a profile", i18n.WithTimeZone(context.Background(), "UTC"), []i18n.Profile{profile}, "UTC", true},
		{"the profile is used when the context names none", context.Background(), []i18n.Profile{profile}, "Asia/Shanghai", true},
		{"empty when there is no profile", context.Background(), nil, "", false},
		{"empty when no profile carries one", context.Background(), []i18n.Profile{stubProfile{}}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mustEngine(t, nil, tt.profiles)
			got, ok, err := e.TimeZoneFor(tt.ctx, "acct")
			if err != nil {
				t.Fatalf("TimeZoneFor ended with %v, want no error", err)
			}
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("TimeZoneFor = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestTimeZoneForReportsAProfileReadFailure checks that a profile failure is
// reported and stops the walk.
func TestTimeZoneForReportsAProfileReadFailure(t *testing.T) {
	readErr := errors.New("the account store is unreachable")
	e := mustEngine(t, nil, []i18n.Profile{
		failingProfile{err: readErr},
		stubProfile{zone: "Asia/Shanghai"},
	})
	got, ok, err := e.TimeZoneFor(context.Background(), "acct")
	if !errors.Is(err, readErr) {
		t.Fatalf("TimeZoneFor ended with %v, want the profile's own failure", err)
	}
	if got != "" || ok {
		t.Errorf("TimeZoneFor = (%q, %v), want no value beside the failure", got, ok)
	}
}

// TestTimeZoneForPrefersTheContextOverAFailingProfile checks that a context zone
// is returned without reading a profile.
func TestTimeZoneForPrefersTheContextOverAFailingProfile(t *testing.T) {
	e := mustEngine(t, nil, []i18n.Profile{failingProfile{err: errors.New("unreachable")}})
	got, ok, err := e.TimeZoneFor(i18n.WithTimeZone(context.Background(), "UTC"), "acct")
	if err != nil || got != "UTC" || !ok {
		t.Fatalf("TimeZoneFor = (%q, %v, %v), want (UTC, true, nil)", got, ok, err)
	}
}

// TestModuleCollectsDeclaredCatalogs checks that the module collects a declared
// catalog through the real Module.
func TestModuleCollectsDeclaredCatalogs(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "app",
		Resources: []any{
			i18n.Catalog{FS: mapFS(map[string]string{"locales/en-US.json": `{"app.en":"Hello"}`}), Pattern: "locales/*.json"},
		},
	})
	reg.Register(Module())
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

// TestModuleReadsTheConfiguredDefaultIntoTheEngine checks that New carries the
// configured default locale into the engine.
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

// TestModuleReadsTheConfiguredOverlayIntoTheEngine checks that New carries the
// configured catalogs directory into the engine.
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

// TestEngineNegotiatesACatalogDefinedThirdLanguage checks that a third language a
// catalog defines is negotiated and rendered.
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

// TestEngineRunsWithANonEnglishConfiguredDefault checks that the configured
// default drives every resolution path.
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
	if got := e.Localize(i18n.WithLocale(context.Background(), localeEN), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize fallback = %q, want %q", got, "こんにちは")
	}
}

// TestCatalogsMissingTheConfiguredDefaultFailStartup checks that a catalog set
// without the configured default fails under ErrNoCatalog naming it.
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

// TestAnUnsetDefaultLocaleResolvesToEnglish checks that an unset default decodes
// to en-US and resolves there.
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

// TestOverlayOverridesADeclaredMessage checks that an overlay message wins over
// the declared one in both render paths.
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

// TestOverlayIntroducesANewLocale checks that a locale only the overlay carries
// joins the supported set.
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

// TestOverlaySuppliesTheConfiguredDefault checks that the default check runs
// after the overlay merge, so the overlay can supply the default locale.
func TestOverlaySuppliesTheConfiguredDefault(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/zh-CN.json": `{"app.zh":"你好"}`,
	})}
	dir := overlayDir(t, map[string]string{
		"ja-JP.json": `{"app.ja":"こんにちは"}`,
	})
	e := mustEngineWithOverlay(t, catalogs, nil, dir, localeJA)
	if got := e.Default(); got != localeJA {
		t.Errorf("Default() = %q, want the configured default %q", got, localeJA)
	}
	if got, ok := e.Translate(localeJA, "app.ja", nil); !ok || got != "こんにちは" {
		t.Errorf("Translate in the overlay-supplied default = %q, %v; want %q, true", got, ok, "こんにちは")
	}
	if got := e.Localize(i18n.WithLocale(context.Background(), localeZH), "app.ja", nil); got != "こんにちは" {
		t.Errorf("Localize fallback to the overlay-supplied default = %q, want %q", got, "こんにちは")
	}
}

// TestOverlayMalformedFileFailsStartup checks that a malformed overlay file fails
// under ErrInvalidOverlay naming the file.
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

// TestNoOverlayLeavesBehaviourAsBefore checks that every way of naming no overlay
// leaves the engine built from the declared catalogs alone.
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
			if _, ok := e.Translate(localeJA, "app.ja", nil); ok {
				t.Error("an overlay-less run carries a locale no declared catalog does")
			}
			if locale, ok := e.Negotiate("ja-JP,ja;q=0.9"); ok {
				t.Errorf("Negotiate matched %q with no overlay", locale)
			}
		})
	}
}
