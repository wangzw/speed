package otel

import (
	"context"
	"errors"
	"testing"
	"time"
)

type holdingRelease struct{}

func (holdingRelease) Shutdown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

type recordingRelease struct {
	called bool
	err    error
}

func (r *recordingRelease) Shutdown(ctx context.Context) error {
	r.called = true
	r.err = ctx.Err()
	return r.err
}

type failingRelease struct{ err error }

func (f failingRelease) Shutdown(context.Context) error { return f.err }

// TestTheReleaseGoesOnPastAnExporterThatFails pins that every collected
// exporter is released whatever an earlier one answered.
func TestTheReleaseGoesOnPastAnExporterThatFails(t *testing.T) {
	failure := errors.New("release failed")
	after := &recordingRelease{}

	err := releaseExporters(context.Background(), []releasable{failingRelease{err: failure}, after})

	if !errors.Is(err, failure) {
		t.Errorf("releaseExporters returned %v, want the failure of the first exporter", err)
	}
	if !after.called {
		t.Error("the exporter after the failing one was not released")
	}
}

// TestTheReleaseIsNotCutShortByACancelledContext pins that the release
// keeps the values of the context it receives and not its cancellation.
func TestTheReleaseIsNotCutShortByACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exporter := &recordingRelease{}

	if err := releaseExporters(ctx, []releasable{exporter}); err != nil {
		t.Errorf("releaseExporters under a cancelled context returned %v, want nil", err)
	}
	if !exporter.called {
		t.Error("the exporter was not released")
	}
}

// TestTheReleaseBoundIsSharedByEveryExporter pins that one bound covers
// the whole release and not each exporter in turn.
func TestTheReleaseBoundIsSharedByEveryExporter(t *testing.T) {
	after := &recordingRelease{}
	done := make(chan error, 1)
	go func() {
		done <- releaseExporters(context.Background(), []releasable{holdingRelease{}, after})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("releaseExporters returned %v, want the deadline of the exporter that held the release", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("releaseExporters did not return: the release of an exporter that holds it is unbounded")
	}
	if !after.called {
		t.Fatal("the exporter after the holding one was not released")
	}
	if !errors.Is(after.err, context.DeadlineExceeded) {
		t.Errorf("the exporter after the holding one was handed a context with error %v, want the "+
			"expired bound the first one ran out: the bound is shared", after.err)
	}
}
