package pgtest

import "testing"

// TestAddressFallsBackToTheComposeStack holds the address to the value the fixture documents when
// no deployment has pointed it elsewhere.
func TestAddressFallsBackToTheComposeStack(t *testing.T) {
	t.Setenv("SPEED_TEST_PG_ADDR", "")

	if got := address(); got != "127.0.0.1:5432" {
		t.Fatalf("address() = %q with SPEED_TEST_PG_ADDR empty, want the default 127.0.0.1:5432", got)
	}
}

// TestAddressFollowsTheEnvironment checks that address reads SPEED_TEST_PG_ADDR when it is set.
func TestAddressFollowsTheEnvironment(t *testing.T) {
	const pointed = "127.0.0.1:15432"
	t.Setenv("SPEED_TEST_PG_ADDR", pointed)

	if got := address(); got != pointed {
		t.Fatalf("address() = %q with SPEED_TEST_PG_ADDR=%s, want %s", got, pointed, pointed)
	}
}
