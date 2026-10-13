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

// The tags these cases write by name. The engine no longer exports them, so the
// test names them itself.
const (
	localeEN i18n.Locale = "en-US"
	localeZH i18n.Locale = "zh-CN"
	localeJA i18n.Locale = "ja-JP"
	localeDE i18n.Locale = "de-DE"
	localeRU i18n.Locale = "ru-RU"
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

// TestRender holds the pre-existing placeholder contract at the renderer: a
// name with a value is interpolated, a name with none is left as written, and a
// message with no placeholder is returned unchanged. It is the guard on the rule
// that a missing parameter is visible rather than silent.
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

// TestPluralSelectsByCount holds that a plural segment selects its branch by the
// count's CLDR category: an English message renders "one" for a count of one and
// "other" for any other count, and # in the branch is the count, grouped for the
// locale rather than written raw.
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

// TestPluralSelectsACategoryBeyondOneAndOther holds that the engine uses the
// language's own CLDR rules and not an English-shaped one/other split: a Russian
// message that carries one, few, many and other renders "few" for a count of
// two and "many" for a count of five.
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

// TestPluralFallsBackToOther holds that a count whose language yields a category
// the message does not carry falls back to the other branch: a Russian message
// that carries only one and other renders the other branch for a count of two,
// which is "few" in Russian.
func TestPluralFallsBackToOther(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/ru-RU.json": `{"app.items":"{{count, plural, one=# item, other=# items}}"}`,
	})}
	e := mustEngineWithDefault(t, catalogs, nil, localeRU)
	if got := e.Localize(context.Background(), "app.items", i18n.Args{"count": i18n.Int(2)}); got != "2 items" {
		t.Errorf("Localize(count=2) = %q, want the other branch %q", got, "2 items")
	}
}

// TestPluralWithNoIntegerValueIsLeftAsWritten holds that the plural rule follows
// the missing-parameter rule: a plural segment whose parameter is absent, or
// present but not an integer, is written back as it stood rather than rendering
// a branch.
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

// TestPluralNegativeCountSelectsByMagnitudeAndPrintsTheSign holds that a
// negative count is legal input: CLDR defines the operand as the absolute value
// of the source number, so -1 selects the "one" branch as 1 does while the
// printed count keeps its sign — in the # token and in a plain interpolation
// alike. Before the fix the signed count reached the rule engine, whose
// inclusion-mask index n%modulus is negative in Go, panicking the request path.
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
	// A negative integer interpolated without a plural segment keeps its sign too.
	if got := e.Localize(ctx, "app.value", i18n.Args{"value": i18n.Int(-1234567)}); got != "-1,234,567" {
		t.Errorf("Localize(a negative integer) = %q, want %q", got, "-1,234,567")
	}
}

// TestNumberIsFormattedForTheMessageLocale holds that an integer or a number
// interpolated without a plural segment is formatted for the message's language
// and not by fmt.Sprintf: the same value groups as 1,234,567 in en-US and
// 1.234.567 in de-DE, and a decimal separates as 1,234.5 and 1.234,5, so the
// same template in two locales renders differently.
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

// TestLargeFloatRendersAGroupedDecimalNotAnExponent holds that a whole-valued
// float at or above 1e6 is rendered as a grouped decimal and not in scientific
// notation: the message.Printer's %v verb switches to "1×10⁰⁶" (en-US) and
// "1·10⁰⁶" (de-DE) at 1e6, contradicting the criterion that a number is grouped
// per the locale. number.Decimal keeps it decimal, asserted in two locales.
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

