package delivery

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/notification"
)

// ownershipSpec is a type every ownership case declares: in_app alone, and no
// parameter whitelist, so a destination reaches the ownership rule rather than
// being refused by the whitelist first.
func ownershipSpec() notification.TypeSpec {
	return notification.TypeSpec{
		Key:             typeName,
		Group:           "billing",
		DefaultChannels: []notification.Channel{notification.ChannelInApp},
		Unsubscribable:  true,
	}
}

// destinationParams is a parameter set that carries one external destination.
func destinationParams(to string) map[string]any {
	return map[string]any{"to": to}
}

// A destination the recipient does not own is refused: one bound to another
// account and one bound to no account both answer ErrInvalidRequest, and neither
// enqueues a job.
func TestDispatchRefusesADestinationThatIsNotTheRecipientsOwn(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")

	for name, to := range map[string]string{
		"another account's": "u2@example.test",
		"no account's":      "nobody@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-"+name, destinationParams(to))); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("Dispatch ended with %v, want ErrInvalidRequest", err)
			}
		})
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a refused dispatch enqueued %d jobs, want 0", got)
	}
}

// A destination the recipient owns passes. The address is compared in the form
// the account store keeps, so a differently cased email address is the same
// address.
func TestDispatchAcceptsADestinationThatIsTheRecipientsOwn(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")

	for name, to := range map[string]string{
		"email": "U1@Example.TEST",
		"phone": "+15550001",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-"+name, destinationParams(to))); err != nil {
				t.Fatalf("Dispatch ended with %v, want the destination accepted", err)
			}
		})
	}
	if got := len(r.queue.enqueued()); got != 2 {
		t.Errorf("the accepted dispatches enqueued %d jobs, want 2", got)
	}
}

// A recipient that carries no identifier cannot own a destination, so a
// dispatch that names one is refused. The account may exist and simply hold no
// identifier, and it may be unknown altogether.
func TestDispatchRefusesADestinationForARecipientWithNoIdentifiers(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")
	r.accounts.register("u4")

	for name, recipient := range map[string]string{
		"a live account without identifiers": "u4",
		"an account that does not exist":     "u9",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.p.Dispatch(ctx, dispatch(recipient, "occ-"+name, destinationParams("someone@example.test"))); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("Dispatch ended with %v, want ErrInvalidRequest", err)
			}
		})
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a refused dispatch enqueued %d jobs, want 0", got)
	}
}

// A failure to read the recipient's identifiers is an outage and not a pass: the
// dispatch ends with the store's own error rather than treating the recipient as
// owning nothing, the error is not the ownership rule's refusal, and nothing is
// enqueued.
func TestDispatchRefusesWhenTheRecipientIdentifiersCannotBeRead(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")
	storeDown := errors.New("the account store is down")
	r.accounts.mu.Lock()
	r.accounts.idsErr = storeDown
	r.accounts.mu.Unlock()

	_, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", destinationParams("u1@example.test")))
	if !errors.Is(err, storeDown) {
		t.Fatalf("Dispatch ended with %v, want the account store's error", err)
	}
	if errors.Is(err, notification.ErrInvalidRequest) {
		t.Errorf("a failed identifier read was reported as an ownership refusal: %v", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a dispatch whose identifier read failed enqueued %d jobs, want 0", got)
	}
}

// A failure to look the destination up is the same outage, not a pass: the
// dispatch ends with the store's own error rather than treating the destination
// as unowned, and nothing is enqueued.
func TestDispatchRefusesWhenTheDestinationLookupFails(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")
	storeDown := errors.New("the account store is down")
	r.accounts.mu.Lock()
	r.accounts.lookErr = storeDown
	r.accounts.mu.Unlock()

	_, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", destinationParams("u1@example.test")))
	if !errors.Is(err, storeDown) {
		t.Fatalf("Dispatch ended with %v, want the account store's error", err)
	}
	if errors.Is(err, notification.ErrInvalidRequest) {
		t.Errorf("a failed destination lookup was reported as an ownership refusal: %v", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a dispatch whose destination lookup failed enqueued %d jobs, want 0", got)
	}
}

// A dispatch that carries no destination is unaffected: the ownership rule
// reads a destination, and a channel that needs none has nothing to check.
func TestDispatchDoesNotCheckAChannelWithoutADestination(t *testing.T) {
	r := newRig(t, func(h *host) { h.specs = map[string]notification.TypeSpec{"test.declarer": ownershipSpec()} })
	ctx := r.tenantCtx("t1")

	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("Dispatch ended with %v, want the destination-free dispatch accepted", err)
	}
	if got := len(r.queue.enqueued()); got != 1 {
		t.Errorf("the accepted dispatch enqueued %d jobs, want 1", got)
	}
}
