package log

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// TestFromContextWithoutLoggerReturnsDefault pins the fallback: the process default logger itself,
// not a panic, not a logger that drops records, and not a freshly built one.
func TestFromContextWithoutLoggerReturnsDefault(t *testing.T) {
	if got := FromContext(context.Background()); got != Default() {
		t.Errorf("FromContext on a bare context returned %p, want the default logger %p", got, Default())
	}
}

// TestFallbackDoesNotReachTheConfiguredDestination pins where the fallback writes, which is the
// half that is easy to get wrong.
func TestFallbackDoesNotReachTheConfiguredDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured.log")
	product := newTestLogger(t, fileConfig(path))

	captureBootstrapStdout(t, func() {
		FromContext(context.Background()).Info("orphan record")
	})

	if out := read(t, path); strings.Contains(out, "orphan record") {
		t.Errorf("the fallback wrote into the configured destination: %q", out)
	}
	product.Named("http").Info("injected record")
	if out := read(t, path); !strings.Contains(out, "injected record") {
		t.Fatalf("the configured destination took nothing at all: %q", out)
	}
}

// TestFallbackAppearsOnStandardOutput is the other half: the record is not lost, it is on standard
// output.
func TestFallbackAppearsOnStandardOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured.log")
	newTestLogger(t, fileConfig(path))

	out := captureBootstrapStdout(t, func() {
		FromContext(context.Background()).Info("orphan record")
	})

	if !strings.Contains(out, "orphan record") {
		t.Errorf("the fallback record did not reach standard output: %q", out)
	}
}

