package mfa

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

func asAccount(t *testing.T, id authn.AccountID) context.Context {
	t.Helper()
	return authntest.Context(t, context.Background(), authntest.WithAccount(id))
}

func TestRemoveHasTheFactorCleanItsDataBeforeTheRowGoes(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		other := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
		var rowDuringRemove bool
		totp.removeFn = func(_ context.Context, account authn.AccountID, rec authn.FactorRecord) error {
			_, rowDuringRemove = r.status(id)
			if account != acct || rec.ID != id || rec.Kind != authn.FactorTOTP {
				t.Errorf("the factor was asked to remove %+v for %q, want the account's row", rec, account)
			}
			return nil
		}

		if err := r.p.Remove(asAccount(t, acct), acct, id); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if !rowDuringRemove {
			t.Error("the row was already gone when the factor was asked to clean its data: a failure there would have left data nobody can find")
		}
		if _, there := r.status(id); there {
			t.Error("the row is still there")
		}
		if _, there := r.status(other); !there {
			t.Error("another account's row went with it")
		}
		var payload authn.FactorChanged
		r.bus.payload(t, authn.EventFactorChanged, &payload)
		if payload != (authn.FactorChanged{AccountID: string(acct), FactorID: id, Kind: "totp", Change: "removed", By: "self"}) {
			t.Errorf("the event is %+v", payload)
		}
		entries := r.audit.entries()
		if len(entries) != 1 || entries[0].Action != "authn.factor.remove" || entries[0].Outcome != "success" || entries[0].Detail["factor_id"] != id {
			t.Errorf("the audit rows are %+v, want the one best-effort remove row", entries)
		}
	})
}

func TestRemoveThatFailsInTheFactorLeavesTheRowForARetry(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		totp.removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error { return errors.New("seed store down") }

		err := r.p.Remove(context.Background(), acct, id)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("Remove = %v, want ErrUnavailable", err)
		}
		if status, there := r.status(id); !there || status != authn.FactorActive {
			t.Error("the row was changed by a removal that did not finish")
		}
		if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 0 {
			t.Errorf("%d events for a removal that did not happen", n)
		}
		totp.removeFn = nil
		if err := r.p.Remove(context.Background(), acct, id); err != nil {
			t.Errorf("the retry: %v", err)
		}
		if _, there := r.status(id); there {
			t.Error("the retry left the row")
		}
	})
}

func TestTheLastFactorOfAnAccountThatRequiresOneStays(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		r.accounts.required[acct] = true
		first := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		second := r.activeFactor(acct, authn.FactorSMS, time.Hour)
		pending := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read(), nil)
		ctx := context.Background()

		// With a second active one the first may go.
		if err := r.p.Remove(ctx, acct, first); err != nil {
			t.Fatalf("removing one of two: %v", err)
		}
		// A pending row is not a factor the account can use, so the last active one stays.
		before := totp.count("remove")
		err := r.p.Remove(ctx, acct, second)
		if !errors.Is(err, authn.ErrLastFactor) {
			t.Fatalf("removing the last active factor: Remove = %v, want ErrLastFactor", err)
		}
		if status, there := r.status(second); !there || status != authn.FactorActive {
			t.Error("the last factor was changed by a removal that was refused")
		}
		if r.p.factors[authn.FactorSMS].(*fakeFactor).count("remove") != 0 || totp.count("remove") != before {
			t.Error("the factor was asked to clean data of a row that stayed")
		}
		denied := 0
		for _, e := range r.audit.entries() {
			if e.Action == "authn.factor.remove" && e.Outcome == "denied" {
				denied++
			}
		}
		if denied != 1 {
			t.Errorf("%d denied rows, want 1", denied)
		}
		// A row that is not active never takes strength away.
		if err := r.p.Remove(ctx, acct, pending); err != nil {
			t.Errorf("removing a pending row: %v", err)
		}
		// An account that does not require one may remove its last.
		r.accounts.required[acct] = false
		if err := r.p.Remove(ctx, acct, second); err != nil {
			t.Errorf("removing the last factor of an account that does not require one: %v", err)
		}
	})
}

