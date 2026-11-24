package i18n

import "context"

// The unexported key types keep nothing outside from reading these values back
// out by constructing a key.
type (
	localeKey struct{}
	zoneKey   struct{}
	renderKey struct{}
)

// TimeRender is how a time segment is written by a render. It is a context value
// because it is a property of who will read the text.
type TimeRender uint8

const (
	// TimeRenderFormatted writes a time segment as the formatted instant, in the
	// zone the context carries. It is the zero value.
	TimeRenderFormatted TimeRender = iota
	// TimeRenderMarked writes a time segment as a MarkedTime marker carrying the
	// parameter name, without reading the value or the zone.
	TimeRenderMarked
)

// WithTimeRender returns a context carrying the render mode. The formatted mode
// is not written.
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

// WithLocale returns a context carrying locale. An empty locale is not written.
func WithLocale(ctx context.Context, locale Locale) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeKey{}, locale)
}

// FromContext returns the locale this context carries, and whether it carries
// one. There is no fallback.
func FromContext(ctx context.Context) (Locale, bool) {
	locale, ok := ctx.Value(localeKey{}).(Locale)
	return locale, ok && locale != ""
}

// WithTimeZone returns a context carrying the IANA time zone name. An empty name
// is not written.
func WithTimeZone(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, zoneKey{}, name)
}

// TimeZoneFromContext returns the IANA time zone name this context carries, and
// whether it carries one.
func TimeZoneFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(zoneKey{}).(string)
	return name, ok && name != ""
}