// TestInjectedLoggerReachesTheConfiguredDestination checks that a logger injected into a context
// writes to the configured file with the module name and the bound attribute.
func TestInjectedLoggerReachesTheConfiguredDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured.log")
	product := newTestLogger(t, fileConfig(path))

	ctx := WithLogger(context.Background(), product.Named("http"))
	ctx = WithAttrs(ctx, "trace_id", "t-3")
	FromContext(ctx).Info("served")

	out := read(t, path)
	for _, want := range []string{`"msg":"served"`, `"trace_id":"t-3"`, `"` + ModuleAttrKey + `":"http"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the configured destination lacks %s: %q", want, out)
		}
	}
}

// TestFromContextFallbackStillPassesThroughRedaction checks that the fallback logger masks a
// registered credential on the bootstrap destination.
func TestFromContextFallbackStillPassesThroughRedaction(t *testing.T) {
	isolateRules(t)
	const secret = "fallback-plaintext-credential"
	processRedaction.AddKeys("fallback-context-key")

	out := captureBootstrapStdout(t, func() {
		FromContext(context.Background()).Info("no logger here", "fallback-context-key", secret)
	})

	if strings.Contains(out, secret) {
		t.Errorf("the fallback path wrote the credential in the clear: %q", out)
	}
	if !strings.Contains(out, MaskedText) {
		t.Errorf("nothing was masked on the fallback path: %q", out)
	}
}

// TestWithLoggerRoundTrips pins that what was injected is what comes back, and that the context it
// was derived from is untouched.
func TestWithLoggerRoundTrips(t *testing.T) {
	injected := slog.New(newCapture())
	bare := context.Background()
	ctx := WithLogger(bare, injected)

	if got := FromContext(ctx); got != injected {
		t.Errorf("FromContext returned %p, want the injected logger %p", got, injected)
	}
	if got := FromContext(bare); got != Default() {
		t.Errorf("the context the injection derived from changed: %p", got)
	}
}

// TestWithAttrsAccumulatesAlongTheContext pins the take, bind and put back: each injection point
// adds its own fields and the ones bound upstream survive.
func TestWithAttrsAccumulatesAlongTheContext(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: allLevels})))
	ctx = WithAttrs(ctx, "trace_id", "t-1")
	ctx = WithAttrs(ctx, "tenant_id", "n-2")

	FromContext(ctx).Info("served")

	out := buf.String()
	for _, want := range []string{"trace_id=t-1", "tenant_id=n-2", "msg=served"} {
		if !strings.Contains(out, want) {
			t.Errorf("the record lacks %q: %q", want, out)
		}
	}
}

// TestWithAttrsOnBareContextAccumulatesOnDefaultChain checks that attributes bound on a context
// with no logger accumulate on the bootstrap chain.
func TestWithAttrsOnBareContextAccumulatesOnDefaultChain(t *testing.T) {
	ctx := WithAttrs(context.Background(), "trace_id", "t-9")
	ctx = WithAttrs(ctx, "tenant_id", "n-9")

	out := captureBootstrapStdout(t, func() { FromContext(ctx).Info("served") })

	for _, want := range []string{"trace_id=t-9", "tenant_id=n-9", "msg=served"} {
		if !strings.Contains(out, want) {
			t.Errorf("the bootstrap chain's record lacks %q: %q", want, out)
		}
	}
}

// TestFromContextIgnoresAForeignValueUnderItsKey checks that a foreign value or a nil logger under
// the context key falls back to the default logger.
func TestFromContextIgnoresAForeignValueUnderItsKey(t *testing.T) {
	//nolint:staticcheck // the point is a value stored under a key this
	// package does not control, which is what a raw context.WithValue does.
	ctx := context.WithValue(context.Background(), contextKey{}, "not a logger")
	if got := FromContext(ctx); got != Default() {
		t.Errorf("FromContext returned %p for a foreign value, want the default logger", got)
	}

	var missing *slog.Logger
	if got := FromContext(WithLogger(context.Background(), missing)); got != Default() {
		t.Errorf("FromContext returned %p for an injected nil logger, want the default logger", got)
	}
}

func jsonContext(l func(slog.Handler) *slog.Logger) (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: allLevels})
	return WithLogger(context.Background(), l(h)), &buf
}

func plainLogger(h slog.Handler) *slog.Logger { return slog.New(h) }

func requireOnce(t *testing.T, out, key, value string) {
	t.Helper()
	if n := strings.Count(out, `"`+key+`":`); n != 1 {
		t.Fatalf("the record carries %s %d times, want once: %s", key, n, out)
	}
	if !strings.Contains(out, `"`+key+`":"`+value+`"`) {
		t.Fatalf("the record carries %s with another value than %q: %s", key, value, out)
	}
}

func requireAbsent(t *testing.T, out, key string) {
	t.Helper()
	if strings.Contains(out, `"`+key+`":`) {
		t.Fatalf("the record still carries %s: %s", key, out)
	}
}

// TestWithAttrsReplacesAKeyAlreadyBound checks that rebinding a key replaces its value in place and
// leaves the other keys as they were.
func TestWithAttrsReplacesAKeyAlreadyBound(t *testing.T) {
	ctx, buf := jsonContext(plainLogger)
	ctx = WithAttrs(ctx, "trace_id", "t-1", "tenant_id", "n-1")
	ctx = WithAttrs(ctx, "tenant_id", "n-2")

	FromContext(ctx).Info("served")

	out := buf.String()
	requireOnce(t, out, "tenant_id", "n-2")
	requireOnce(t, out, "trace_id", "t-1")
	if !strings.Contains(out, `"trace_id":"t-1","tenant_id":"n-2"`) {
		t.Errorf("the replaced key moved from where it was first bound: %s", out)
	}
}

// A key given twice in one call is the same binding said twice; the later one stands, as it would
// across two calls.
func TestWithAttrsKeepsTheLaterOfAKeyGivenTwiceInOneCall(t *testing.T) {
	ctx, buf := jsonContext(plainLogger)
	ctx = WithAttrs(ctx, "tenant_id", "n-1", "tenant_id", "n-2")
	FromContext(ctx).Info("served")
	requireOnce(t, buf.String(), "tenant_id", "n-2")
}

// The members of a group with an empty key are written in line, so they are keyed by their own
// keys, and a later binding of one of them replaces it.
func TestWithAttrsKeysTheMembersOfAnInlineGroup(t *testing.T) {
	ctx, buf := jsonContext(plainLogger)
	ctx = WithAttrs(ctx, slog.Group("", slog.String("tenant_id", "n-1"), slog.String("job", "j-1")))
	ctx = WithAttrs(ctx, "tenant_id", "n-2")
	FromContext(ctx).Info("served")
	out := buf.String()
	requireOnce(t, out, "tenant_id", "n-2")
	requireOnce(t, out, "job", "j-1")
}

// TestWithoutAttrsTakesABoundKeyOff pins the other half: a producer takes its own field off a
// context that stopped being what the field said, and what the others bound stays.
func TestWithoutAttrsTakesABoundKeyOff(t *testing.T) {
	ctx, buf := jsonContext(plainLogger)
	ctx = WithAttrs(ctx, "trace_id", "t-1", "tenant_id", "n-1")
	ctx = WithoutAttrs(ctx, "tenant_id")

	FromContext(ctx).Info("served")

	out := buf.String()
	requireAbsent(t, out, "tenant_id")
	requireOnce(t, out, "trace_id", "t-1")

	buf.Reset()
	FromContext(WithAttrs(ctx, "tenant_id", "n-3")).Info("served")
	requireOnce(t, buf.String(), "tenant_id", "n-3")
}

// Taking off a key nothing bound changes nothing, and hands the context back as it was.
func TestWithoutAttrsOfAKeyNothingBoundLeavesTheContext(t *testing.T) {
	ctx, _ := jsonContext(plainLogger)
	ctx = WithAttrs(ctx, "trace_id", "t-1")
	if got := WithoutAttrs(ctx, "tenant_id"); got != ctx {
		t.Error("taking off a key nothing bound derived a new context")
	}
	bare := context.Background()
	if got := WithoutAttrs(bare, "tenant_id"); got != bare {
		t.Error("taking a key off a context with no logger derived a new context")
	}
}

// A replacement or a removal derives a new context; the one it came from goes on carrying what it
// carried.
func TestReplacingAKeyLeavesTheContextItCameFromAlone(t *testing.T) {
	parent, buf := jsonContext(plainLogger)
	parent = WithAttrs(parent, "tenant_id", "n-1")
	_ = WithAttrs(parent, "tenant_id", "n-2")
	_ = WithoutAttrs(parent, "tenant_id")

	FromContext(parent).Info("served")
	requireOnce(t, buf.String(), "tenant_id", "n-1")
}

// TestReplacingAKeyKeepsWhatTheInjectedLoggerBound checks that rebinding and removing context
// attributes does not disturb what the injected logger bound.
func TestReplacingAKeyKeepsWhatTheInjectedLoggerBound(t *testing.T) {
	ctx, buf := jsonContext(func(h slog.Handler) *slog.Logger {
		return slog.New(h).With(ModuleAttrKey, "http", "tenant_id", "injected")
	})
	ctx = WithAttrs(ctx, "trace_id", "t-1")
	ctx = WithAttrs(ctx, "trace_id", "t-2")
	ctx = WithoutAttrs(ctx, "tenant_id")

	FromContext(ctx).Info("served")

	out := buf.String()
	requireOnce(t, out, ModuleAttrKey, "http")
	requireOnce(t, out, "trace_id", "t-2")
	requireOnce(t, out, "tenant_id", "injected")
}

// TestAReplacementStillPassesRedaction checks that a rebound credential is masked and a removed key
// is absent.
func TestAReplacementStillPassesRedaction(t *testing.T) {
	isolateRules(t)
	const secret = "rebound-plaintext-credential"
	processRedaction.AddKeys("rebound-context-key")

	ctx, buf := jsonContext(func(h slog.Handler) *slog.Logger { return slog.New(newRedactHandler(h)) })
	ctx = WithAttrs(ctx, "rebound-context-key", secret, "tenant_id", "n-1")
	ctx = WithAttrs(ctx, "tenant_id", "n-2")
	ctx = WithoutAttrs(ctx, "trace_id", "tenant_id")

	FromContext(ctx).Info("served")

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("a rebinding wrote the credential in the clear: %s", out)
	}
	requireOnce(t, out, "rebound-context-key", MaskedText)
	requireAbsent(t, out, "tenant_id")
}

// On a context with no logger the replacement holds on the default logger too.
func TestWithAttrsReplacesAKeyOnTheDefaultChain(t *testing.T) {
	ctx := WithAttrs(context.Background(), "tenant_id", "n-1")
	ctx = WithAttrs(ctx, "tenant_id", "n-2")

	out := captureBootstrapStdout(t, func() { FromContext(ctx).Info("served") })

	if n := strings.Count(out, "tenant_id="); n != 1 || !strings.Contains(out, "tenant_id=n-2") {
		t.Errorf("the bootstrap chain's record carries tenant_id %d times, want once as n-2: %q", n, out)
	}
}