func TestRemoveOnlyReachesTheAccountsOwnFactors(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
		for _, id := range []string{theirs, "no-such-factor", ""} {
			if err := r.p.Remove(context.Background(), acct, id); !errors.Is(err, authn.ErrFactorNotEnrolled) {
				t.Errorf("Remove(%q) = %v, want ErrFactorNotEnrolled", id, err)
			}
		}
		if err := r.p.Remove(context.Background(), "", theirs); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("no account: Remove = %v, want ErrInvalidAccountID", err)
		}
		if _, there := r.status(theirs); !there {
			t.Error("another account's factor was removed")
		}
		if r.p.factors[authn.FactorTOTP].(*fakeFactor).count("remove") != 0 {
			t.Error("the factor was asked to clean the data of a row that is not the account's")
		}
	})
}

func TestRemoveRefusesWhenTheAccountCannotBeReadAndTheRowIsActive(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.accounts.getErr = errors.New("account store down")
		if err := r.p.Remove(context.Background(), acct, id); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("Remove = %v, want ErrUnavailable: whether the removal is allowed is not known", err)
		}
		if _, there := r.status(id); !there {
			t.Error("the row was removed although the policy could not be read")
		}
	})
}

func TestRemoveOfAKindNobodyDeliversStillRemovesTheRow(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorWebAuthn, time.Hour)
		if err := r.p.Remove(context.Background(), acct, id); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, there := r.status(id); there {
			t.Error("the row stayed")
		}
		if len(r.logs.lines("level=WARN", "no implementation")) != 1 {
			t.Errorf("the log holds %q, want one Warn that says what stays behind", r.logs.String())
		}
	})
}

func TestTheEventNamesWhoActed(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		cases := []struct {
			name string
			ctx  context.Context
			want string
		}{
			{"the account itself", asAccount(t, acct), "self"},
			{"another account", asAccount(t, "admin-1"), "admin"},
			{"no identity in the request", context.Background(), "system"},
		}
		for _, c := range cases {
			id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
			if err := r.p.Remove(c.ctx, acct, id); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			events := r.bus.ofType(authn.EventFactorChanged)
			var payload authn.FactorChanged
			if err := decode(events[len(events)-1].Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.By != c.want {
				t.Errorf("%s: by = %q, want %q", c.name, payload.By, c.want)
			}
		}
	})
}

func TestAnEventThatCannotBePublishedDoesNotFailTheRemoval(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.bus.err = errors.New("bus down")
		r.audit.err = errors.New("audit down")
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if err := r.p.Remove(context.Background(), acct, id); err != nil {
			t.Errorf("Remove = %v, want the removal to stand: the event and the audit row of it are best effort", err)
		}
		if _, there := r.status(id); there {
			t.Error("the row stayed")
		}
		if len(r.logs.lines("level=WARN", "event could not be published")) != 1 {
			t.Error("the failed publish was not a Warn")
		}
		if len(r.logs.lines("level=ERROR", "audit row could not be written")) != 1 {
			t.Error("the failed audit row was not an Error")
		}
		if got := r.counter("authn.audit.write-failures"); got != 1 {
			t.Errorf("the audit failure counter is %d, want 1", got)
		}
	})
}

