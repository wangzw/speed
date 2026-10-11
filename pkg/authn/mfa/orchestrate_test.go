package mfa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// answerIs fails the case unless the step is of the kind.
func answerIs(t *testing.T, got authn.Step, want step.Kind) {
	t.Helper()
	if k := step.KindOf(got); k != want {
		t.Fatalf("the answer is %v (cause %v), want %v", k, step.CauseOf(got), want)
	}
}

// confirming is the answer of a factor whose registration has a step still to go.
func confirming(private string) authn.Step {
	return authn.StepPrompt("confirm", []byte(private), authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
}

// A registration that has a step still to go leaves a pending row, which nothing
// else uses, and the row becomes active on the last step and not before.
func TestARegistrationActivatesTheRowOnTheLastStepOnly(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(_ context.Context, f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
			if len(f.Private) == 0 {
				return confirming("seed-state").WithSends(2), nil
			}
			return authn.StepEnrolled(authn.FactorRecord{}).WithSends(1), nil
		}
		ctx := context.Background()

		first, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll, first step: %v", err)
		}
		answerIs(t, first, step.KindPrompt)
		if got := step.SendsOf(first); got != 2 {
			t.Errorf("the sends the factor reported are %d in the answer, want 2 carried over", got)
		}
		rows := r.rows(acct)
		if len(rows) != 1 || rows[0].Status != authn.FactorPending || !rows[0].ActivatedAt.IsZero() {
			t.Fatalf("after the first step the register holds %+v, want one pending row with no activation", rows)
		}
		if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 0 {
			t.Errorf("%d factor events were published before the last step", n)
		}
		if n := len(r.audit.entries()); n != 0 {
			t.Errorf("%d audit rows were written before the last step", n)
		}
		if private := string(step.PrivateOf(first)); !strings.Contains(private, rows[0].ID) || !strings.HasSuffix(private, "seed-state") {
			t.Errorf("the private bytes %q carry neither the identifier of the pending row nor the factor's own", private)
		}

		// The next step is called with the factor's own bytes, and with the row.
		r.clock.advance(3 * time.Minute)
		next := flow(acct)
		next.Private = step.PrivateOf(first)
		last, err := r.p.Enroll(ctx, next, authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll, last step: %v", err)
		}
		answerIs(t, last, step.KindEnrolled)
		if got := step.SendsOf(last); got != 1 {
			t.Errorf("the sends of the last step are %d, want the 1 the factor reported", got)
		}
		if len(totp.seen) != 2 {
			t.Fatalf("the factor was called %d times, want 2", len(totp.seen))
		}
		if !totp.seen[0].hadRow || !totp.seen[1].hadRow || totp.seen[0].record.ID != totp.seen[1].record.ID {
			t.Errorf("the factor saw the pending row %+v and then %+v, want the one row both times", totp.seen[0], totp.seen[1])
		}
		if string(totp.seen[1].private) != "seed-state" || len(totp.seen[0].private) != 0 {
			t.Errorf("the factor's private bytes were %q and then %q, want none and then its own", totp.seen[0].private, totp.seen[1].private)
		}
		rows = r.rows(acct)
		if len(rows) != 1 || rows[0].Status != authn.FactorActive || !rows[0].ActivatedAt.Equal(r.clock.read()) {
			t.Fatalf("after the last step the register holds %+v, want one row active as of that step", rows)
		}
		if rows[0].ID != totp.seen[0].record.ID || step.RecordOf(last).ID != rows[0].ID {
			t.Errorf("the record the engine gets is %+v, want the row %s", step.RecordOf(last), rows[0].ID)
		}

		var payload authn.FactorChanged
		r.bus.payload(t, authn.EventFactorChanged, &payload)
		want := authn.FactorChanged{AccountID: string(acct), FactorID: rows[0].ID, Kind: "totp", Change: "enrolled", By: "self"}
		if payload != want {
			t.Errorf("the event is %+v, want %+v", payload, want)
		}
		entries := r.audit.entries()
		if len(entries) != 1 || entries[0].Action != "authn.factor.enroll" || entries[0].Outcome != "success" ||
			entries[0].Object != string(acct) || entries[0].Detail["factor_id"] != rows[0].ID || entries[0].Detail["kind"] != "totp" {
			t.Errorf("the audit rows are %+v, want the one best-effort enrol row", entries)
		}
		for k := range entries[0].Detail {
			if k != "factor_id" && k != "kind" && k != "by" {
				t.Errorf("the audit detail has the key %q, which is not one the events design lists for this row", k)
			}
		}
	})
}

