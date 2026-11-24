package log

import (
	"fmt"
	"io"
	"log/slog"
	"math"
)

const allLevels = slog.Level(math.MinInt)

type chain struct {
	handler slog.Handler
	writers []*destWriter
	release func()
}

func newChain(cfg resolvedConfig) (*chain, error) {
	encoders := make([]func(io.Writer) slog.Handler, len(cfg.outputs))
	for i, o := range cfg.outputs {
		encoder, err := encoderFor(o)
		if err != nil {
			return nil, err
		}
		encoders[i] = encoder
	}

	writers, release, err := openWriters(cfg.outputs)
	if err != nil {
		return nil, err
	}

	branches := make([]slog.Handler, len(writers))
	for i, w := range writers {
		branches[i] = encoders[i](w)
	}
	newHead := newLevelHandler
	if len(branches) == 0 {
		newHead = newSilentLevelHandler
	}
	handler := newHead(cfg.level, newRedactHandler(newFanoutHandler(branches)))
	return &chain{handler: handler, writers: writers, release: release}, nil
}

func encoderFor(o resolvedOutput) (func(io.Writer) slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: allLevels}
	switch o.format {
	case formatText:
		return func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, opts) }, nil
	case formatJSON:
		return func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, opts) }, nil
	}
	return nil, fmt.Errorf("%w: the output to %s gives %q", ErrUnknownFormat, o.to, o.format)
}
