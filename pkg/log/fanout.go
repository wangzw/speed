package log

import (
	"context"
	"errors"
	"log/slog"
)

type fanoutHandler struct{ branches []slog.Handler }

func newFanoutHandler(branches []slog.Handler) *fanoutHandler {
	return &fanoutHandler{branches: branches}
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, b := range h.branches {
		if b.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, b := range h.branches {
		if err := b.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	derived := make([]slog.Handler, len(h.branches))
	for i, b := range h.branches {
		derived[i] = b.WithAttrs(attrs)
	}
	return &fanoutHandler{branches: derived}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	derived := make([]slog.Handler, len(h.branches))
	for i, b := range h.branches {
		derived[i] = b.WithGroup(name)
	}
	return &fanoutHandler{branches: derived}
}
