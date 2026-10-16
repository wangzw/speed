package nats

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestANamespaceASubjectCannotCarryIsRefused covers the other half: a namespace
// that would silently widen or move the subject space fails at open rather than
// later, when an assembly is receiving strangers' events.
func TestANamespaceASubjectCannotCarryIsRefused(t *testing.T) {
	for _, bad := range []string{"has.dot", "has space", "has*wildcard", "has>wildcard", "has\ttab"} {
		if err := checkNamespace(bad); err == nil {
			t.Errorf("checkNamespace(%q) accepted it, and a subject token may not hold a dot, "+
				"whitespace or a wildcard", bad)
		}
	}
	for _, good := range []string{"speed_events", "prod-1", "assemblyOne"} {
		if err := checkNamespace(good); err != nil {
			t.Errorf("checkNamespace(%q) refused it: %v", good, err)
		}
	}
}

// TestStanceFailsWithoutAServer pins the enablement rule at this backend's own
// precondition: a module taken in at run level has to start, so an implementation
// the host did not point at a server fails the startup naming the item. It is not
// a stance that lets the run continue without an event bus.
func TestStanceFailsWithoutAServer(t *testing.T) {
	_, err := stance(&Config{})
	if err == nil {
		t.Fatal("stance with no server did not fail the startup")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+urlKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
}

// TestStanceTreatsABlankServerAsNone covers the case an equality check on the
// raw setting misses: a URL that is only whitespace is not a server, and the
// stance is what turns it into a startup failure naming the input rather than a
// connect refusal at the first publish.
func TestStanceTreatsABlankServerAsNone(t *testing.T) {
	for _, url := range []string{"", " ", "\t", "\n"} {
		_, err := stance(&Config{URL: url})
		if err == nil {
			t.Errorf("stance with url %q started anyway", url)
			continue
		}
		if want := ConfigNamespace + "." + urlKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the error for url %q does not name %q: %v", url, want, err)
		}
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got, err := stance(&Config{URL: "nats://127.0.0.1:4222"})
	if err != nil {
		t.Fatalf("stance with a server failed the startup: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a server = %v, want StateEnabled", got.State)
	}
}
