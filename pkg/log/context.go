package log

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

type contextKey struct{}

type binding struct {
	base   *slog.Logger
	attrs  []slog.Attr
	logger *slog.Logger
}

func (b *binding) root() *slog.Logger {
	if b == nil || b.base == nil {
		return Default()
	}
	return b.base
}

func bindingOf(ctx context.Context) *binding {
	b, _ := ctx.Value(contextKey{}).(*binding)
	return b
}

// FromContext returns the logger carried by ctx, or the process default logger when it carries
// none.
func FromContext(ctx context.Context) *slog.Logger {
	if b := bindingOf(ctx); b != nil && b.logger != nil {
		return b.logger
	}
	return Default()
}

// WithLogger returns a context carrying l.
// ctx is unchanged.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, &binding{base: l, logger: l})
}

// WithAttrs binds attributes to the logger in ctx and returns a context carrying the result: the
// take, bind and put back that an injection point does, written once.
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

// WithoutAttrs returns a context whose logger no longer carries the attributes bound through
// WithAttrs under these keys.
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

func appendInline(out []slog.Attr, a slog.Attr) []slog.Attr {
	if a.Key != "" || a.Value.Kind() != slog.KindGroup {
		return append(out, a)
	}
	for _, member := range a.Value.Group() {
		out = appendInline(out, member)
	}
	return out
}

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

func anyOf(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}