// Reset leaves a failure at any step with the account stricter than before. Each
// step reads what the previous one left, which is the evidence of the order.
func TestResetRunsItsStepsInOrder(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS}, func(t *testing.T, r *rig) {
		a := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		b := r.activeFactor(acct, authn.FactorSMS, time.Hour)
		c := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read(), nil)
		d := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
		other := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
		statuses := func() []authn.FactorStatus {
			out := make([]authn.FactorStatus, 0, 4)
			for _, id := range []string{a, b, c, d} {
				s, _ := r.status(id)
				out = append(out, s)
			}
			return out
		}
		r.admin.onSet = func() {
			if got := statuses(); !slices.Equal(got, []authn.FactorStatus{authn.FactorActive, authn.FactorActive, authn.FactorPending, authn.FactorDisabled}) {
				t.Errorf("when the flag is set the rows are %v: the flag is to be set first", got)
			}
		}
		r.sessions.onRevoke = func() {
			if got := statuses(); !slices.Equal(got, []authn.FactorStatus{authn.FactorDisabled, authn.FactorDisabled, authn.FactorDisabled, authn.FactorDisabled}) {
				t.Errorf("when the sessions are revoked the rows are %v, want them all disabled first", got)
			}
			if !r.accounts.required[acct] {
				t.Error("when the sessions are revoked the account does not require a factor yet")
			}
			if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 0 {
				t.Errorf("the change was published before the sessions were revoked (%d events)", n)
			}
		}

		removed, err := r.p.Reset(context.Background(), acct, "lost the phone")
		if err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if removed != 3 {
			t.Errorf("Reset reported %d, want the 3 rows it disabled (the one already disabled is not counted)", removed)
		}
		if !r.accounts.required[acct] || len(r.admin.calls) != 1 {
			t.Errorf("the account flag is %v after %v", r.accounts.required[acct], r.admin.calls)
		}
		if want := []string{`revoke-account:acct-1:except="":factor-reset`}; !slices.Equal(r.sessions.revoked, want) {
			t.Errorf("the sessions were revoked as %v, want %v", r.sessions.revoked, want)
		}
		if s, _ := r.status(other); s != authn.FactorActive {
			t.Error("another account's factor was disabled")
		}
		var payload authn.FactorChanged
		r.bus.payload(t, authn.EventFactorChanged, &payload)
		if payload.Change != "reset" || payload.AccountID != string(acct) || payload.By != "system" {
			t.Errorf("the event is %+v", payload)
		}
		if strings.Contains(r.logs.String(), "lost the phone") {
			t.Error("the free-text reason reached the log")
		}
		for _, e := range r.audit.entries() {
			if strings.Contains(e.Purpose+e.Action+e.Outcome, "lost the phone") {
				t.Errorf("the free-text reason reached an audit row: %+v", e)
			}
		}
	})
}

func TestResetThatFailsAtAStepLeavesTheAccountStricterAndCanBeRepeated(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		ctx := context.Background()

		// Step 1 fails: nothing else happened.
		r.admin.err = errors.New("account store down")
		if _, err := r.p.Reset(ctx, acct, ""); !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Reset = %v, want ErrUnavailable", err)
		}
		if s, _ := r.status(id); s != authn.FactorActive || len(r.sessions.revoked) != 0 || len(r.bus.ofType(authn.EventFactorChanged)) != 0 {
			t.Errorf("a failed first step still changed the row to %v, revoked %v or published", s, r.sessions.revoked)
		}
		r.admin.err = nil

		// Step 3 fails: the flag is set and the factors are disabled, which is
		// stricter than before: the account has to register again and cannot use
		// the factors it had. No event yet.
		r.sessions.err = errors.New("session store down")
		if _, err := r.p.Reset(ctx, acct, ""); !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Reset = %v, want ErrUnavailable", err)
		}
		if s, _ := r.status(id); s != authn.FactorDisabled || !r.accounts.required[acct] {
			t.Errorf("after a failed revocation the row is %v and the flag %v, want the stricter state", s, r.accounts.required[acct])
		}
		if len(r.bus.ofType(authn.EventFactorChanged)) != 0 {
			t.Error("the change was published although the reset did not finish")
		}

		// The repeat finishes it: nothing left to disable, the sessions revoked
		// again, the event published once.
		r.sessions.err = nil
		removed, err := r.p.Reset(ctx, acct, "")
		if err != nil {
			t.Fatalf("the repeat: %v", err)
		}
		if removed != 0 {
			t.Errorf("the repeat disabled %d rows, want 0: the rows were disabled already", removed)
		}
		if len(r.sessions.revoked) != 2 {
			t.Errorf("the sessions were revoked %d times, want 2: the attempt that failed there and the repeat, and none from the one that failed before it", len(r.sessions.revoked))
		}
		if n := len(r.bus.ofType(authn.EventFactorChanged)); n != 1 {
			t.Errorf("%d events, want the one of the finished reset", n)
		}
		// A third call changes nothing more.
		if _, err := r.p.Reset(ctx, acct, ""); err != nil {
			t.Fatalf("a third call: %v", err)
		}
		if s, _ := r.status(id); s != authn.FactorDisabled || !r.accounts.required[acct] {
			t.Error("a repeated reset changed the state a finished one leaves")
		}
	})
}