func TestARowThatIsPendingCannotBeUsedToVerify(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return confirming("s"), nil
		}
		if _, err := r.p.Enroll(context.Background(), flow(acct), authn.FactorTOTP, authn.StepInput{}); err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		id := r.rows(acct)[0].ID

		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		answerIs(t, got, step.KindRefused)
		if !errors.Is(step.CauseOf(got), authn.ErrFactorNotEnrolled) {
			t.Errorf("the cause is %v, want ErrFactorNotEnrolled", step.CauseOf(got))
		}
		if totp.count("verify") != 0 {
			t.Error("the factor was asked to verify a row that is pending")
		}
		if calls := r.throttle.callList(); len(calls) != 0 {
			t.Errorf("the back-off was touched by %v: a row that is not enrolled is neither a guess nor a pass", calls)
		}
		got, err = r.p.Challenge(context.Background(), flow(acct), id)
		if err != nil {
			t.Fatalf("Challenge: %v", err)
		}
		answerIs(t, got, step.KindRefused)
		if totp.count("challenge") != 0 {
			t.Error("the factor was asked for a challenge on a row that is pending")
		}
	})
}

func TestEnrollRefusesPastTheLimitPerKindAndCountsOnlyWhatIsNotDisabled(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS}, func(t *testing.T, r *rig) {
		r.store.set(maxPerKindPath, 2)
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return confirming("s"), nil
		}
		ctx := context.Background()
		r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
		r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
		r.insertRow("other", authn.FactorTOTP, authn.FactorActive, r.clock.read(), nil)
		r.insertRow(acct, authn.FactorSMS, authn.FactorActive, r.clock.read(), nil)

		// One active and one pending are two; the third is refused, and disabled rows
		// and other accounts' rows were no part of the count.
		r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if _, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{}); err != nil {
			t.Fatalf("the second factor of the kind: %v", err)
		}
		before := totp.count("enroll")
		got, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{})
		if !errors.Is(err, authn.ErrFactorLimit) {
			t.Fatalf("the third factor of the kind: Enroll = %v, %v, want ErrFactorLimit", step.KindOf(got), err)
		}
		if totp.count("enroll") != before {
			t.Error("the factor was called for a registration the limit refused")
		}
		if n := len(r.rows(acct)); n != 5 {
			t.Errorf("the account has %d rows, want the 5 there were and none from the refused call", n)
		}
		var denied []audit.Entry
		for _, e := range r.audit.entries() {
			if e.Action == "authn.factor.enroll" && e.Outcome == "denied" {
				denied = append(denied, e)
			}
		}
		if len(denied) != 1 {
			t.Errorf("%d denied audit rows, want 1", len(denied))
		}
		// The limit is per kind: the same account registers the other kind.
		r.store.set(maxPerKindPath, 2)
		if _, err := r.p.Enroll(ctx, flow(acct), authn.FactorSMS, authn.StepInput{}); err != nil {
			t.Errorf("a factor of another kind: Enroll = %v, want it allowed under its own count", err)
		}
	})
}

func TestEnrollRefusesAKindNobodyDelivers(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		for _, kind := range []authn.FactorKind{authn.FactorWebAuthn, "carrier-pigeon", ""} {
			_, err := r.p.Enroll(context.Background(), flow(acct), kind, authn.StepInput{})
			if !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("kind %q: Enroll = %v, want ErrInvalidArgument", kind, err)
			}
		}
		if n := len(r.rows(acct)); n != 0 {
			t.Errorf("%d rows were written for kinds that cannot be registered", n)
		}
		if _, err := r.p.Enroll(context.Background(), flow(""), authn.FactorTOTP, authn.StepInput{}); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("a registration for no account: Enroll = %v, want ErrInvalidArgument", err)
		}
	})
}

