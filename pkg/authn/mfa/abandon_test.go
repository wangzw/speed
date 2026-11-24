package mfa

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestAbandonReleasesOnlyTheRowItsFlowWrote checks that abandoning a flow removes only the pending row that flow wrote, with its factor data.
func TestAbandonReleasesOnlyTheRowItsFlowWrote(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorWebAuthn}, func(t *testing.T, r *rig) {
		passkey := r.p.factors[authn.FactorWebAuthn].(*fakeFactor)
		passkey.enrollFn = func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return confirming("seed-state"), nil
		}
		ctx := context.Background()

		first, err := r.p.Enroll(ctx, flow(acct), authn.FactorWebAuthn, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll, first flow: %v", err)
		}
		second, err := r.p.Enroll(ctx, flow(acct), authn.FactorWebAuthn, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll, second flow: %v", err)
		}
		if rows := r.rows(acct); len(rows) != 2 {
			t.Fatalf("two registrations left %d rows, want two", len(rows))
		}
		abandonedID, _, ok := unwrapPrivate(step.PrivateOf(first))
		if !ok {
			t.Fatalf("the first step's private bytes %q carry no row identifier", step.PrivateOf(first))
		}
		keptID, _, ok := unwrapPrivate(step.PrivateOf(second))
		if !ok || keptID == abandonedID {
			t.Fatalf("the second step's private bytes %q carry no distinct row identifier", step.PrivateOf(second))
		}

		abandoned := flow(acct)
		abandoned.Private = step.PrivateOf(first)
		if err := r.p.Abandon(ctx, abandoned); err != nil {
			t.Fatalf("Abandon: %v", err)
		}

		rows := r.rows(acct)
		if len(rows) != 1 || rows[0].ID != keptID {
			t.Fatalf("after the abandonment the register holds %+v, want only the row %s the other flow wrote", rows, keptID)
		}
		if len(passkey.removed) != 1 || passkey.removed[0] != abandonedID {
			t.Errorf("the factor's own data was removed for %v, want only the abandoned row %s", passkey.removed, abandonedID)
		}
	})
}

// TestAbandonWithoutAPendingRowChangesNothing checks that abandoning a flow with no pending row of its own changes nothing.
func TestAbandonWithoutAPendingRowChangesNothing(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorWebAuthn}, func(t *testing.T, r *rig) {
		passkey := r.p.factors[authn.FactorWebAuthn].(*fakeFactor)
		passkey.enrollFn = func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return confirming("seed-state"), nil
		}
		ctx := context.Background()
		pending, err := r.p.Enroll(ctx, flow(acct), authn.FactorWebAuthn, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if rows := r.rows(acct); len(rows) != 1 {
			t.Fatalf("the registration left %d rows, want one", len(rows))
		}
		activeID := r.insertRow(acct, authn.FactorTOTP, authn.FactorActive, base, nil)
		keptID, _, ok := unwrapPrivate(step.PrivateOf(pending))
		if !ok {
			t.Fatalf("the step's private bytes %q carry no row identifier", step.PrivateOf(pending))
		}

		cases := map[string]authn.FlowState{
			"no private state":               flow(acct),
			"a row that is not there":        privateFlow(acct, wrapPrivate("no-such-row", nil)),
			"a row that is not pending":      privateFlow(acct, wrapPrivate(activeID, nil)),
			"bytes that are not an envelope": privateFlow(acct, []byte("not-an-envelope")),
		}
		for name, f := range cases {
			if err := r.p.Abandon(ctx, f); err != nil {
				t.Errorf("%s: Abandon = %v, want no error", name, err)
			}
		}

		rows := r.rows(acct)
		if len(rows) != 2 {
			t.Fatalf("after the no-ops the register holds %+v, want the pending row %s and the active row %s", rows, keptID, activeID)
		}
		if len(passkey.removed) != 0 {
			t.Errorf("the factor's own data was removed for %v, want nothing", passkey.removed)
		}
	})
}

func privateFlow(account authn.AccountID, private []byte) authn.FlowState {
	f := flow(account)
	f.Private = private
	return f
}
