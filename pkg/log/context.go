package log

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

// contextKey is this package's key in a context. It is an unexported type, so
// nothing outside can collide with it or read the logger back out by
// constructing the key.
type contextKey struct{}

// binding is what a context carries under contextKey: the logger injected into
// it, the attributes bound on top of that one through WithAttrs, and the logger
// the two make.
//
// The attributes are kept beside the logger, rather than only inside it,
// because a bound attribute cannot be taken back out of a slog.Logger: With
// appends, and the handler has already preformatted what it was given. Keeping
// them is what lets a later binding of a key replace the earlier one, and lets
// a producer take its own field off again, by deriving the logger afresh from
// the injected one.
type binding struct {
	// base is the logger WithLogger injected. It is nil on a context no
	// logger was injected into, which is the default logger.
	base *slog.Logger
	// attrs are the attributes bound through WithAttrs, at most one per key,
	// in the order their keys were first bound.
	attrs []slog.Attr
	// logger is base with attrs bound, derived once per binding.
	logger *slog.Logger
}

// root is the logger the attributes are bound on: the injected one, or the
// default logger where nothing was injected.
func (b *binding) root() *slog.Logger {
	if b == nil || b.base == nil {
		return Default()
	}
	return b.base
}

// bindingOf returns what the context carries under this package's key, or nil.
func bindingOf(ctx context.Context) *binding {
	b, _ := ctx.Value(contextKey{}).(*binding)
	return b
}

// FromContext returns the logger carried by ctx, or the process default logger
// when it carries none.
//
// The fallback neither panics nor returns a logger that drops records: a log
// call has to hold on any context, and the record still passes the redaction
// layer. What it does not do is reach the configured destinations — the
// default logger writes to standard output, and is filtered by the bootstrap
// level rather than by the level this run was configured with.
//
// A missing injection therefore shows up as the whole path's logs appearing on
// standard output instead of in the configured file. That is deliberate: it is
// visible without producing a failure signal, and it is fixed by injecting a
// logger where the context is created, not by widening the fallback.
func FromContext(ctx context.Context) *slog.Logger {
	if b := bindingOf(ctx); b != nil && b.logger != nil {
		return b.logger
	}
	return Default()
}

// WithLogger returns a context carrying l. ctx is unchanged.
//
// The logger to inject is the one taken from this module's product through
// Named, not Default(): injecting the default logger would take the whole path
// off the configured destinations. Injection belongs to whoever creates the
// context — the HTTP entry point, the job runner — not to a middleware, which
// would leave several parties each believing they are the fallback.
//
// The injected logger is taken as it stands. Attributes bound through
// WithAttrs on the context before it are not carried over, and what l itself
// has bound is part of l: neither WithAttrs nor WithoutAttrs reaches it.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, &binding{base: l, logger: l})
}

// WithAttrs binds attributes to the logger in ctx and returns a context
// carrying the result: the take, bind and put back that an injection point
// does, written once.
//
// A key already bound through WithAttrs is replaced, not bound a second time:
// the record carries the later value, once, where the key was first bound. A
// field describes the context it is bound on, and a context that has moved on
// — to another tenant, to another job — would otherwise have its records carry
// the old value beside the new one, old first. An attribute is keyed by its
// key; the members of a group with an empty key, which a handler writes in
// line, are keyed by their own, and one with an empty key that is not a group
// is kept beside the rest.
//
// The attributes are bound rather than passed with each record, so they are
// evaluated and judged for redaction once and reused by every record of that
// request. A binding that only adds keys binds them on the logger already in
// the context; one that replaces a key derives the logger again from the
// injected one, binding every attribute afresh — and so judging each of them
// against the rules registered by then. On a context with no logger the
// attributes accumulate on the default logger, so nothing is lost, but the path
// stays on the bootstrap chain.
func WithAttrs(ctx context.Context, args ...any) context.Context {
	added := attrsOf(args)
	if len(added) == 0 {
		return ctx
	}
	prev := bindingOf(ctx)
	var held []slog.Attr
	if prev != nil {
		held = prev.attrs
	}
	merged, replaced := merge(held, added)
	next := &binding{attrs: merged}
	if prev != nil {
		next.base = prev.base
	}
	if replaced {
		next.logger = next.root().With(anyOf(merged)...)
	} else {
		next.logger = FromContext(ctx).With(anyOf(added)...)
	}
	return context.WithValue(ctx, contextKey{}, next)
}

// WithoutAttrs returns a context whose logger no longer carries the attributes
// bound through WithAttrs under these keys. It is how a producer takes its own
// field off a context that has stopped being what the field said — a context
// that gave up its tenant no longer belongs to the tenant its records name.
//
// A key nothing bound is not an error, and a context that carries none of the
// keys is returned as it is. What the injected logger has bound itself is not
// reached, for the reason WithLogger gives.
func WithoutAttrs(ctx context.Context, keys ...string) context.Context {
	prev := bindingOf(ctx)
	if prev == nil || len(keys) == 0 {
		return ctx
	}
	kept := slices.DeleteFunc(slices.Clone(prev.attrs), func(a slog.Attr) bool {
		return slices.Contains(keys, a.Key)
	})
	if len(kept) == len(prev.attrs) {
		return ctx
	}
	next := &binding{base: prev.base, attrs: kept}
	next.logger = next.root().With(anyOf(kept)...)
	return context.WithValue(ctx, contextKey{}, next)
}

// attrsOf turns the arguments of a With call into the attributes slog makes of
// them, by the standard library's own rule, with the members of a group that
// has an empty key put in its place.
func attrsOf(args []any) []slog.Attr {
	if len(args) == 0 {
		return nil
	}
	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "", 0)
	r.Add(args...)
	out := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		out = appendInline(out, a)
		return true
	})
	return out
}

// appendInline appends an attribute, or the members of a group with an empty
// key in its place.
func appendInline(out []slog.Attr, a slog.Attr) []slog.Attr {
	if a.Key != "" || a.Value.Kind() != slog.KindGroup {
		return append(out, a)
	}
	for _, member := range a.Value.Group() {
		out = appendInline(out, member)
	}
	return out
}

// merge puts the added attributes over the held ones by key, and reports
// whether any of them replaced one — including one added earlier in the same
// call. An attribute with an empty key names nothing to replace, and is kept
// beside the rest. The held slice is not written.
func merge(held, added []slog.Attr) ([]slog.Attr, bool) {
	out := slices.Clone(held)
	replaced := false
	for _, a := range added {
		i := -1
		if a.Key != "" {
			i = slices.IndexFunc(out, func(b slog.Attr) bool { return b.Key == a.Key })
		}
		if i >= 0 {
			out[i] = a
			replaced = true
			continue
		}
		out = append(out, a)
	}
	return out, replaced
}

// anyOf hands attributes to a With call, which takes them as arguments.
func anyOf(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}