// A first step that does not move the registration on leaves no state for a later
// step to find, so the pending row and what the factor wrote for it go at once.
func TestAFirstStepThatDoesNotMoveOnLeavesNoPendingRow(t *testing.T) {
	cases := map[string]func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error){
		"refused": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.StepRefused(errors.New("no")), nil
		},
		"throttled": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.StepThrottled(time.Second), nil
		},
		"unavailable": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.StepUnavailable(errors.New("down")), nil
		},
		"an error": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.Step{}, errors.New("boom")
		},
		"a pass": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.StepPassed(authn.Pass{Methods: methodsOf(authn.FactorTOTP)}), nil
		},
		"no answer": func(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) { return authn.Step{}, nil },
	}
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		for name, fn := range cases {
			t.Run(name, func(t *testing.T) {
				totp := newFactor(authn.FactorTOTP)
				totp.enrollFn = fn
				r := newRig(t, h, totp)
				account := authn.AccountID("drop-" + strings.ReplaceAll(name, " ", "-"))
				got, err := r.p.Enroll(context.Background(), flow(account), authn.FactorTOTP, authn.StepInput{})
				if err != nil {
					t.Fatalf("Enroll: %v", err)
				}
				if k := step.KindOf(got); k == step.KindPrompt || k == step.KindEnrolled || k == step.KindData {
					t.Errorf("the answer is %v, which moves the registration on", k)
				}
				if n := len(r.rows(account)); n != 0 {
					t.Errorf("%d rows are left after a step that did not move on", n)
				}
				if len(totp.removed) != 1 {
					t.Errorf("the factor was asked to remove its data %d times, want once", len(totp.removed))
				}
				if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 0 {
					t.Errorf("%d events were published for a registration that did not happen", n)
				}
			})
		}
	})
}

func TestALaterStepFindsOnlyAPendingRowOfTheSameAccount(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(_ context.Context, f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
			if len(f.Private) == 0 {
				return confirming("s"), nil
			}
			return authn.StepEnrolled(authn.FactorRecord{}), nil
		}
		ctx := context.Background()
		first, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		private := step.PrivateOf(first)
		id := r.rows(acct)[0].ID
		sms := r.p.factors[authn.FactorSMS].(*fakeFactor)

		continued := func(account authn.AccountID, kind authn.FactorKind, private []byte) authn.Step {
			f := flow(account)
			f.Private = private
			got, err := r.p.Enroll(ctx, f, kind, authn.StepInput{})
			if err != nil {
				t.Fatalf("Enroll: %v", err)
			}
			return got
		}
		for name, got := range map[string]authn.Step{
			"another account's flow":       continued("intruder", authn.FactorTOTP, private),
			"a kind that is not the row's": continued(acct, authn.FactorSMS, private),
			"bytes of no envelope":         continued(acct, authn.FactorTOTP, []byte("not-an-envelope")),
			"a truncated envelope":         continued(acct, authn.FactorTOTP, private[:3]),
			"an envelope of no row":        continued(acct, authn.FactorTOTP, wrapPrivate("no-such-row", []byte("s"))),
		} {
			if k := step.KindOf(got); k != step.KindRefused {
				t.Errorf("%s: the answer is %v, want it refused", name, k)
			}
		}
		if totp.count("enroll") != 1 || sms.count("enroll") != 0 {
			t.Errorf("a factor was called for a continuation that was refused: totp %d, sms %d", totp.count("enroll"), sms.count("enroll"))
		}
		if status, _ := r.status(id); status != authn.FactorPending {
			t.Errorf("the row is %v after refused continuations, want it still pending", status)
		}

		// A row that is already active is not continued: no later step makes an
		// active row active again, or brings back a removed one.
		answerIs(t, continued(acct, authn.FactorTOTP, private), step.KindEnrolled)
		answerIs(t, continued(acct, authn.FactorTOTP, private), step.KindRefused)
		if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 1 {
			t.Errorf("%d events for one registration", n)
		}
	})
}

