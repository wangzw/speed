// Package i18n carries the user-facing text the server renders: the
// interfaces, value types and resource type a module uses to declare a catalog
// and render a message. It registers no module and depends on the standard
// library alone.
package i18n

import (
	"context"
	"io/fs"
	"strings"
	"time"
)

// Locale is the language a message is rendered in, written as a BCP 47 tag.
type Locale string

const (
	// ConfigNamespace is the config namespace this capability's items hang from.
	ConfigNamespace = "i18n"
	// DefaultLocaleItem is the config item carrying the default locale: the
	// locale a request that names none resolves to, and the fallback for a key a
	// catalog misses. Its value is a BCP 47 language tag, and it is "en-US" when
	// unset.
	DefaultLocaleItem = "default-locale"
)

// Key identifies a message in a catalog. It is flat and namespaced by the module
// that renders it.
type Key string

// Args carries the values a parameterised message interpolates. A name with no
// value is left in the text as written. A value is typed: a string is written
// verbatim, a number is formatted for the message's language, and a time is
// formatted by the layout of the message's own time segment.
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

// Value is the typed value a message parameter carries, built with String, Int,
// Float or Time. The zero Value reads as an empty string.
type Value struct {
	kind ValueKind
	text string
	num  int64
	flt  float64
	tm   time.Time
}

const (
	timeMarkerOpen  = "\uE000"
	timeMarkerClose = "\uE001"
)

// MarkedTime is the marker a TimeRenderMarked render writes in place of a time
// segment: the opening delimiter, "time:", the parameter name, and the closing
// delimiter. It carries no instant, layout or zone, so a client resolves it in
// the viewer's display zone at read time.
func MarkedTime(name string) string {
	return timeMarkerOpen + "time:" + name + timeMarkerClose
}

// StripTimeMarkerDelimiters removes the two private-use characters a marker is
// delimited with.
func StripTimeMarkerDelimiters(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\uE000' || r == '\uE001' {
			return -1
		}
		return r
	}, s)
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
	// default and then to the key itself, so the result is never empty.
	Localize(ctx context.Context, key Key, args Args) string
	// Translate renders key in the named locale, reporting whether a catalog
	// carried it. It does not fall back.
	Translate(locale Locale, key Key, args Args) (string, bool)
}

// Negotiator picks the supported locale that best fits a request's language
// preferences. It is delivered exclusively by the engine.
type Negotiator interface {
	// Negotiate returns the supported locale that best matches an Accept-Language
	// header value, and whether any supported locale matched. No match means the
	// caller leaves the locale unset rather than using the default.
	Negotiate(acceptLanguage string) (Locale, bool)
}

// Resolver answers which locale and time zone apply to a context, and to a
// named account off the request path. It is delivered exclusively by the
// engine.
type Resolver interface {
	// Default is the locale a request that names none resolves to, and the
	// fallback for a key a catalog misses.
	Default() Locale
	// LocaleOf is the locale of ctx: the one written into it, or Default. It
	// never fails.
	LocaleOf(ctx context.Context) Locale
	// TimeZoneOf is the IANA time zone name of ctx, or "" when none is set, which
	// reads as UTC.
	TimeZoneOf(ctx context.Context) string
	// LocaleFor is the locale for a named account, read from its stored
	// preferences when the context carries none, and Default otherwise.
	LocaleFor(ctx context.Context, account string) Locale
	// TimeZoneFor is the time zone for a named account: the context's zone when
	// it carries one, otherwise the account's stored preference. It reports
	// whether a zone was found and, when a profile could not be read, the
	// failure; an unset zone and an unreadable one lead to different behaviour at
	// the caller.
	TimeZoneFor(ctx context.Context, account string) (string, bool, error)
}

// Profile is the seam onto an account's stored language and time zone. It is
// non-exclusive and optional.
type Profile interface {
	// LocaleFor returns the locale stored for an account, and whether one is. An
	// error reports a preference that could not be read, which a caller must tell
	// from an absent preference.
	LocaleFor(ctx context.Context, account string) (Locale, bool, error)
	// TimeZoneFor returns the IANA time zone stored for an account, whether one
	// is, and the failure that kept the store from answering.
	TimeZoneFor(ctx context.Context, account string) (string, bool, error)
}

// Catalog is the resource type a module declares its message files as. FS is the
// filesystem the module reads its own files in, and Pattern selects the message
// files under it, one per locale, as "locales/{tag}.json".
type Catalog struct {
	// FS is the filesystem the module reads its own message files from.
	FS fs.FS
	// Pattern selects the message files under FS, one per locale.
	Pattern string
}

// LocaleResolver marks the middleware layer that settles a request's locale, so
// that another layer can be ordered against it with After or Before. It has no
// method a caller uses.
type LocaleResolver interface{ localeResolver() }
