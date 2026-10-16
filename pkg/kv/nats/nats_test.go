package nats

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// TestStanceFailsTheStartupWithoutAServer pins the rule that a module named in
// the command tree has to start: this one reaches NATS at a server, so being
// named without one stops the startup and names the input it stopped on. A
// stand-down in its place would bring the process up with the capability
// silently missing.
func TestStanceFailsTheStartupWithoutAServer(t *testing.T) {
	_, err := stance(&Config{})
	if err == nil {
		t.Fatal("naming this implementation without a server started anyway")
	}
	if want := ConfigNamespace + "." + urlKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

// TestStanceTreatsABlankServerAsNone covers the case an equality check on the
// raw setting misses: a URL that is only whitespace is not a server, and the
// stance is what turns it into a startup failure naming the input rather than a
// connect refusal at the first operation.
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

// TestTheServerURLIsSensitive covers the credentials a NATS URL carries. The
// client reads a user and password or a token from it (nats://user:secret@host),
// and an item not marked sensitive is printed by the help output and the
// diagnostics as it was given.
func TestTheServerURLIsSensitive(t *testing.T) {
	var schema config.Schema
	for _, resource := range Module().Resources {
		if s, ok := resource.(config.Schema); ok {
			schema = s
		}
	}
	item, ok := schema.Items[urlKey]
	if !ok {
		t.Fatalf("the module declares no %q item", urlKey)
	}
	if !item.Sensitive {
		t.Fatalf("%s.%s is not marked sensitive, and a URL carries the credentials to connect with",
			ConfigNamespace, urlKey)
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got, err := stance(&Config{URL: "nats://127.0.0.1:4222"})
	if err != nil {
		t.Fatalf("stance with a server: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a server = %v, want StateEnabled", got.State)
	}
}