// A registration whose row was swept, reset or removed between its steps does not
// resurrect the row.
func TestAnActivationThatLostTheRaceIsRefused(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(_ context.Context, f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
			if len(f.Private) == 0 {
				return confirming("s"), nil
			}
			return authn.StepEnrolled(authn.FactorRecord{}), nil
		}
		ctx := context.Background()
		for name, take := range map[string]func(id string){
			"swept":    func(id string) { r.h.Exec("DELETE FROM authn_factors WHERE id = ?", id) },
			"disabled": func(id string) { r.h.Exec("UPDATE authn_factors SET status = 2 WHERE id = ?", id) },
		} {
			first, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{})
			if err != nil {
				t.Fatal(err)
			}
			rows := r.rows(acct)
			id := rows[len(rows)-1].ID
			take(id)
			f := flow(acct)
			f.Private = step.PrivateOf(first)
			got, err := r.p.Enroll(ctx, f, authn.FactorTOTP, authn.StepInput{})
			if err != nil {
				t.Fatal(err)
			}
			if k := step.KindOf(got); k != step.KindRefused {
				t.Errorf("%s: the answer is %v, want it refused", name, k)
			}
			if status, there := r.status(id); there && status == authn.FactorActive {
				t.Errorf("%s: the row was made active", name)
			}
		}
		if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 0 {
			t.Errorf("%d events for registrations that did not finish", n)
		}
	})
}

// The confirmation step is a guess at a code: it is held to the factor's back-off
// and a refusal is counted against the factor, where the first step, which guesses
// nothing, is neither.
func TestTheConfirmationStepIsHeldToTheFactorsBackOff(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.enrollFn = func(_ context.Context, f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
			if len(f.Private) == 0 {
				return confirming("s"), nil
			}
			return authn.StepRefused(errors.New("wrong code")), nil
		}
		ctx := context.Background()
		first, err := r.p.Enroll(ctx, flow(acct), authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		if calls := r.throttle.callList(); len(calls) != 0 {
			t.Errorf("the first step touched the back-off: %v", calls)
		}
		id := r.rows(acct)[0].ID
		f := flow(acct)
		f.Private = step.PrivateOf(first)

		got, _ := r.p.Enroll(ctx, f, authn.FactorTOTP, authn.StepInput{})
		answerIs(t, got, step.KindRefused)
		if want := []string{"check:factor:" + id, "fail:factor:" + id}; fmt.Sprint(r.throttle.callList()) != fmt.Sprint(want) {
			t.Errorf("the back-off calls are %v, want %v", r.throttle.callList(), want)
		}
		if status, there := r.status(id); !there || status != authn.FactorPending {
			t.Error("a wrong confirmation removed or changed the pending row, which the user is to try again on")
		}

		r.throttle.waits[id] = 45 * time.Second
		calls := totp.count("enroll")
		got, _ = r.p.Enroll(ctx, f, authn.FactorTOTP, authn.StepInput{})
		answerIs(t, got, step.KindThrottled)
		if step.RetryAfterOf(got) != 45*time.Second {
			t.Errorf("the wait is %v, want 45s", step.RetryAfterOf(got))
		}
		if totp.count("enroll") != calls {
			t.Error("the factor was called while it was in back-off")
		}
	})
}

// Verify is the one place a failure is counted and a pass clears the count.
func TestVerifyThrottlesBeforeAskingAndCountsTheOutcome(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		ctx := context.Background()
		verify := func() authn.Step {
			got, err := r.p.Verify(ctx, flow(acct), id, authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{"code": "424242"}})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			return got
		}

		// A pass: checked, asked, cleared, the use recorded.
		answerIs(t, verify(), step.KindPassed)
		if want := []string{"check:factor:" + id, "succeed:factor:" + id}; fmt.Sprint(r.throttle.callList()) != fmt.Sprint(want) {
			t.Errorf("the back-off calls for a pass are %v, want %v", r.throttle.callList(), want)
		}
		if used := r.rows(acct)[0].LastUsedAt; !used.Equal(r.clock.read()) {
			t.Errorf("the factor was last used at %v, want %v", used, r.clock.read())
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("factor", "totp"), attribute.String("outcome", "ok")); got != 1 {
			t.Errorf("the counter of passes is %d, want 1", got)
		}

		// A refusal is counted against the factor and not cleared.
		totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepRefused(errors.New("wrong code")), nil
		}
		r.throttle.calls = nil
		answerIs(t, verify(), step.KindRefused)
		if want := []string{"check:factor:" + id, "fail:factor:" + id}; fmt.Sprint(r.throttle.callList()) != fmt.Sprint(want) {
			t.Errorf("the back-off calls for a refusal are %v, want %v", r.throttle.callList(), want)
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("outcome", "bad")); got != 1 {
			t.Errorf("the counter of refusals is %d, want 1", got)
		}
		if n := len(r.bus.ofType(authn.EventThrottleEngaged)); n != 0 {
			t.Errorf("a first failure published %d back-off events", n)
		}
	})
}

