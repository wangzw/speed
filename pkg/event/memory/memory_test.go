package memory

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/log"
)

// openBus builds a bus that has not been put into service, for the cases that
// drive the stages themselves.
func openBus(t *testing.T) event.Bus {
	t.Helper()
	b, err := open(context.Background(), &Config{Replicas: 1}, log.Default())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return b
}

// TestConforms runs the shared contract. Every implementation of the capability
// runs this same suite.
func TestConforms(t *testing.T) {
	bussuite.AssertConforms(t, func(t *testing.T) (event.Bus, func()) {
		b := openBus(t)
		// This bus delivers in-process, so putting it into service is only the
		// freeze the drive performs at its start stage.
		return b, func() {
			if err := start(context.Background(), b); err != nil {
				t.Fatalf("start: %v", err)
			}
		}
	})
}

func TestStanceIsAutoWhenOneReplicaSharesTheState(t *testing.T) {
	got, err := stance(&Config{Replicas: 1})
	if err != nil {
		t.Fatalf("stance with one replica failed the startup: %v", err)
	}
	if got.State != core.StateAuto {
		t.Fatalf("stance with one replica = %v, want StateAuto: this implementation is the "+
			"capability's zero-configuration default and has to stand down for an explicitly "+
			"enabled backend", got.State)
	}
}

// TestStanceFailsTheStartupWhenSeveralReplicasShareTheState is the half of the
// enablement rule this module owns: a module taken in at run level has to start,
// and this one cannot start on a deployment of several replicas. The answer is a
// failure naming the input, not a stance that lets the startup succeed with the
// capability missing — a stand-down there is the silent loss of the bus the
// premise exists to prevent.
func TestStanceFailsTheStartupWhenSeveralReplicasShareTheState(t *testing.T) {
	_, err := stance(&Config{Replicas: 2})
	if err == nil {
		t.Fatal("stance with two replicas did not fail the startup: a subscriber on one replica " +
			"would never see an event published on another, while every publish still returned " +
			"success")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+replicasKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
}

// TestPublishAfterCloseIsRefused covers the one failure this implementation adds
// to the hand-over step: a publish that arrives once the bus is closed is
// refused rather than quietly reaching nobody.
func TestPublishAfterCloseIsRefused(t *testing.T) {
	b := openBus(t)
	var ran atomic.Int32
	b.Subscribe("t", func(context.Context, event.Event) error {
		ran.Add(1)
		return nil
	})
	if err := b.(*bus).Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: "t"}); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Publish after Close: %v, want ErrClosed", err)
	}
	if n := ran.Load(); n != 0 {
		t.Fatalf("a subscriber ran %d times for a publish the bus refused", n)
	}
}

// TestStartFreezesTheSubscriptionTable covers the stage this implementation
// adds: the table is built by every module's Init, and the first publish must
// not race a subscription still arriving.
func TestStartFreezesTheSubscriptionTable(t *testing.T) {
	b := openBus(t)
	b.Subscribe("before", func(context.Context, event.Event) error { return nil })
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Subscribe after the start stage did not panic: a subscription registered that " +
				"late would silently miss everything published before it")
		}
	}()
	b.Subscribe("after", func(context.Context, event.Event) error { return nil })
}

// TestPublishRunsHandlersBeforeReturning pins what this implementation adds over
// the capability's floor, which the contract suite deliberately does not assert
// because a transport-backed bus cannot offer it.
func TestPublishRunsHandlersBeforeReturning(t *testing.T) {
	b := openBus(t)
	var ran atomic.Int32
	b.Subscribe("t", func(context.Context, event.Event) error {
		ran.Add(1)
		return nil
	})
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if n := ran.Load(); n != 1 {
		t.Fatalf("the subscriber had run %d times by the time Publish returned, want 1", n)
	}
}
