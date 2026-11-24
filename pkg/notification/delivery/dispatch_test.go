package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

func validParams() map[string]any {
	return map[string]any{"invoice_id": "inv-1", "amount": 100}
}

// Dispatch runs the shape checks first.
func TestDispatchRefusesAnUnreadableShape(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")

	cases := map[string]func(*notification.Dispatch){
		"empty type key":  func(d *notification.Dispatch) { d.TypeKey = "" },
		"external class":  func(d *notification.Dispatch) { d.Recipient.Class = notification.RecipientExternal },
		"unknown class":   func(d *notification.Dispatch) { d.Recipient.Class = "robot" },
		"empty recipient": func(d *notification.Dispatch) { d.Recipient.UserID = "" },
		"empty locale":    func(d *notification.Dispatch) { d.Locale = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := dispatch("u1", "occ-1", validParams())
			mutate(&d)
			if _, err := r.p.Dispatch(ctx, d); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("Dispatch ended with %v, want ErrInvalidRequest", err)
			}
		})
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a refused dispatch enqueued %d jobs, want 0", got)
	}
}

// A type that is not declared is ErrUnknownType; a parameter outside the type's
// whitelist is ErrParamsNotAllowed.
func TestDispatchRefusesAnUnknownTypeAndAnUnlistedParameter(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")

	unknown := dispatch("u1", "occ-1", validParams())
	unknown.TypeKey = "billing.no_such_type"
	if _, err := r.p.Dispatch(ctx, unknown); !errors.Is(err, notification.ErrUnknownType) {
		t.Fatalf("an unknown type ended with %v, want ErrUnknownType", err)
	}

	unlisted := dispatch("u1", "occ-1", map[string]any{"invoice_id": "inv-1", "secret": "s"})
	if _, err := r.p.Dispatch(ctx, unlisted); !errors.Is(err, notification.ErrParamsNotAllowed) {
		t.Fatalf("an unlisted parameter ended with %v, want ErrParamsNotAllowed", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a refused dispatch enqueued %d jobs, want 0", got)
	}
}

// A context that carries no tenant is refused with the tenant capability's own
// error, before the queue is reached.
func TestDispatchRefusesAContextWithoutATenant(t *testing.T) {
	r := newRig(t, nil)
	if _, err := r.p.Dispatch(context.Background(), dispatch("u1", "occ-1", validParams())); !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("a tenant-less dispatch ended with %v, want tenant.ErrNoTenantInContext", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a refused dispatch enqueued %d jobs, want 0", got)
	}
}

// A dispatch enqueues one job of this module's type, and two dispatches
// of one logical event converge into one job.
func TestDispatchEnqueuesOneJobAndConvergesOneLogicalEvent(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")

	first, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams()))
	if err != nil {
		t.Fatalf("the first dispatch failed: %v", err)
	}
	second, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams()))
	if err != nil {
		t.Fatalf("the repeated dispatch failed: %v", err)
	}
	if first != second {
		t.Errorf("the repeated dispatch returned %q, want the existing job %q", second, first)
	}
	tasks := r.queue.enqueued()
	if len(tasks) == 0 {
		t.Fatal("no job was enqueued")
	}
	for _, task := range tasks {
		if task.Type != JobType {
			t.Errorf("the enqueued job is of type %q, want %q", task.Type, JobType)
		}
		if task.IdempotencyKey == "" {
			t.Error("the enqueued job carries no idempotency key")
		}
	}
}

// Two dispatches to different recipients, and two of different logical events to
// one recipient, do not converge: each derives its own job.
func TestDispatchDoesNotConvergeDifferentRecipientsOrOccurrences(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")

	base, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams()))
	if err != nil {
		t.Fatalf("the base dispatch failed: %v", err)
	}
	other, err := r.p.Dispatch(ctx, dispatch("u2", "occ-1", validParams()))
	if err != nil {
		t.Fatalf("the other-recipient dispatch failed: %v", err)
	}
	later, err := r.p.Dispatch(ctx, dispatch("u1", "occ-2", validParams()))
	if err != nil {
		t.Fatalf("the other-occurrence dispatch failed: %v", err)
	}
	if base == other {
		t.Error("two recipients converged into one job")
	}
	if base == later {
		t.Error("two occurrences converged into one job")
	}
}