func TestAFactorInBackOffIsNotAskedAndNothingIsCounted(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.throttle.waits[id] = 90 * time.Second

		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, got, step.KindThrottled)
		if step.RetryAfterOf(got) != 90*time.Second {
			t.Errorf("the wait is %v, want the 90s the back-off reported", step.RetryAfterOf(got))
		}
		if totp.count("verify") != 0 {
			t.Error("the factor derived and compared for an attempt that was in back-off")
		}
		if r.throttle.count("fail") != 0 || r.throttle.count("succeed") != 0 {
			t.Errorf("an attempt that was not made was counted: %v", r.throttle.callList())
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("outcome", "throttled")); got != 1 {
			t.Errorf("the counter of throttled attempts is %d, want 1", got)
		}

		chal, err := r.p.Challenge(context.Background(), flow(acct), id)
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, chal, step.KindThrottled)
		if totp.count("challenge") != 0 {
			t.Error("a code was sent to a factor in back-off")
		}
	})
}

// A back-off that cannot be read must not let an attempt through: the call ends
// as an outage of the factor and the factor is not asked, because a state that
// could not be read is not the same as no back-off.
func TestAFactorWhoseBackOffCannotBeReadIsNotAsked(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.throttle.checkErr = errors.New("the back-off store is down")

		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, got, step.KindUnavailable)
		if totp.count("verify") != 0 {
			t.Error("the factor was asked although its back-off could not be read")
		}
		if r.throttle.count("fail") != 0 || r.throttle.count("succeed") != 0 {
			t.Errorf("an attempt that was not made was counted: %v", r.throttle.callList())
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("outcome", "unavailable")); got != 1 {
			t.Errorf("the counter of attempts whose back-off could not be read is %d, want 1", got)
		}

		chal, err := r.p.Challenge(context.Background(), flow(acct), id)
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, chal, step.KindUnavailable)
		if totp.count("challenge") != 0 {
			t.Error("a code was sent although the factor's back-off could not be read")
		}
	})
}

// A register that cannot be read or written is an outage, not a refusal: a check
// or a registration ends with the factor unavailable and the factor is not asked,
// so a client is never told its code was wrong when nothing was checked.
func TestAnUnreadableRegisterAnswersAnOutageAndAsksNoFactor(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if err := r.h.Migrator().DropTable(factorTable); err != nil {
			t.Fatal(err)
		}

		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, got, step.KindUnavailable)
		if totp.count("verify") != 0 {
			t.Error("the factor was asked although the register could not be read")
		}

		// A registration whose pending row cannot be written is the same outage.
		enr, err := r.p.Enroll(context.Background(), flow(acct), authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, enr, step.KindUnavailable)
		if totp.count("enroll") != 0 {
			t.Error("the factor was asked although its pending row could not be written")
		}
	})
}

func TestTheFailureThatStartsABackOffIsAnnouncedOnceAndCounted(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepRefused(authn.ErrInvalidCredentials), nil
		}
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		for i := 1; i <= 3; i++ {
			if _, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if i == 3 {
				want = 1
			}
			if n := len(r.bus.ofType(authn.EventThrottleEngaged)); n != want {
				t.Fatalf("after %d failures %d back-off events, want %d", i, n, want)
			}
		}
		var payload authn.ThrottleEngaged
		r.bus.payload(t, authn.EventThrottleEngaged, &payload)
		if payload != (authn.ThrottleEngaged{Scope: "factor", AccountID: string(acct)}) {
			t.Errorf("the event is %+v, want the factor scope and the account", payload)
		}
		if got := r.counter("authn.throttle.engagements", attribute.String("scope", "factor")); got != 1 {
			t.Errorf("the engagement counter is %d, want 1", got)
		}
		lines := r.logs.lines("a factor was put into back-off", "reason_class=throttled")
		if len(lines) != 1 || strings.Contains(lines[0], "level=ERROR") {
			t.Errorf("the log lines for the back-off are %q, want one that is not an Error", lines)
		}
		// The fourth attempt is in back-off and is not counted again.
		got, _ := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		answerIs(t, got, step.KindThrottled)
		if r.throttle.count("fail") != 3 {
			t.Errorf("%d failures were counted, want the 3 that were attempts", r.throttle.count("fail"))
		}
	})
}

