package i18n

import "context"

// The keys this package stores a locale and a time zone under. They are
// unexported types, so nothing outside can read the values back out by
// constructing the key, and the two cannot be read as one another.
type (
	localeKey struct{}
	zoneKey   struct{}
)

// WithLocale returns a context carrying locale. ctx is unchanged. An empty
// locale is not written: a context with nothing named reads as carrying none,
// which is what FromContext reports and what the resolver falls back from.
//
// Unlike an identity, a locale is a preference and not a fact a decision rests
// on, so the way to write one is exported: a host that starts a job for a known
// account — no request, no header — writes the locale it resolved there. The
// request path writes it from the entry point's chain instead.
func WithLocale(ctx context.Context, locale Locale) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeKey{}, locale)
}

// FromContext returns the locale this context carries, and whether it carries
// one. There is no fallback: a caller that needs one and gets none resolves it
// through the Resolver, which is where the default lives.
func FromContext(ctx context.Context) (Locale, bool) {
	locale, ok := ctx.Value(localeKey{}).(Locale)
	return locale, ok && locale != ""
}

// WithTimeZone returns a context carrying the IANA time zone name. ctx is
// unchanged. An empty name is not written: it reads as UTC, the fallback the
// resolver uses.
func WithTimeZone(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, zoneKey{}, name)
}

// TimeZoneFromContext returns the IANA time zone name this context carries, and
// whether it carries one. A context carrying none reads as UTC.
func TimeZoneFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(zoneKey{}).(string)
	return name, ok && name != ""
}
