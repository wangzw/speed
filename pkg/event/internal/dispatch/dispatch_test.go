package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
)

func newTable() *Table {
	return New("event/test", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestAStopWhoseGraceRunsOutCancelsTheSubscribers pins the bound on the stop
// stage. A subscriber still running when the grace runs out is handed a
// cancelled context, and the stage reports that it cut something short rather
// than returning as if the stop had been clean.
func TestAStopWhoseGraceRunsOutCancelsTheSubscribers(t *testing.T) {
	tbl := newTable()
	tbl.grace = 100 * time.Millisecond
	sawCancel := make(chan error, 1)
	tbl.Subscribe("t", func(ctx context.Context, _ event.Event) error {
		<-ctx.Done()
		sawCancel <- ctx.Err()
		return nil
	})
	tbl.Freeze(context.Background())
	tbl.Go(func(_, handling context.Context) { tbl.Deliver(handling, event.Event{Type: "t"}) })

	began := time.Now()
	err := tbl.Stop()
	if err == nil {
		t.Fatal("a stop whose subscribers outlived the grace reported a clean stop")
	}
	if !errors.Is(<-sawCancel, context.Canceled) {
		t.Fatal("the subscriber running past the grace was not handed a cancelled context")
	}
	if took := time.Since(began); took > 100*time.Millisecond+settle+time.Second {
		t.Fatalf("the stop stage took %v, past its grace and the moment it gives a cancelled subscriber", took)
	}
	if again := tbl.Stop(); again == nil || again.Error() != err.Error() {
		t.Fatalf("a second Stop reported %v, want what the first reported: %v", again, err)
	}
}

// TestAStopBeforeTheStartStageIsClean covers the rollback shape: a startup that
// failed part-way stops every module it built, whatever stage it reached.
func TestAStopBeforeTheStartStageIsClean(t *testing.T) {
	tbl := newTable()
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop on a table that never froze: %v", err)
	}
	if err := tbl.Close(); err != nil {
		t.Fatalf("Close on a table that never froze: %v", err)
	}
	if _, first := tbl.Freeze(context.Background()); first {
		t.Fatal("a table that has stopped opened to publishing")
	}
	if err := tbl.Accepting(); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Accepting after the stop stage: %v, want ErrClosed", err)
	}
}

// TestNoReaderStartsAfterTheStopStage pins what keeps the wait for the readers
// sound: a reader arriving once the stop stage began is not started, so none is
// added while the stage waits.
func TestNoReaderStartsAfterTheStopStage(t *testing.T) {
	tbl := newTable()
	tbl.Freeze(context.Background())
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	var ran atomic.Bool
	tbl.Go(func(context.Context, context.Context) { ran.Store(true) })
	time.Sleep(50 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a reader started after the stop stage")
	}
}

// TestTheDrivesCancellationDoesNotReachTheBus pins where the contexts a bus
// reads and handles under come from: the start stage's context, without its
// cancellation. The drive's context is cancelled as soon as a host begins
// shutting down, and the bus serves until its own stop stage.
func TestTheDrivesCancellationDoesNotReachTheBus(t *testing.T) {
	tbl := newTable()
	type key struct{}
	drive, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "carried"))
	tbl.Freeze(drive)
	seen := make(chan [2]context.Context, 1)
	tbl.Go(func(reading, handling context.Context) { seen <- [2]context.Context{reading, handling} })
	got := <-seen
	cancel()
	for i, ctx := range got {
		if ctx.Err() != nil {
			t.Fatalf("context %d ended with the drive's: %v", i, ctx.Err())
		}
		if ctx.Value(key{}) != "carried" {
			t.Fatalf("context %d lost the values the drive's context carried", i)
		}
	}
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got[0].Err() == nil {
		t.Fatal("the reading context outlived the stop stage")
	}
}

// TestPayloadKeepsEveryNumberAsItsText pins the transport-side decoding: a
// number comes back as the decimal text the publisher's serialization wrote,
// nested ones included, so an integer past 2^53 is not rounded on the way.
func TestPayloadKeepsEveryNumberAsItsText(t *testing.T) {
	got, err := Payload([]byte(`{"id":9007199254740993,"deal":{"amount":9223372036854775807},"ratio":0.25}`))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	fields, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("the payload decoded as %T, want the ordinary map", got)
	}
	if id := fields["id"]; id != json.Number("9007199254740993") {
		t.Fatalf("id decoded as %#v, want its text", id)
	}
	nested, _ := fields["deal"].(map[string]any)
	if amount := nested["amount"]; amount != json.Number("9223372036854775807") {
		t.Fatalf("the nested amount decoded as %#v, want its text", amount)
	}
	if ratio := fields["ratio"]; ratio != json.Number("0.25") {
		t.Fatalf("ratio decoded as %#v, want its text", ratio)
	}
}

// TestPayloadRefusesDataAfterItsValue keeps the decoder as strict as the
// one-shot decoding it replaces: a payload is one value.
func TestPayloadRefusesDataAfterItsValue(t *testing.T) {
	if _, err := Payload([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("a payload carrying a second value decoded without an error")
	}
	if _, err := Payload([]byte(" null \n")); err != nil {
		t.Fatalf("a payload of null with surrounding whitespace: %v", err)
	}
}
