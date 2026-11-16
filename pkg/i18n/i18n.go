// Package i18n carries the user-facing text the server renders.
//
// It is the interface package of a capability directory: it declares the
// interfaces, the value types and the resource type, and registers no module.
// The engine that collects the message catalogs, the entry point wiring that
// settles a request's locale and the bridge to an account's stored preferences
// each live in a subpackage and register a module of their own, the way the
// subpackages of pkg/authn do.
//
// The root package depends on the standard library only, so a module that only
// reads a locale can import it without carrying any library the engine binds.
// The one library the engine needs, golang.org/x/text — for language matching,
// plural selection and number formatting — stays inside the engine.
package i18n

import (
	"context"
	"io/fs"
	"time"
)

// Locale is the language a message is rendered in, written as a BCP 47 tag. It
// is a string so that it crosses a module boundary — a context, an interface, a
// declared catalog — without carrying a language-matching library to every
// consumer; the engine parses it where matching happens.
type Locale string

// The names this capability reads its configuration under. The set of locales a
// run supports is the set of locales the declared catalogs and the configured
// catalogs directory carry, and the one
// additional choice — which of them a request that names none resolves to — is
// this configuration item rather than a value written into code.
//
// The default locale is the one configuration item the SMS module also resolves,
// so one statement of the default serves both capabilities. Because that second
// module needs the two names, they are exported here rather than spelled as
// literals on both sides.
const (
	// ConfigNamespace is the config namespace this capability's items hang from.
	ConfigNamespace = "i18n"
	// DefaultLocaleItem is the config item carrying the default locale: the
	// locale a request that names none resolves to, and the locale a message
	// falls back to when the requested locale has no entry for its key. Its value
	// is a BCP 47 language tag, and it is "en-US" when unset.
	DefaultLocaleItem = "default-locale"
)

// Key identifies a message in a catalog. A key is flat and namespaced by the
// module that renders it — "authn.login-failed", "http.404" — so that two
// modules never collide on one and a key read off a problem's Type is
// unambiguous.
type Key string

// Args carries the values a parameterised message interpolates. A message
// writes a parameter as {{name}}, and a name with no value in Args is left in
// the text as written, so a missing parameter is visible rather than silent.
//
// A value is typed: a string is written into the text verbatim, an integer or a
// number is formatted for the message's language by the engine, and a time is
// formatted by the layout the message's own time segment carries and in the
// render context's time zone. The type is a plain value type built with String,
// Int, Float or Time, so this package stays on the standard library and all
// formatting lives in the engine.
type Args map[string]Value

// ValueKind is which type a Value carries.
type ValueKind uint8

const (
	// KindString is text interpolated into a message verbatim.
	KindString ValueKind = iota
	// KindInt is a whole number, formatted for the message's language. A plural
	// segment selects its branch by an integer value.
	KindInt
	// KindFloat is a number with a fractional part, formatted for the message's
	// language.
	KindFloat
	// KindTime is an instant, formatted by the message's own layout and in the
	// render context's time zone.
	KindTime
)

// Value is the typed value a message parameter carries. It is built with
// String, Int, Float or Time; the engine reads it back with the KindOf, StringOf,
// IntOf, FloatOf and TimeOf functions. A message author only builds one.
//
// The zero Value reads as an empty string, the same as String("").
type Value struct {
	kind ValueKind
	text string
	num  int64
	flt  float64
	tm   time.Time
}

// String returns a Value carrying s, interpolated into a message verbatim.
func String(s string) Value { return Value{kind: KindString, text: s} }

// Int returns a Value carrying n, a whole number, formatted for the message's
// language. A plural segment selects its branch by this value.
func Int(n int) Value { return Value{kind: KindInt, num: int64(n)} }

// Float returns a Value carrying f, a number with a fractional part, formatted
// for the message's language.
func Float(f float64) Value { return Value{kind: KindFloat, flt: f} }

// Time returns a Value carrying t, an instant, formatted by the message's own
// layout and in the render context's time zone.
func Time(t time.Time) Value { return Value{kind: KindTime, tm: t} }