func TestAPassClearsTheFailuresSoTheNextRunStartsOver(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		refuse := func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepRefused(errors.New("wrong")), nil
		}
		pass := func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepPassed(authn.Pass{Methods: methodsOf(authn.FactorTOTP)}), nil
		}
		run := func(fn func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error)) {
			totp.verifyFn = fn
			if _, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{}); err != nil {
				t.Fatal(err)
			}
		}
		run(refuse)
		run(refuse)
		run(pass)
		run(refuse)
		run(refuse)
		if n := len(r.bus.ofType(authn.EventThrottleEngaged)); n != 0 {
			t.Errorf("four failures split by a pass began %d back-offs, want none: the pass clears the count", n)
		}
		run(refuse)
		if n := len(r.bus.ofType(authn.EventThrottleEngaged)); n != 1 {
			t.Errorf("the third failure since the pass began %d back-offs, want 1", n)
		}
	})
}

func TestAReplayIsCountedAsAFailureAndReportedAsAReplay(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepRefused(fmt.Errorf("%w: counter used", authn.ErrReplay)), nil
		}
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if _, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{}); err != nil {
			t.Fatal(err)
		}
		if r.throttle.count("fail") != 1 {
			t.Error("a replay was not counted against the factor")
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("outcome", "replay")); got != 1 {
			t.Errorf("the replay counter is %d, want 1", got)
		}
		if got := r.counter("authn.mfa.verifications", attribute.String("outcome", "bad")); got != 0 {
			t.Errorf("the bad counter is %d for a replay, want 0", got)
		}
	})
}

func TestOnlyAnActiveFactorOfTheAccountIsAskedToVerify(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		active := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		pending := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read(), nil)
		disabled := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
		theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)

		for name, c := range map[string]struct {
			account authn.AccountID
			id      string
		}{
			"a pending factor":                    {acct, pending},
			"a disabled factor":                   {acct, disabled},
			"another account's factor":            {acct, theirs},
			"a factor that is not there":          {acct, "no-such-factor"},
			"no factor named":                     {acct, ""},
			"no account":                          {"", active},
			"the right factor, the wrong account": {"someone-else", active},
		} {
			got, err := r.p.Verify(context.Background(), flow(c.account), c.id, authn.StepInput{})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrFactorNotEnrolled) {
				t.Errorf("%s: the answer is %v (%v), want a refusal as a factor not enrolled", name, step.KindOf(got), step.CauseOf(got))
			}
		}
		if totp.count("verify") != 0 || len(r.throttle.callList()) != 0 {
			t.Errorf("a factor that is not the account's active one reached the factor or the back-off: %v %v", totp.callList(), r.throttle.callList())
		}
		// And the one that is, for contrast.
		got, _ := r.p.Verify(context.Background(), flow(acct), active, authn.StepInput{})
		answerIs(t, got, step.KindPassed)
	})
}

func TestAKindWithNoImplementationIsRefusedAndNeverCounted(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorWebAuthn, time.Hour)
		for name, call := range map[string]func() (authn.Step, error){
			"Verify":    func() (authn.Step, error) { return r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{}) },
			"Challenge": func() (authn.Step, error) { return r.p.Challenge(context.Background(), flow(acct), id) },
		} {
			got, err := call()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrNoFactor) {
				t.Errorf("%s: the answer is %v (%v), want a refusal that is ErrNoFactor", name, step.KindOf(got), step.CauseOf(got))
			}
		}
		if len(r.throttle.callList()) != 0 {
			t.Errorf("the back-off was touched for a factor nobody can verify: %v", r.throttle.callList())
		}
	})
}

