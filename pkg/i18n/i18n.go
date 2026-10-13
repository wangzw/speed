// Package i18n carries the user-facing text the server renders.
//
// It is the interface package of a capability directory: it declares the
// interfaces, the value types and the resource type, and registers no module.
// The engine that collects the message catalogs, the entry point wiring that
// settles a request's locale, the responder that renders an error and the
// bridge to an account's stored preferences each live in a subpackage and
// register a module of their own, the way the subpackages of pkg/authn do.
//
// The root package depends on the standard library only, so a module that only
// reads a locale can import it without carrying any library the engine binds.
// The one library the engine needs for language matching, golang.org/x/text,
// stays inside the engine and the entry point wiring.
package i18n

import (
	"context"
	"io/fs"
)

// Locale is the language a message is rendered in, written as a BCP 47 tag. It
// is a string so that it crosses a module boundary — a context, an interface, a
// declared catalog — without carrying a language-matching library to every
// consumer; the engine parses it where matching happens.
type Locale string

// The locales the project ships catalogs for. The set a run supports is the set
// of locales the declared catalogs carry, not this list; these are the tags the
// code writes by name.
const (
	// LocaleEN is English. It is the default.
	LocaleEN Locale = "en-US"
	// LocaleZH is Simplified Chinese.
	LocaleZH Locale = "zh-CN"
)

// Default is the locale a request that names none resolves to, and the locale a
// message falls back to when the requested locale has no entry for its key.
const Default Locale = LocaleEN

// Key identifies a message in a catalog. A key is flat and namespaced by the
// module that renders it — "authn.login-failed", "http.404" — so that two
// modules never collide on one and a key read off a problem's Type is
// unambiguous.
type Key string

// Args carries the values a parameterised message interpolates. A message
// writes a parameter as {{name}}, and a name with no value in Args is left in
// the text as written, so a missing parameter is visible rather than silent.
type Args map[string]string

// Localizer renders a message in a locale. It is delivered exclusively by the
// engine.
type Localizer interface {
	// Localize renders key in the locale of ctx, falling back to Default and
	// then to the key itself, so the result is never empty. A key no catalog
	// carries is written back as the key.
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
	// LocaleOf is the locale of ctx: the one written into it, or Default. It
	// never fails.
	LocaleOf(ctx context.Context) Locale
	// TimeZoneOf is the IANA time zone name of ctx, or "" when none is set, which
	// reads as UTC.
	TimeZoneOf(ctx context.Context) string
	// LocaleFor is the locale for a named account, read from its stored
	// preferences when the context carries none, and Default otherwise.
	LocaleFor(ctx context.Context, account string) Locale
	// TimeZoneFor is the time zone for a named account, or "" when none is
	// stored.
	TimeZoneFor(ctx context.Context, account string) string
}

// Profile is the seam onto an account's stored language and time zone. It is
// non-exclusive and optional: the engine takes every implementation with
// core.ResolveAll and falls back to Default when there is none. The bridge to
// pkg/authn's accounts implements it.
type Profile interface {
	// LocaleFor returns the locale stored for an account, and whether one is.
	LocaleFor(ctx context.Context, account string) (Locale, bool)
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