// KindOf reports which type a value carries. The zero Value reports KindString.
func KindOf(v Value) ValueKind { return v.kind }

// StringOf reports the text a KindString value carries, and "" for any other
// kind.
func StringOf(v Value) string { return v.text }

// IntOf reports the integer a KindInt value carries, and 0 for any other kind.
func IntOf(v Value) int64 { return v.num }

// FloatOf reports the number a KindFloat value carries, and 0 for any other
// kind.
func FloatOf(v Value) float64 { return v.flt }

// TimeOf reports the instant a KindTime value carries, and the zero time for any
// other kind.
func TimeOf(v Value) time.Time { return v.tm }

// Localizer renders a message in a locale. It is delivered exclusively by the
// engine.
type Localizer interface {
	// Localize renders key in the locale of ctx, falling back to the run's
	// default and then to the key itself, so the result is never empty. A key no
	// catalog carries is written back as the key.
	Localize(ctx context.Context, key Key, args Args) string
	// Translate renders key in the named locale, reporting whether a catalog
	// carried it. It does not fall back.
	Translate(locale Locale, key Key, args Args) (string, bool)
}

// Negotiator picks the supported locale that best fits a request's language
// preferences. It is delivered exclusively by the engine, which holds the set
// of supported locales.
type Negotiator interface {
	// Negotiate returns the supported locale that best matches an Accept-Language
	// header value, and whether any supported locale matched. A header naming
	// only unsupported languages, or none at all, reports no match, which a
	// caller reads as "leave the locale unset" rather than "use the default".
	Negotiate(acceptLanguage string) (Locale, bool)
}

// Resolver answers which locale and time zone apply to a context, and to a
// named account off the request path. It is delivered exclusively by the
// engine.
type Resolver interface {
	// Default is the locale a request that names none resolves to, and the
	// fallback for a key a catalog misses. It is the run's configured default.
	Default() Locale
	// LocaleOf is the locale of ctx: the one written into it, or the value
	// Default reports. It never fails.
	LocaleOf(ctx context.Context) Locale
	// TimeZoneOf is the IANA time zone name of ctx, or "" when none is set, which
	// reads as UTC.
	TimeZoneOf(ctx context.Context) string
	// LocaleFor is the locale for a named account, read from its stored
	// preferences when the context carries none, and the value Default reports
	// otherwise.
	LocaleFor(ctx context.Context, account string) Locale
	// TimeZoneFor is the time zone for a named account, or "" when none is
	// stored.
	TimeZoneFor(ctx context.Context, account string) string
}

// Profile is the seam onto an account's stored language and time zone. It is
// non-exclusive and optional: the engine takes every implementation with
// core.ResolveAll and falls back to the run's default when there is none. The
// bridge to pkg/authn's accounts implements it.
type Profile interface {
	// LocaleFor returns the locale stored for an account, and whether one is. An
	// error reports a preference that could not be read: a caller cannot
	// otherwise tell a failed read from an absent preference. Both take the
	// same fallback, and only the failed read leaves a warning.
	LocaleFor(ctx context.Context, account string) (Locale, bool, error)
	// TimeZoneFor returns the IANA time zone stored for an account, and whether
	// one is.
	TimeZoneFor(ctx context.Context, account string) (string, bool)
}

// Catalog is the resource type a module declares its message files as. The
// engine is the consumer: it reads every declared catalog, over the available
// set, and merges them into one index.
//
// FS is a filesystem — an embed.FS a module reads its own files in with — and
// Pattern selects the message files under it, one per locale, each named for
// the locale it carries, as "locales/{tag}.json".
type Catalog struct {
	FS      fs.FS
	Pattern string
}

// LocaleResolver marks the middleware layer that settles a request's locale, so
// that another layer can be ordered against it with After or Before. It has no
// method a caller uses; a layer stands for it by naming (*LocaleResolver)(nil)
// in its Middleware.Provides.
type LocaleResolver interface{ localeResolver() }
