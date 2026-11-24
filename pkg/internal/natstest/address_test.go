package natstest

import "testing"

// TestServerURLFallsBackToTheComposeStack holds the URL and the dial address to the values the
// fixture documents when no deployment has pointed it elsewhere.
func TestServerURLFallsBackToTheComposeStack(t *testing.T) {
	t.Setenv("SPEED_TEST_NATS_URL", "")

	if got := serverURL(); got != "nats://127.0.0.1:4222" {
		t.Fatalf("serverURL() = %q with SPEED_TEST_NATS_URL empty, want the default nats://127.0.0.1:4222", got)
	}
	if got := dialAddress(); got != "127.0.0.1:4222" {
		t.Fatalf("dialAddress() = %q with SPEED_TEST_NATS_URL empty, want 127.0.0.1:4222", got)
	}
}

// TestServerURLFollowsTheEnvironment holds the fixture to reading the URL when it is used: a case
// that sets the variable must reach the server it named, not the one the package saw at start-up.
func TestServerURLFollowsTheEnvironment(t *testing.T) {
	const pointed = "nats://127.0.0.1:14222"
	t.Setenv("SPEED_TEST_NATS_URL", pointed)

	if got := serverURL(); got != pointed {
		t.Fatalf("serverURL() = %q with SPEED_TEST_NATS_URL=%s, want %s", got, pointed, pointed)
	}
	if got := dialAddress(); got != "127.0.0.1:14222" {
		t.Fatalf("dialAddress() = %q with SPEED_TEST_NATS_URL=%s, want 127.0.0.1:14222", got, pointed)
	}
}
