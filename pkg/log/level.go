package log

import (
	"context"
	"log/slog"
)

type levelHandler struct {
	level          slog.Leveler
	next           slog.Handler
	nowhereToWrite bool
}

func newLevelHandler(level slog.Leveler, next slog.Handler) *levelHandler {
	return &levelHandler{level: level, next: next}
}

func newSilentLevelHandler(level slog.Leveler, next slog.Handler) *levelHandler {
	return &levelHandler{level: level, next: next, nowhereToWrite: true}
}

func (h *levelHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.nowhereToWrite {
		return false
	}
	return level >= h.level.Level()
}

func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return &levelHandler{level: h.level, next: h.next.WithAttrs(attrs), nowhereToWrite: h.nowhereToWrite}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &levelHandler{level: h.level, next: h.next.WithGroup(name), nowhereToWrite: h.nowhereToWrite}
}
