package i18n

import "context"

// The keys this package stores a locale and a time zone under. They are
// unexported types, so nothing outside can read the values back out by
// constructing the key, and the two cannot be read as one another.
type (
	localeKey struct{}
	zoneKey   struct{}
	renderKey struct{}
)

// TimeRender is how a time segment is written by a render. It is a context
// value rather than an argument on every call because it is a property of who
// will read the text: a server that hands a title to a client writes a marker
// for the client to resolve in the viewer's display zone, while a text the
// server sends as it stands — an email, a log line, a translated string — is
// formatted now.
type TimeRender uint8

const (
	// TimeRenderFormatted writes a time segment as the formatted instant, in the
	// zone the context carries. It is the zero value, so every caller that names
	// no mode keeps the behaviour it had.
	TimeRenderFormatted TimeRender = iota
	// TimeRenderMarked writes a time segment as a MarkedTime marker carrying the
	// parameter name, without reading the value or the zone. The reader resolves
	// it, so one stored message renders in whatever zone and language the viewer
	// has now.
	TimeRenderMarked
)

// WithTimeRender returns a context carrying the render mode. ctx is unchanged,
// and the formatted mode is not written: a context with nothing named reads as
// TimeRenderFormatted.
func WithTimeRender(ctx context.Context, mode TimeRender) context.Context {
	if mode == TimeRenderFormatted {
		return ctx
	}
	return context.WithValue(ctx, renderKey{}, mode)
}

// TimeRenderFromContext returns the mode this context carries, or
// TimeRenderFormatted when it names none.
func TimeRenderFromContext(ctx context.Context) TimeRender {
	mode, _ := ctx.Value(renderKey{}).(TimeRender)
	return mode
}

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
