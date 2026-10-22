package selfservice_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// logLines counts the captured log lines that carry every needle.
func logLines(h *harness, needles ...string) int {
	count := 0
	for _, line := range strings.Split(h.logs.String(), "\n") {
		if line == "" {
			continue
		}
		all := true
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				all = false
				break
			}
		}
		if all {
			count++
		}
	}
	return count
}

// wideBudgets leaves only the budget under test in the way of a send.
func wideBudgets() map[string]int {
	return map[string]int{
		budgetAddressHourly: 60, budgetAddressDaily: 200, budgetAccountHourly: 60, budgetGlobalHourly: 1000,
	}
}

// TestACountThatCannotBeReadUsesTheDefaultReportedOnce holds the failure
// direction of counts.get: a budget setting that cannot be read reads as its
// declared default, not as zero (which would turn the budget off) and not as a
// failed request, and the fault is written at Error once across repeated reads.
func TestACountThatCannotBeReadUsesTheDefaultReportedOnce(t *testing.T) {
	h := budgetHarness(t, wideBudgets())
	h.settings.errs[budgetAddressHourly] = errors.New("the settings store is unavailable")

	// The declared default of the per-address hourly budget is five, so the
	// sixth send inside the hour is the one refused.
	for i := 0; i < 5; i++ {
		h.reauthenticate()
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("send %d answered %d %s, want 200 from the default budget", i+1, w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
	}
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the sixth send answered %d %s, want the default budget's 429", w.Code, w.Body)
	}
	if len(h.email.sent) != 5 {
		t.Errorf("the address was sent %d message(s), want the default budget of 5", len(h.email.sent))
	}
	if got := logLines(h, budgetAddressHourly, "cannot be read"); got != 1 {
		t.Errorf("the unreadable setting was reported %d times, want once for the fault", got)
	}
}

// TestACountOutsideItsDomainUsesTheDefaultReportedOnce holds the other fallback
// of counts.get: a stored value outside the setting's domain reads as the
// declared default and is written at Error once, never served as the tuning
// value.
func TestACountOutsideItsDomainUsesTheDefaultReportedOnce(t *testing.T) {
	numbers := wideBudgets()
	numbers[budgetAddressHourly] = 0 // below the domain's floor of 1
	h := budgetHarness(t, numbers)

	for i := 0; i < 5; i++ {
		h.reauthenticate()
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
			t.Fatalf("send %d answered %d %s, want 200 from the default budget", i+1, w.Code, w.Body)
		}
		h.clockNow = h.clockNow.Add(2 * time.Minute)
	}
	h.reauthenticate()
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the sixth send answered %d %s, want the default budget's 429", w.Code, w.Body)
	}
	if got := logLines(h, budgetAddressHourly, "outside its domain"); got != 1 {
		t.Errorf("the out-of-domain setting was reported %d times, want once for the fault", got)
	}
}