// TestTimeFormRendersInTheZoneOfTheContext holds that a {{name, time, layout}}
// segment formats the instant with the layout the message carries, in the zone
// the context names. The same instant read under two zone names renders
// differently, so the zone is really applied and not ignored.
func TestTimeFormRendersInTheZoneOfTheContext(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"At {{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	// One instant; its wall clock differs by the zone it is shown in.
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

// TestTimeFormReadsAnUnsetOrUnloadableZoneAsUTC holds the two fallbacks: a
// context that names no zone, and a zone name this process cannot load, both
// render the instant as UTC rather than failing the render. The fixture instant
// is built in a fixed +08:00 zone, so its wall clock there (15:04:05) differs
// from its UTC wall clock (07:04:05); asserting the UTC clock is what fails if
// the zone conversion is dropped — a fixture already in UTC would render the
// same either way and would not discriminate the fallback from a no-op.
func TestTimeFormReadsAnUnsetOrUnloadableZoneAsUTC(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	// 2026-01-02 15:04:05 in +08:00 is 2026-01-02 07:04:05 in UTC.
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

// TestTimeFormReadsTheGoLocalNameAsUTC holds the read path's own guard. "Local"
// is one of Go's special location names: time.LoadLocation resolves it to the
// host's own zone, so a context carrying "Local" would render user-visible text
// in whatever zone the process happens to run in. The write side refuses the
// name as a stored preference, but the accessor that writes a zone into a
// context is exported and takes a value unexamined, so the engine answers UTC
// rather than resolving it.
//
// The engine's own answer is asserted first because it does not depend on the
// host: a host that runs in UTC would render the same text either way, and the
// render assertion alone would not discriminate a leak from the guard.
func TestTimeFormReadsTheGoLocalNameAsUTC(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, 2006-01-02 15:04:05}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	if loc := e.location("Local"); loc != time.UTC {
		t.Errorf("location(%q) = %v, want UTC: the host's own zone must not be reachable from a name in a context",
			"Local", loc)
	}
	// 2026-01-02 15:04:05 in +08:00 is 2026-01-02 07:04:05 in UTC.
	instant := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.FixedZone("Fake/Zone", 8*60*60))
	ctx := i18n.WithTimeZone(context.Background(), "Local")
	if got, want := e.Localize(ctx, "app.when", i18n.Args{"at": i18n.Time(instant)}), "2026-01-02 07:04:05"; got != want {
		t.Errorf("Localize(a context naming Local) = %q, want %q (UTC)", got, want)
	}
}

// TestTimeFormLayoutMayContainAComma holds that the segment is not split on a
// comma inside its layout: a Go layout like "Mon, 02 Jan 2006 15:04" is taken
// whole after the second comma, so the weekday and the day-of-month both render.
func TestTimeFormLayoutMayContainAComma(t *testing.T) {
	catalogs := []core.Resource[i18n.Catalog]{catalogOf("app", map[string]string{
		"locales/en-US.json": `{"app.when":"{{at, time, Mon, 02 Jan 2006 15:04}}"}`,
	})}
	e := mustEngine(t, catalogs, nil)
	// 2 January 2026 is a Friday.
	instant := time.Date(2026, time.January, 2, 15, 4, 0, 0, time.UTC)
	if got := e.Localize(context.Background(), "app.when", i18n.Args{"at": i18n.Time(instant)}); got != "Fri, 02 Jan 2026 15:04" {
		t.Errorf("Localize = %q, want %q", got, "Fri, 02 Jan 2026 15:04")
	}
}

// TestTimeFormWithANonTimeValueIsLeftAsWritten holds that the time rule follows
// the missing-parameter rule: a segment whose parameter is absent, or present
// but not a time, is written back as it stood rather than rendered.
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

// TestTimeFormWithAnEmptyLayoutFailsStartup holds that a time segment carrying no
// layout is a malformed message: startup terminates under ErrInvalidMessage and
// the failure names the key it came from.
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

// TestTimeFormRendersConcurrently holds that the zone cache is safe under
// concurrent renders: many goroutines render a time form naming different zones
// at once, so the guard on the cache is exercised. Run under -race it is the
// check that the one field written after construction is written behind a lock.
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

// TestMalformedMessageFailsStartup holds that a message the engine cannot parse
// terminates startup under ErrInvalidMessage — a sentinel distinct from the
// source failures — naming the key and the module it came from. The three ways a
// plural segment is malformed are a category that is not a CLDR category, a
// missing "other" branch, and a repeated category.
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

// TestMalformedOverlayMessageFailsStartup holds that a malformed message in the
// configured catalogs directory fails startup under ErrInvalidMessage naming the
// key and the directory, so an operator can find the file.
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
