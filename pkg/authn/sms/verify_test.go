package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// sentCode sends a delivered login code and returns the challenge identifier and
// the code, read off the queue the send put it on.
func (r *rig) sentCode() (string, string) {
	r.t.Helper()
	id, err := r.sendLogin()
	if err != nil {
		r.t.Fatalf("Send: %v", err)
	}
	msg := <-r.p.pool.queue
	return id, msg.Params["code"]
}

// TestVerifyAcceptsTheRightCodeOnce holds the happy path: the code the row was
// written for passes, the row is consumed, and a second submit of the same
// correct code fails because the row is spent.
func TestVerifyAcceptsTheRightCodeOnce(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	id, code := r.sentCode()

	if err := r.p.Verify(context.Background(), id, code); err != nil {
		t.Fatalf("Verify with the right code: %v", err)
	}
	if consumed, _ := r.column(id, "consumed_at"); consumed == nil {
		t.Errorf("the challenge was not consumed by a passing Verify")
	}
	if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Fatalf("a replay returned %v, want ErrInvalidCredentials", err)
	}
}

// TestVerifyRefusesAWrongCodeAndCountsTheAttempt holds the refusal: a wrong code
// is ErrInvalidCredentials with no distinction, and the attempt is counted.
func TestVerifyRefusesAWrongCodeAndCountsTheAttempt(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	id, code := r.sentCode()

	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	if err := r.p.Verify(context.Background(), id, wrong); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Fatalf("Verify with a wrong code returned %v, want ErrInvalidCredentials", err)
	}
	if attempts, _ := r.column(id, "attempts"); toInt(attempts) != 1 {
		t.Errorf("attempts = %v, want 1", attempts)
	}
}

// TestVerifyRefusesAfterTheAttemptLimit holds the attempt cap: once the row has
// taken its last comparison, neither a wrong nor the right code passes, and the
// count does not move past the limit.
func TestVerifyRefusesAfterTheAttemptLimit(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.store.set(codeAttemptsPath, 2)
	id, code := r.sentCode()

	for i := range 2 {
		if err := r.p.Verify(context.Background(), id, "000000"); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Fatalf("attempt %d returned %v, want ErrInvalidCredentials", i, err)
		}
	}
	if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Fatalf("Verify with the right code past the limit returned %v, want ErrInvalidCredentials", err)
	}
	if attempts, _ := r.column(id, "attempts"); toInt(attempts) != 2 {
		t.Errorf("attempts = %v, want 2: the count must not pass the limit", attempts)
	}
}

// TestVerifyNeverPassesASuppressedRow holds the suppression at the check: a row
// marked suppressed fails even with the exact code it was written for, and the
// attempt is still counted.
func TestVerifyNeverPassesASuppressedRow(t *testing.T) {
	r := newRigSQLite(t)
	id := event.NewID()
	code := "123456"
	mac := r.database.BlindIndex(id + "|" + code)
	r.insert(id, authn.SMSPurposeLogin, r.database.BlindIndex(phone), nil, true, mac, 0, 5,
		base, base.Add(5*time.Minute), nil)

	if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Fatalf("Verify on a suppressed row with the right code returned %v, want ErrInvalidCredentials", err)
	}
	if attempts, _ := r.column(id, "attempts"); toInt(attempts) != 1 {
		t.Errorf("attempts = %v, want 1: a suppressed row is counted like any other", attempts)
	}
}

// TestVerifyRefusesAnUnknownExpiredOrConsumedChallenge holds the unified refusal:
// a challenge that is not there, one past its time, and one already consumed are
// all ErrInvalidCredentials and are told apart by nothing.
func TestVerifyRefusesAnUnknownExpiredOrConsumedChallenge(t *testing.T) {
	r := newRigSQLite(t)

	if err := r.p.Verify(context.Background(), "no-such-challenge", "123456"); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("Verify on an unknown challenge returned %v, want ErrInvalidCredentials", err)
	}

	expired := event.NewID()
	r.insert(expired, authn.SMSPurposeLogin, r.database.BlindIndex(phone), nil, false,
		r.database.BlindIndex(expired+"|"+"123456"), 0, 5, base.Add(-10*time.Minute), base.Add(-time.Minute), nil)
	if err := r.p.Verify(context.Background(), expired, "123456"); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("Verify on an expired challenge returned %v, want ErrInvalidCredentials", err)
	}

	consumed := event.NewID()
	at := base.Add(-time.Minute)
	r.insert(consumed, authn.SMSPurposeLogin, r.database.BlindIndex(phone), nil, false,
		r.database.BlindIndex(consumed+"|"+"123456"), 1, 5, base.Add(-2*time.Minute), base.Add(time.Minute), &at)
	if err := r.p.Verify(context.Background(), consumed, "123456"); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("Verify on a consumed challenge returned %v, want ErrInvalidCredentials", err)
	}
}

// TestConcurrentSubmitsOfOneCorrectCodeHaveOneWinner holds the conditional
// consume: two verifications of the same correct code cannot both pass.
func TestConcurrentSubmitsOfOneCorrectCodeHaveOneWinner(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	id, code := r.sentCode()

	const n = 8
	results := make(chan error, n)
	for range n {
		go func() { results <- r.p.Verify(context.Background(), id, code) }()
	}
	var winners int
	for range n {
		if err := <-results; err == nil {
			winners++
		} else if !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a concurrent Verify returned %v, want nil or ErrInvalidCredentials", err)
		}
	}
	if winners != 1 {
		t.Errorf("%d concurrent submits passed, want exactly 1", winners)
	}
}

// TestASecondVerifyOfTheSameCodeFailsOnTheSpentRow holds the end-to-end
// consequence of the one-winner gate: once a correct submit has consumed the
// row, the same correct code no longer passes. What is being asserted is that
// one correct code has one lifetime, not which gate refuses the second submit —
// the increment gate does here, and the consume gate is pinned on its own at the
// register layer.
func TestASecondVerifyOfTheSameCodeFailsOnTheSpentRow(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	id, code := r.sentCode()

	if err := r.p.Verify(context.Background(), id, code); err != nil {
		t.Fatalf("the first Verify: %v", err)
	}
	if consumed, _ := r.column(id, "consumed_at"); consumed == nil {
		t.Fatal("the first Verify did not consume the row")
	}
	if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Fatalf("the second Verify of the same code returned %v, want ErrInvalidCredentials", err)
	}
}

func toInt(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case int:
		return x
	}
	return -1
}