func TestResetStopsAtADisableThatFailsBeforeAnySessionIsRevoked(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.admin.onSet = func() { r.h.Exec("DROP TABLE authn_factors") }
		_, err := r.p.Reset(context.Background(), acct, "")
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Reset = %v, want ErrUnavailable", err)
		}
		if !r.accounts.required[acct] {
			t.Error("the flag was not set before the step that failed")
		}
		if len(r.sessions.revoked) != 0 || len(r.bus.ofType(authn.EventFactorChanged)) != 0 {
			t.Error("sessions were revoked or the change published after the factors could not be disabled")
		}
	})
}

func TestResetWithoutASessionModuleStillStopsTheFactorsAndSaysSo(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.p.sessions = nil
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		removed, err := r.p.Reset(context.Background(), acct, "")
		if err != nil || removed != 1 {
			t.Fatalf("Reset = %d, %v, want 1 and no error", removed, err)
		}
		if s, _ := r.status(id); s != authn.FactorDisabled {
			t.Errorf("the factor is %v, want disabled", s)
		}
		if len(r.logs.lines("level=WARN", "sessions stay valid")) != 1 {
			t.Error("nothing said the sessions stay valid")
		}
	})
}

func TestResetOfAnAccountThatIsNotThereChangesNothing(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.admin.err = authn.ErrAccountNotFound
		if _, err := r.p.Reset(context.Background(), acct, ""); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("Reset = %v, want ErrAccountNotFound", err)
		}
		if s, _ := r.status(id); s != authn.FactorActive {
			t.Error("the factors of an account that was not found were disabled")
		}
		if _, err := r.p.Reset(context.Background(), "", ""); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("no account: Reset = %v, want ErrInvalidAccountID", err)
		}
	})
}

func TestSeasonedNeedsANonSMSActiveFactorOldEnough(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		minAge := 24 * time.Hour
		ctx := context.Background()
		check := func(account authn.AccountID) bool {
			ok, err := r.p.Seasoned(ctx, account)
			if err != nil {
				t.Fatalf("Seasoned(%s): %v", account, err)
			}
			return ok
		}
		active := func(account authn.AccountID, kind authn.FactorKind, age time.Duration) {
			r.activeFactor(account, kind, age)
		}

		active("exactly", authn.FactorTOTP, minAge)
		active("just-short", authn.FactorTOTP, minAge-time.Second)
		active("old-passkey", authn.FactorWebAuthn, 30*24*time.Hour)
		active("sms-only", authn.FactorSMS, 90*24*time.Hour)
		active("sms-old-totp-young", authn.FactorSMS, 90*24*time.Hour)
		active("sms-old-totp-young", authn.FactorTOTP, time.Hour)
		active("sms-and-old-totp", authn.FactorSMS, time.Hour)
		active("sms-and-old-totp", authn.FactorTOTP, 2*minAge)
		r.insertRow("pending-old", authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-100*time.Hour), nil)
		old := r.clock.read().Add(-100 * time.Hour)
		r.insertRow("disabled-old", authn.FactorTOTP, authn.FactorDisabled, old, &old)

		for account, want := range map[authn.AccountID]bool{
			"exactly":            true,
			"just-short":         false,
			"old-passkey":        true,
			"sms-only":           false,
			"sms-old-totp-young": false,
			"sms-and-old-totp":   true,
			"pending-old":        false,
			"disabled-old":       false,
			"nobody":             false,
		} {
			if got := check(account); got != want {
				t.Errorf("Seasoned(%s) = %v, want %v", account, got, want)
			}
		}
		// Time moving on seasons a factor; the setting moves the line.
		r.clock.advance(2 * time.Second)
		if !check("just-short") {
			t.Error("a factor that has been active long enough since is not seasoned")
		}
		r.store.set(sensitiveMinAgePath, 200*time.Hour)
		if check("old-passkey") != true {
			t.Error("a factor 30 days old is not seasoned under a setting of 200 hours")
		}
		r.store.set(sensitiveMinAgePath, 1000*time.Hour)
		if !check("old-passkey") {
			// 1000h is outside the domain (720h at most): the declared default of 24h applies.
			t.Error("a setting outside its domain was acted on instead of the default")
		}
		if len(r.logs.lines("level=ERROR", sensitiveMinAgePath)) != 1 {
			t.Errorf("a setting outside its domain gave %d Error lines, want 1", len(r.logs.lines("level=ERROR", sensitiveMinAgePath)))
		}
		if _, err := r.p.Seasoned(ctx, ""); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("no account: Seasoned = %v, want ErrInvalidAccountID", err)
		}
	})
}

