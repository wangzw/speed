package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/pgpool"
)

// TestStanceFailsWithoutAConnectionString pins the enablement rule at this
// backend's own precondition: a module taken in at run level has to start, so an
// implementation the host did not point at a database fails the startup naming
// the item. It is not a stance that lets the run continue without an event bus.
func TestStanceFailsWithoutAConnectionString(t *testing.T) {
	_, err := stance(&pgpool.Config{})
	if err == nil {
		t.Fatal("stance with no connection string did not fail the startup")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+pgpool.DSNKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
}

func TestStanceEnablesOnceAConnectionStringIsGiven(t *testing.T) {
	got, err := stance(&pgpool.Config{DSN: "postgres://localhost/x"})
	if err != nil {
		t.Fatalf("stance with a locator failed the startup: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a locator = %v, want StateEnabled", got.State)
	}
}

// TestRetireRunsAtMostOncePerInterval pins the pacing of the retention pass: a
// pass at the end of every drain would be two large DELETEs at the poll
// interval, per replica, one of them an anti-join over the delivered table — a
// standing load for a window a day wide.
func TestRetireRunsAtMostOncePerInterval(t *testing.T) {
	b := &bus{}
	start := time.Now()
	runs := 0
	run := func() error { runs++; return nil }
	if err := b.retireIfDue(start, run); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	if runs != 1 {
		t.Fatal("the first round did not run the retention pass, so a process that just started " +
			"retires nothing until the second interval")
	}
	if err := b.retireIfDue(start.Add(time.Minute), run); err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if runs != 1 {
		t.Fatal("a round a minute later ran the retention pass again; the pass is due once per " +
			"retireEvery, not once per poll")
	}
	if err := b.retireIfDue(start.Add(retireEvery), run); err != nil {
		t.Fatalf("the third pass: %v", err)
	}
	if runs != 2 {
		t.Fatal("the retention pass is not due when the interval has passed")
	}
}

// TestAFailedRetireDoesNotBuyAnHour pins what a failed pass does to the
// schedule: nothing. Recording the run before running it would leave a
// transient DELETE failure with an hour of silence.
func TestAFailedRetireDoesNotBuyAnHour(t *testing.T) {
	b := &bus{}
	start := time.Now()
	sentinel := errors.New("the connection dropped")
	runs := 0
	fail := func() error { runs++; return sentinel }
	if err := b.retireIfDue(start, fail); !errors.Is(err, sentinel) {
		t.Fatalf("a failed pass returned %v, want the failure itself", err)
	}
	if err := b.retireIfDue(start.Add(time.Second), fail); !errors.Is(err, sentinel) {
		t.Fatalf("the next round returned %v; a failed pass has to be retried, not skipped for "+
			"the whole interval", err)
	}
	if runs != 2 {
		t.Fatalf("the failing pass ran %d times across two rounds, want 2: a failed pass must not "+
			"record itself as done", runs)
	}
}
