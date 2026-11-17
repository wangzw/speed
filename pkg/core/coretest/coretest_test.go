package coretest

import (
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
	if got, want := Declared(d), time.Duration(float64(d)*scale); got != want {
		t.Errorf("Declared(%v) = %v, want %v", d, got, want)
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
	previous := scale
	scale = declared
	t.Cleanup(func() { scale = previous })

	if got, want := Declared(d), time.Duration(float64(d)*declared); got != want {
		t.Errorf("Declared(%v) at GATE_TIME_SCALE=%v = %v, want %v", d, declared, got, want)
	}
	if got, want := Scale(d), time.Duration(float64(d)*float64(raceFactor)*declared); got != want {
		t.Errorf("Scale(%v) at GATE_TIME_SCALE=%v = %v, want %v", d, declared, got, want)
	}
}
