package log

import (
	"context"
	"log/slog"
)

type redactHandler struct {
	next   slog.Handler
	groups []string
}

func newRedactHandler(next slog.Handler) *redactHandler {
	return &redactHandler{next: next}
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	rules := currentRules()
	inherited := rules.matchesAny(h.groups)

	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, redactAttr(rules, inherited, a))
		return true
	})

	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(attrs...)
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	rules := currentRules()
	inherited := rules.matchesAny(h.groups)

	judged := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		judged[i] = redactAttr(rules, inherited, a)
	}
	return &redactHandler{next: h.next.WithAttrs(judged), groups: h.groups}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &redactHandler{next: h.next.WithGroup(name), groups: groups}
}

func redactAttr(rules *ruleSet, inherited bool, a slog.Attr) slog.Attr {
	value := a.Value.Resolve()
	hit := inherited || rules.matchesKey(a.Key)

	if value.Kind() == slog.KindGroup {
		members := value.Group()
		judged := make([]slog.Attr, len(members))
		for i, member := range members {
			judged[i] = redactAttr(rules, hit, member)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(judged...)}
	}
	if hit {
		return slog.Attr{Key: a.Key, Value: slog.StringValue(MaskedText)}
	}
	return slog.Attr{Key: a.Key, Value: maskValue(rules, value)}
}

func maskValue(rules *ruleSet, value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindString:
		if masked, ok := rules.maskString(value.String()); ok {
			return slog.StringValue(masked)
		}
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			if masked, ok := rules.maskString(err.Error()); ok {
				return slog.StringValue(masked)
			}
		}
	}
	return value
}
