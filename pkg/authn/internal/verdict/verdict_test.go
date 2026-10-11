package verdict_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

func principal() model.Principal {
	return model.Principal{
		Account:    "acct-1",
		Methods:    []model.Method{model.MethodPassword},
		Assurance:  model.AssuranceSingle,
		AuthTime:   time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		Attributes: map[string]string{"region": "eu"},
	}
}

// TestAVerdictNobodyAnsweredReadsAsSaidNothing holds the criterion the shape
// of a verdict rests on: the zero value is not a pass.
func TestAVerdictNobodyAnsweredReadsAsSaidNothing(t *testing.T) {
	var v verdict.Verdict
	if got := verdict.OutcomeOf(v); got != verdict.Unspoken {
		t.Errorf("the zero verdict reads as %v, want %v", got, verdict.Unspoken)
	}
	if p := verdict.PrincipalOf(v); p.Account != "" || p.Methods != nil || p.Attributes != nil {
		t.Errorf("the zero verdict carries a principal: %+v", p)
	}
	if err := verdict.CauseOf(v); err != nil {
		t.Errorf("the zero verdict carries a cause: %v", err)
	}
}

func TestEachConstructorSaysWhatItWasCalledFor(t *testing.T) {
	for _, c := range []struct {
		name string
		made verdict.Verdict
		want verdict.Outcome
	}{
		{"a credential that holds", verdict.NewAuthenticated(principal()), verdict.Authenticated},
		{"a credential that does not", verdict.NewDenied(errors.New("wrong password")), verdict.Denied},
		{"a backend that cannot say", verdict.NewUnavailable(errors.New("down")), verdict.Unavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := verdict.OutcomeOf(c.made); got != c.want {
				t.Errorf("reads as %v, want %v", got, c.want)
			}
		})
	}
	if verdict.Unspoken == verdict.Authenticated || verdict.Denied == verdict.Unavailable {
		t.Error("outcomes collide")
	}
}

func TestAnAuthenticatedVerdictCarriesItsPrincipal(t *testing.T) {
	p := principal()
	got := verdict.PrincipalOf(verdict.NewAuthenticated(p))
	if got.Account != p.Account || got.Assurance != p.Assurance || !got.AuthTime.Equal(p.AuthTime) ||
		len(got.Methods) != 1 || got.Methods[0] != model.MethodPassword || got.Attributes["region"] != "eu" {
		t.Errorf("the principal read back is %+v, want %+v", got, p)
	}
	if err := verdict.CauseOf(verdict.NewAuthenticated(p)); err != nil {
		t.Errorf("an authenticated verdict carries the cause %v", err)
	}
}

func TestOnlyAnAuthenticatedVerdictCarriesAPrincipal(t *testing.T) {
	for name, v := range map[string]verdict.Verdict{
		"denied":      verdict.NewDenied(errors.New("x")),
		"unavailable": verdict.NewUnavailable(errors.New("x")),
	} {
		if p := verdict.PrincipalOf(v); p.Account != "" {
			t.Errorf("%s carries the account %q", name, p.Account)
		}
	}
}

// TestWhatARefusalCarriesDoesNotChangeThatItIsARefusal holds the one thing a
// backend must not be able to do with the cause it hands over: a backend that
// returns the gateway's own timeout as the cause of a refusal must not turn a
// wrong password into an outage.
func TestWhatARefusalCarriesDoesNotChangeThatItIsARefusal(t *testing.T) {
	for _, cause := range []error{
		errors.New("wrong password"),
		context.DeadlineExceeded,
		context.Canceled,
		nil,
	} {
		v := verdict.NewDenied(cause)
		if got := verdict.OutcomeOf(v); got != verdict.Denied {
			t.Errorf("a refusal carrying %v reads as %v, want %v", cause, got, verdict.Denied)
		}
		if got := verdict.CauseOf(v); !errors.Is(got, cause) {
			t.Errorf("the cause comes back as %v, want %v unchanged", got, cause)
		}
	}
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, nil} {
		v := verdict.NewUnavailable(cause)
		if got := verdict.OutcomeOf(v); got != verdict.Unavailable {
			t.Errorf("an unavailability carrying %v reads as %v", cause, got)
		}
		if got := verdict.CauseOf(v); !errors.Is(got, cause) {
			t.Errorf("the cause comes back as %v, want %v", got, cause)
		}
	}
}

// TestTheBackendChangingItsPrincipalAfterwardsChangesNothing holds the
// copy-on-construct: the backend keeps its own Principal after returning, and
// the gateway must read what was handed over.
func TestTheBackendChangingItsPrincipalAfterwardsChangesNothing(t *testing.T) {
	p := principal()
	v := verdict.NewAuthenticated(p)

	p.Methods[0] = model.MethodSMS
	p.Methods = append(p.Methods, model.MethodSAML)
	p.Attributes["region"] = "us"
	p.Attributes["injected"] = "x"
	p.Account = "someone-else"

	got := verdict.PrincipalOf(v)
	if len(got.Methods) != 1 || got.Methods[0] != model.MethodPassword {
		t.Errorf("Methods = %v, want [pwd]", got.Methods)
	}
	if got.Attributes["region"] != "eu" || len(got.Attributes) != 1 {
		t.Errorf("Attributes = %v, want the original", got.Attributes)
	}
	if got.Account != "acct-1" {
		t.Errorf("Account = %q", got.Account)
	}
}

// TestAReaderChangingWhatItReadChangesNothingForTheNext holds the copy-on-read:
// a verdict may be returned from a cache more than once.
func TestAReaderChangingWhatItReadChangesNothingForTheNext(t *testing.T) {
	v := verdict.NewAuthenticated(principal())

	first := verdict.PrincipalOf(v)
	first.Methods[0] = model.MethodSMS
	first.Attributes["region"] = "us"

	second := verdict.PrincipalOf(v)
	if second.Methods[0] != model.MethodPassword || second.Attributes["region"] != "eu" {
		t.Errorf("the second read sees the first reader's changes: %+v", second)
	}
}

// TestCopiesHoldUnderConcurrentUse runs under -race.
func TestCopiesHoldUnderConcurrentUse(t *testing.T) {
	p := principal()
	v := verdict.NewAuthenticated(p)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				got := verdict.PrincipalOf(v)
				got.Methods[0] = model.MethodSMS
				got.Attributes["region"] = "us"
				if again := verdict.PrincipalOf(v); again.Methods[0] != model.MethodPassword || again.Attributes["region"] != "eu" {
					t.Error("a read reflects another goroutine's write")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			p.Methods[0] = model.MethodSAML
			p.Attributes["region"] = "xx"
		}
	}()
	wg.Wait()
}

// The bucket names are what a dashboard reads. Changing one breaks every
// panel that names it.
func TestOutcomeBucketNamesAreFrozen(t *testing.T) {
	for o, want := range map[verdict.Outcome]string{
		verdict.Unspoken:      "unspoken",
		verdict.Authenticated: "authenticated",
		verdict.Denied:        "denied",
		verdict.Unavailable:   "unavailable",
		verdict.Outcome(99):   "unknown",
	} {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", int(o), got, want)
		}
	}
}