func TestAnAnswerAFactorMayNotGiveIsAFaultOfTheFactor(t *testing.T) {
	good := authn.Pass{Methods: methodsOf(authn.FactorTOTP)}
	answers := map[string]authn.Step{
		"a pass that names no method":        authn.StepPassed(authn.Pass{}),
		"a pass that names another's method": authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodSMS}}),
		"a pass that adds a method":          authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodTOTP, authn.MethodPassword}}),
		"a proof":                            authn.StepProven(authn.Proof{Methods: []authn.Method{authn.MethodPassword}}),
		"a registration":                     authn.StepEnrolled(authn.FactorRecord{}),
		"a redirect":                         authn.StepRedirect("x", nil, "https://example.com/"),
		"no answer":                          {},
	}
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		for name, answer := range answers {
			totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
				return answer, nil
			}
			r.throttle.calls = nil
			got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if step.KindOf(got) != step.KindUnavailable {
				t.Errorf("%s: Verify answered %v, want an outage of the factor and not a pass or a refusal", name, step.KindOf(got))
			}
			if r.throttle.count("succeed") != 0 || r.throttle.count("fail") != 0 {
				t.Errorf("%s: the defect was counted as a pass or a failure: %v", name, r.throttle.callList())
			}
		}
		if lines := r.logs.lines("level=ERROR", "may not give"); len(lines) != len(answers) {
			t.Errorf("%d Error lines for %d defects", len(lines), len(answers))
		}
		// A factor that errors is the same outage.
		totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepPassed(good), errors.New("crashed")
		}
		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil || step.KindOf(got) != step.KindUnavailable {
			t.Errorf("a factor that returned an error: Verify = %v, %v, want an outage even though it also returned a pass", step.KindOf(got), err)
		}
	})
}

func TestAnOutageOfTheFactorIsNotCountedAsAGuess(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		totp.verifyFn = func(context.Context, authn.FlowState, authn.FactorRecord, authn.StepInput) (authn.Step, error) {
			return authn.StepUnavailable(errors.New("the seed store is down")), nil
		}
		got, err := r.p.Verify(context.Background(), flow(acct), id, authn.StepInput{})
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, got, step.KindUnavailable)
		if r.throttle.count("fail") != 0 {
			t.Error("an outage was counted as a failed attempt, which would lock a user out for the factor's fault")
		}
		if r.counter("authn.mfa.verifications", attribute.String("outcome", "unavailable")) != 1 {
			t.Error("the outage was not counted as unavailable")
		}
		// It is not a refusal: the two are told apart by the engine.
		if step.KindOf(got) == step.KindRefused {
			t.Error("an outage answered as a refusal")
		}
	})
}

func TestChallengePassesTheFactorsOwnPromptAndCountsNothing(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorSMS}, func(t *testing.T, r *rig) {
		sms := r.p.factors[authn.FactorSMS].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorSMS, time.Hour)
		sms.challengeFn = func(context.Context, authn.FlowState, authn.FactorRecord) (authn.Step, error) {
			return authn.StepPrompt("code", []byte("p"), authn.Field{Name: "code", Kind: authn.FieldCode}).WithSends(1), nil
		}
		got, err := r.p.Challenge(context.Background(), flow(acct), id)
		if err != nil {
			t.Fatal(err)
		}
		answerIs(t, got, step.KindPrompt)
		if step.SendsOf(got) != 1 || string(step.PrivateOf(got)) != "p" || step.NameOf(got) != "code" {
			t.Errorf("the answer was changed on the way: sends %d, private %q, name %q", step.SendsOf(got), step.PrivateOf(got), step.NameOf(got))
		}
		if r.throttle.count("check") != 1 || r.throttle.count("fail") != 0 || r.throttle.count("succeed") != 0 {
			t.Errorf("the back-off calls are %v, want only the check", r.throttle.callList())
		}
		for name, bad := range map[string]authn.Step{
			"a pass": authn.StepPassed(authn.Pass{Methods: methodsOf(authn.FactorSMS)}), "a registration": authn.StepEnrolled(authn.FactorRecord{}),
			"a redirect": authn.StepRedirect("x", nil, "https://example.com"), "no answer": {},
		} {
			sms.challengeFn = func(context.Context, authn.FlowState, authn.FactorRecord) (authn.Step, error) { return bad, nil }
			got, err := r.p.Challenge(context.Background(), flow(acct), id)
			if err != nil || step.KindOf(got) != step.KindUnavailable {
				t.Errorf("%s: Challenge = %v, %v, want an outage of the factor", name, step.KindOf(got), err)
			}
		}
		sms.challengeFn = func(context.Context, authn.FlowState, authn.FactorRecord) (authn.Step, error) {
			return authn.Step{}, errors.New("sender crashed")
		}
		if got, err := r.p.Challenge(context.Background(), flow(acct), id); err != nil || step.KindOf(got) != step.KindUnavailable {
			t.Errorf("a factor that errored: Challenge = %v, %v, want an outage", step.KindOf(got), err)
		}
	})
}

