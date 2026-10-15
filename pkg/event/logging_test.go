package event_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	eventnats "github.com/vislake/speed/pkg/event/nats"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
	"github.com/vislake/speed/pkg/log"
)

// recordingLogger is a Logger capability that keeps every record written
// through the loggers it hands out, so a case can tell a record that went
// through the assembly's logging from one that went anywhere else.
type recordingLogger struct {
	mu      sync.Mutex
	records []string
}

func (l *recordingLogger) Named(name string) *slog.Logger {
	return slog.New(&recordingHandler{into: l}).With(log.ModuleAttrKey, name)
}

func (l *recordingLogger) Redaction() log.Redaction { return noopRedaction{} }

// noopRedaction is a logging capability's registration face that keeps no rule.
// A backend registers its connection credentials through it, and a stand-in
// whose records nobody reads has nothing to mask.
type noopRedaction struct{}

func (noopRedaction) AddKeys(...string)              {}
func (noopRedaction) AddPattern(string, log.Matcher) {}

func (l *recordingLogger) written() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.records, "\n")
}

type recordingHandler struct {
	into  *recordingLogger
	attrs []slog.Attr
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	line := r.Message
	for _, a := range h.attrs {
		line += " " + a.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		line += " " + a.String()
		return true
	})
	h.into.mu.Lock()
	defer h.into.mu.Unlock()
	h.into.records = append(h.into.records, line)
	return nil
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{into: h.into, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// pointedAt is a reading face that points each transport at the server a case
// acquired, and leaves every other value at the module's default.
type pointedAt struct {
	redisAddr string
	redisDB   int
	dsn       string
	natsURL   string
}

func (p pointedAt) Decode(_ string, target any) error {
	switch cfg := target.(type) {
	case *eventredis.Config:
		cfg.Addr, cfg.DB = p.redisAddr, p.redisDB
	case *pgpool.Config:
		cfg.DSN = p.dsn
	case *eventnats.Config:
		cfg.URL = p.natsURL
	}
	return nil
}

// failingSubscriber is a module that subscribes a handler failing on purpose,
// and publishes the event it fails on from the drive's action.
func failingSubscriber() core.Module {
	return core.Module{
		Name:     "case.failing-subscriber",
		Requires: []core.Requirement{{Token: (*event.Bus)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe("case.logged", "test", func(context.Context, event.Event) error {
				return errors.New("this subscriber fails on purpose")
			})
			return nil
		},
	}
}

// TestASubscriberFailureIsWrittenThroughTheAssemblysLogging pins where a
// subscriber's failure is reported on every backend: through the Logger
// capability the assembly delivers, so the record obeys its configured
// destinations, format and redaction, rather than through the process default
// logger, which that capability never replaces.
func TestASubscriberFailureIsWrittenThroughTheAssemblysLogging(t *testing.T) {
	cases := []struct {
		module string
		point  func(t *testing.T) pointedAt
	}{
		{memory.ModuleName, func(*testing.T) pointedAt { return pointedAt{} }},
		{eventredis.ModuleName, func(t *testing.T) pointedAt {
			addr, db := redistest.Acquire(t)
			return pointedAt{redisAddr: addr, redisDB: db}
		}},
		{eventpostgres.ModuleName, func(t *testing.T) pointedAt { return pointedAt{dsn: pgtest.Acquire(t)} }},
		{eventnats.ModuleName, func(t *testing.T) pointedAt { return pointedAt{natsURL: natstest.URL(t)} }},
	}
	for _, c := range cases {
		t.Run(c.module, func(t *testing.T) {
			logger := &recordingLogger{}
			reg := core.New()
			// The reading face and the recorder are the instances of the config
			// and log modules. The recorder stands where the log module's own
			// product would: that module's Prepare and New do not run for it, and
			// its Close, the only later callback the module declares, leaves an
			// instance that is not its own alone.
			reg.Register(config.Module())
			reg.Supply(c.point(t), config.ModuleName)
			reg.Register(log.Module())
			reg.Supply(logger, log.ModuleName)
			reg.Register(memory.Module())
			reg.Register(eventredis.Module())
			reg.Register(eventpostgres.Module())
			reg.Register(eventnats.Module())
			reg.Register(failingSubscriber())
			err := reg.Drive(context.Background(), core.Drive{
				Depth:        core.StageStart,
				Participants: []string{c.module, "case.failing-subscriber"},
				Action: func(ctx context.Context, reg *core.Registry) error {
					bus, err := core.Resolve[event.Bus](reg)
					if err != nil {
						return err
					}
					if err := bus.Publish(ctx, event.Event{Type: "case.logged"}); err != nil {
						return err
					}
					deadline := time.Now().Add(10 * time.Second)
					for time.Now().Before(deadline) {
						if strings.Contains(logger.written(), "a subscriber failed") {
							return nil
						}
						time.Sleep(10 * time.Millisecond)
					}
					return errors.New("the subscriber's failure never reached the assembly's logging")
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := logger.written(); !strings.Contains(got, log.ModuleAttrKey+"="+c.module) {
				t.Fatalf("the record does not name the module that wrote it:\n%s", got)
			}
		})
	}
}
