package coretest

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadScaleAnswersOneForAnUnsetOrEmptyVariable(t *testing.T) {
	t.Setenv(envVar, "")
	os.Unsetenv(envVar)
	if got, err := readScale(os.Getenv(envVar)); err != nil || got != 1 {
		t.Fatalf("readScale(unset) = %v, %v, want 1, nil", got, err)
	}
	if got, err := readScale(""); err != nil || got != 1 {
		t.Fatalf("readScale(empty) = %v, %v, want 1, nil", got, err)
	}
	if got, err := readScale("   "); err != nil || got != 1 {
		t.Fatalf("readScale(whitespace) = %v, %v, want 1, nil", got, err)
	}
}

func TestReadScaleAnswersAParsedValue(t *testing.T) {
	for raw, want := range map[string]float64{
		"1":     1,
		"2":     2,
		"2.5":   2.5,
		" 2.5 ": 2.5,
		"1e1":   10,
		"1.5e1": 15,
	} {
		got, err := readScale(raw)
		if err != nil {
			t.Errorf("readScale(%q) returned error %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("readScale(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestReadScaleRefusesAValueThatIsNotAFiniteNumberAtLeastOne carries the whole
// table of values the variable refuses, "NaN" and "Inf" included: ParseFloat
// accepts both, neither is below 1, and either would travel on as a bound.
func TestReadScaleRefusesAValueThatIsNotAFiniteNumberAtLeastOne(t *testing.T) {
	for _, raw := range []string{
		"abc",
		"0", "0.5", "-3",
		"NaN", "Inf", "+Inf", "-Inf", "Infinity", "1e999",
		"0x10", "0x1p4", "0X1P4",
		"1_000",
	} {
		got, err := readScale(raw)
		if err == nil {
			t.Errorf("readScale(%q) = %v, want an error", raw, got)
			continue
		}
		if !strings.Contains(err.Error(), envVar) ||
			!strings.Contains(err.Error(), "finite number of at least 1") {
			t.Errorf("readScale(%q) error %q does not name the requirement", raw, err)
		}
	}
}

func TestMustReadScaleStopsTheRunOnARefusedValue(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("mustReadScale did not stop the run on a refused value")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("mustReadScale panicked with %v, want an error", r)
		}
		if !strings.Contains(err.Error(), envVar) || !strings.Contains(err.Error(), "at least 1") {
			t.Errorf("mustReadScale panic %q does not name the requirement", err)
		}
	}()
	mustReadScale("abc")
}

func TestMustReadScaleAnswersTheParsedValue(t *testing.T) {
	if got := mustReadScale("2.5"); got != 2.5 {
		t.Errorf("mustReadScale(2.5) = %v, want 2.5", got)
	}
}

func TestScaleNeverLowersTheRaceBound(t *testing.T) {
	const d = 3 * time.Second
	if got, floor := Scale(d), time.Duration(float64(d)*float64(raceFactor)); got < floor {
		t.Errorf("Scale(%v) = %v, want at least the race bound %v", d, got, floor)
	}
	if got := Scale(0); got != 0 {
		t.Errorf("Scale(0) = %v, want 0", got)
	}
}

// TestDeclaredCarriesTheCallersScaleAlone holds the second factor set apart: a
// detection bound grows with the caller's declaration and not with the race
// detector, so the time a wrong implementation needs stays measurable above it.
func TestDeclaredCarriesTheCallersScaleAlone(t *testing.T) {
	const d = 3 * time.Second
	declared, err := readScale(os.Getenv(envVar))
	if err != nil {
		t.Fatalf("readScale(%q) returned error %v", os.Getenv(envVar), err)
	}
	if got, want := Declared(d), time.Duration(float64(d)*declared); got != want {
		t.Errorf("Declared(%v) at GATE_TIME_SCALE=%v = %v, want %v", d, declared, got, want)
	}
	if got := Declared(0); got != 0 {
		t.Errorf("Declared(0) = %v, want 0", got)
	}
}

// TestBothFactorsApplyTheCallersScale sets a scale other than the run's own, so
// that a function that forgot to multiply by it is seen: at the default of 1 a
// missing multiply and a correct one answer the same.
func TestBothFactorsApplyTheCallersScale(t *testing.T) {
	const d = 2 * time.Second
	const declared = 2.5
	t.Setenv(envVar, "2.5")

	if got, want := Declared(d), time.Duration(float64(d)*declared); got != want {
		t.Errorf("Declared(%v) at GATE_TIME_SCALE=%v = %v, want %v", d, declared, got, want)
	}
	if got, want := Scale(d), time.Duration(float64(d)*float64(raceFactor)*declared); got != want {
		t.Errorf("Scale(%v) at GATE_TIME_SCALE=%v = %v, want %v", d, declared, got, want)
	}
}

// recordingFataler records the failure a case is stopped with instead of
// stopping the run, so the points around the margin can be read. Like
// testing.T.Fatalf, only the first failure is the one a run reports.
type recordingFataler struct {
	failed  bool
	message string
}

func (*recordingFataler) Helper() {}

func (r *recordingFataler) Fatalf(format string, args ...any) {
	if r.failed {
		return
	}
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

// atScale runs body with GATE_TIME_SCALE set to declared.
func atScale(t *testing.T, declared string, body func()) {
	t.Helper()
	t.Setenv(envVar, declared)
	body()
}

// TestDeclaredAgainstReturnsABoundBelowTheMargin holds the passing side. The
// whole bound, the fixed part included, stays below nine tenths of the wrong
// time, so the case is still told apart and the function returns the bound.
func TestDeclaredAgainstReturnsABoundBelowTheMargin(t *testing.T) {
	atScale(t, "1", func() {
		rec := &recordingFataler{}
		if got := DeclaredAgainst(rec, 600*time.Millisecond, 200*time.Millisecond, time.Second); got != 800*time.Millisecond {
			t.Errorf("DeclaredAgainst = %v, want 800ms", got)
		}
		if rec.failed {
			t.Errorf("DeclaredAgainst failed a case whose bound is below nine tenths of the wrong time: %s", rec.message)
		}
	})
}

// TestDeclaredAgainstPassesJustBelowNineTenths holds the passing edge of the
// guard band: a bound one millisecond below nine tenths of the wrong time is
// still told apart, so the function returns it and does not fail the case. The
// wrong time is 900ms so that the margin, 900ms - 900ms/10, is the exact 810ms
// a reader can check against the integer arithmetic rather than a truncated
// share.
func TestDeclaredAgainstPassesJustBelowNineTenths(t *testing.T) {
	atScale(t, "1", func() {
		rec := &recordingFataler{}
		if got := DeclaredAgainst(rec, 0, 809*time.Millisecond, 900*time.Millisecond); got != 809*time.Millisecond {
			t.Errorf("DeclaredAgainst = %v, want 809ms", got)
		}
		if rec.failed {
			t.Errorf("DeclaredAgainst failed a case whose bound is below nine tenths of the wrong time: %s", rec.message)
		}
	})
}

// TestDeclaredAgainstFailsAtExactlyNineTenths holds the failing edge of the
// guard band: a bound exactly at nine tenths of the wrong time is no longer
// below it, so the case fails rather than passing a bound both implementations
// satisfy.
func TestDeclaredAgainstFailsAtExactlyNineTenths(t *testing.T) {
	atScale(t, "1", func() {
		rec := &recordingFataler{}
		DeclaredAgainst(rec, 0, 810*time.Millisecond, 900*time.Millisecond)
		if !rec.failed {
			t.Fatal("DeclaredAgainst passed a case whose bound is exactly nine tenths of the wrong time")
		}
	})
}

// TestDeclaredAgainstFailureNamesTheFourElements holds what a caller reads when
// the guard fires: the scale, the bound, the wrong implementation's time and
// the meeting point. The scale is well past the margin, so the wording says the
// bound has passed nine tenths of the wrong time rather than sitting at it.
func TestDeclaredAgainstFailureNamesTheFourElements(t *testing.T) {
	atScale(t, "2", func() {
		rec := &recordingFataler{}
		DeclaredAgainst(rec, 0, 600*time.Millisecond, time.Second)
		if !rec.failed {
			t.Fatal("DeclaredAgainst passed a case whose scaled bound passed nine tenths of the wrong time")
		}
		for _, want := range []string{"GATE_TIME_SCALE=2", "1.2s", "1s", "1.67", "has passed nine tenths"} {
			if !strings.Contains(rec.message, want) {
				t.Errorf("the failure %q does not name %q", rec.message, want)
			}
		}
	})
}

// TestDeclaredAgainstFailsPastTheMargin holds the failing side, at a scale
// other than 1 so that the failure is seen to carry the scale the run declared.
func TestDeclaredAgainstFailsPastTheMargin(t *testing.T) {
	atScale(t, "5", func() {
		rec := &recordingFataler{}
		DeclaredAgainst(rec, 0, 200*time.Millisecond, time.Second)
		if !rec.failed {
			t.Fatal("DeclaredAgainst passed a case whose scaled bound reached the wrong time")
		}
		for _, want := range []string{"GATE_TIME_SCALE=5", "1s", "5.00"} {
			if !strings.Contains(rec.message, want) {
				t.Errorf("the failure %q does not name %q", rec.message, want)
			}
		}
	})
}

// TestBoundsReadTheVariableWhenTheCaseRuns checks that a scale set after
// startup applies to each bound the case computes.
func TestBoundsReadTheVariableWhenTheCaseRuns(t *testing.T) {
	const d = 2 * time.Second
	t.Setenv(envVar, "2.5")
	if got, want := Scale(d), time.Duration(float64(d)*float64(raceFactor)*2.5); got != want {
		t.Errorf("Scale(%v) at GATE_TIME_SCALE=2.5 = %v, want %v", d, got, want)
	}
	if got, want := Declared(d), 5*time.Second; got != want {
		t.Errorf("Declared(%v) at GATE_TIME_SCALE=2.5 = %v, want %v", d, got, want)
	}
	rec := &recordingFataler{}
	if got := DeclaredAgainst(rec, 0, 200*time.Millisecond, time.Second); got != 500*time.Millisecond {
		t.Errorf("DeclaredAgainst at GATE_TIME_SCALE=2.5 = %v, want 500ms", got)
	}
	if rec.failed {
		t.Errorf("DeclaredAgainst failed a case whose bound is below nine tenths of the wrong time: %s", rec.message)
	}
	t.Setenv(envVar, "5")
	rec = &recordingFataler{}
	DeclaredAgainst(rec, 0, 200*time.Millisecond, time.Second)
	if !rec.failed {
		t.Fatal("DeclaredAgainst passed a case whose bound reached the wrong time at GATE_TIME_SCALE=5")
	}
	if !strings.Contains(rec.message, "GATE_TIME_SCALE=5") {
		t.Errorf("the failure %q does not name GATE_TIME_SCALE=5", rec.message)
	}
}

// TestEveryBoundRefusesAValueTheVariableCannotMean checks that each value
// function panics on a refused GATE_TIME_SCALE.
func TestEveryBoundRefusesAValueTheVariableCannotMean(t *testing.T) {
	t.Setenv(envVar, "abc")
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"Scale", func() { Scale(time.Second) }},
		{"Declared", func() { Declared(time.Second) }},
		{"DeclaredAgainst", func() { DeclaredAgainst(&recordingFataler{}, 0, time.Second, time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s did not stop the run on a refused GATE_TIME_SCALE", tc.name)
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("%s panicked with %v, want an error", tc.name, r)
				}
				if !strings.Contains(err.Error(), envVar) || !strings.Contains(err.Error(), "at least 1") {
					t.Errorf("%s panic %q does not name the requirement", tc.name, err)
				}
			}()
			tc.call()
		})
	}
}

// TestDeclaredAgainstNeedsPositiveArguments holds the guard: a bound or a wrong
// time that is not positive is a caller's mistake and stops the case rather
// than being scaled into a bound nothing checks.
func TestDeclaredAgainstNeedsPositiveArguments(t *testing.T) {
	atScale(t, "1", func() {
		for _, args := range [][3]time.Duration{
			{0, 0, time.Second},
			{0, time.Second, 0},
			{0, time.Second, -time.Second},
			{-time.Second, time.Second, time.Second},
		} {
			rec := &recordingFataler{}
			DeclaredAgainst(rec, args[0], args[1], args[2])
			if !rec.failed || !strings.Contains(rec.message, "positive") {
				t.Errorf("DeclaredAgainst(%v, %v, %v) answered %q, want a failure naming the positive requirement",
					args[0], args[1], args[2], rec.message)
			}
		}
	})
}