// What the user typed, and the codes and seeds the factors handle, are in no log
// line the module writes.
func TestNoSecretReachesALogLine(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		const typed = "code-424242-typed"
		totp.verifyFn = func(_ context.Context, _ authn.FlowState, _ authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
			if in.Fields["code"] == typed {
				return authn.StepRefused(authn.ErrInvalidCredentials), nil
			}
			return authn.Step{}, nil
		}
		in := authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{"code": typed}}
		for i := 0; i < 4; i++ {
			if _, err := r.p.Verify(context.Background(), flow(acct), id, in); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.p.Enroll(context.Background(), flow(acct), authn.FactorTOTP, in); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.logs.String(), typed) {
			t.Errorf("the log holds what the user typed:\n%s", r.logs.String())
		}
		if r.logs.String() == "" {
			t.Error("nothing was logged, so the case proved nothing")
		}
	})
}

// A max-per-kind that cannot be read reads as its declared default of five: a
// first registration goes through. Reading the type's zero value would set the
// limit to zero and refuse every registration, since a count of zero is at a
// limit of zero.
func TestEnrollingUnderAnUnreadableMaxPerKindUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.store.fail(maxPerKindPath, errors.New("settings: not loaded"))
		if _, err := r.p.Enroll(context.Background(), flow(acct), authn.FactorTOTP, authn.StepInput{}); err != nil {
			t.Fatalf("the first factor under an unreadable setting: Enroll = %v, want it allowed", err)
		}
	})
}

// A max-per-kind outside its domain reads as the declared default of five. A
// stored zero would refuse the first registration instead of admitting the fifth;
// a stored twenty-one would admit a sixth.
func TestAnOutOfDomainMaxPerKindUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.store.set(maxPerKindPath, 0)
		if _, err := r.p.Enroll(ctx, flow("zero"), authn.FactorTOTP, authn.StepInput{}); err != nil {
			t.Fatalf("with 0 stored, the first Enroll = %v, want it allowed under the declared default of %d",
				err, defaults.MaxPerKind)
		}
		r.store.set(maxPerKindPath, maxMaxPerKind+1)
		for i := 0; i < defaults.MaxPerKind; i++ {
			r.insertRow("over", authn.FactorTOTP, authn.FactorActive, r.clock.read(), nil)
		}
		if _, err := r.p.Enroll(ctx, flow("over"), authn.FactorTOTP, authn.StepInput{}); !errors.Is(err, authn.ErrFactorLimit) {
			t.Errorf("with %d stored, the sixth Enroll = %v, want ErrFactorLimit under the declared default of %d",
				maxMaxPerKind+1, err, defaults.MaxPerKind)
		}
	})
}

// An empty identifier is refused before the register is read: a factor that is
// not named, or one of no account, is not the account's, so the answer is the
// refusal of a factor that is not enrolled and no read is made. The register is
// dropped so that a read would answer an outage, which is what tells the refusal
// apart from a failed read.
func TestAnEmptyIdentifierIsRefusedWithoutReadingTheRegister(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		if err := r.h.Migrator().DropTable(factorTable); err != nil {
			t.Fatal(err)
		}
		cases := map[string]func() (authn.Step, error){
			"Verify with no factor identifier": func() (authn.Step, error) {
				return r.p.Verify(context.Background(), flow(acct), "", authn.StepInput{})
			},
			"Verify with no account": func() (authn.Step, error) {
				return r.p.Verify(context.Background(), flow(""), "some-factor", authn.StepInput{})
			},
			"Challenge with no factor identifier": func() (authn.Step, error) {
				return r.p.Challenge(context.Background(), flow(acct), "")
			},
			"Challenge with no account": func() (authn.Step, error) {
				return r.p.Challenge(context.Background(), flow(""), "some-factor")
			},
		}
		for name, call := range cases {
			got, err := call()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if k := step.KindOf(got); k != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrFactorNotEnrolled) {
				t.Errorf("%s = %v (cause %v), want a refusal of ErrFactorNotEnrolled and no register read", name, k, step.CauseOf(got))
			}
		}
	})
}