func TestFactorsListsEveryStatusOfTheAccountAndNoOtherAccount(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		a := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		b := r.insertRow(acct, authn.FactorSMS, authn.FactorPending, r.clock.read(), nil)
		c := r.insertRow(acct, authn.FactorWebAuthn, authn.FactorDisabled, r.clock.read().Add(time.Second), nil)
		r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
		recs := r.rows(acct)
		ids := make([]string, len(recs))
		for i, rec := range recs {
			ids[i] = rec.ID
			if rec.Account != acct {
				t.Errorf("the list holds %+v of another account", rec)
			}
		}
		if !slices.Equal(ids, []string{a, b, c}) {
			t.Errorf("the list is %v, want the three rows oldest first", ids)
		}
		if recs[0].Status != authn.FactorActive || recs[1].Status != authn.FactorPending || recs[2].Status != authn.FactorDisabled {
			t.Errorf("the statuses are %v %v %v", recs[0].Status, recs[1].Status, recs[2].Status)
		}
		if recs[0].Kind != authn.FactorTOTP || recs[1].Kind != authn.FactorSMS || recs[2].Kind != authn.FactorWebAuthn {
			t.Error("the kinds were not read back")
		}
		if recs[0].ActivatedAt.IsZero() || !recs[1].ActivatedAt.IsZero() {
			t.Error("the activation time is not what the rows hold")
		}
		if _, err := r.p.Factors(context.Background(), ""); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("no account: Factors = %v", err)
		}
	})
}

func TestKindsListsWhatIsTakenInAndIsACopy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorWebAuthn), newFactor(authn.FactorTOTP))
		got := r.p.Kinds()
		if !slices.Equal(got, []authn.FactorKind{authn.FactorTOTP, authn.FactorWebAuthn}) {
			t.Errorf("Kinds = %v, want totp and webauthn, in order", got)
		}
		got[0] = "tampered"
		if r.p.Kinds()[0] != authn.FactorTOTP {
			t.Error("a caller changed the module's list through the one it was given")
		}
		empty := newRig(t, h)
		if len(empty.p.Kinds()) != 0 {
			t.Errorf("Kinds of a module with no factor = %v", empty.p.Kinds())
		}
	})
}

// A sensitive-min-age that cannot be read reads as its declared default of 24
// hours: a factor two hours old is not seasoned. Reading the type's zero value
// would season a factor the instant it was registered, lowering the bar for
// impersonation and for resetting another account's factors.
func TestSeasonedUnderAnUnreadableSensitiveMinAgeUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.store.fail(sensitiveMinAgePath, errors.New("settings: not loaded"))
		r.activeFactor("young", authn.FactorTOTP, 2*time.Hour)
		got, err := r.p.Seasoned(context.Background(), "young")
		if err != nil {
			t.Fatalf("Seasoned: %v", err)
		}
		if got {
			t.Error("a factor two hours old is seasoned although the declared default is 24 hours")
		}
	})
}

// A sensitive-min-age below its domain's minimum reads as the declared default of
// 24 hours: a factor two hours old is not seasoned even though a stored thirty
// minutes, zero or a negative duration would season it.
func TestAnOutOfDomainSensitiveMinAgeUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.activeFactor("young", authn.FactorTOTP, 2*time.Hour)
		for _, below := range []time.Duration{30 * time.Minute, 0, -time.Hour} {
			r.store.set(sensitiveMinAgePath, below)
			got, err := r.p.Seasoned(context.Background(), "young")
			if err != nil {
				t.Fatalf("Seasoned under %v: %v", below, err)
			}
			if got {
				t.Errorf("under a stored %v, a factor two hours old is seasoned, want the declared default of 24 hours", below)
			}
		}
	})
}
