package log_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/log"
)

var moduleLogger log.Logger

// Example shows the shape a module takes logging up in: it registers its redaction rules and takes
// a named logger in its own New, and its request path carries the logger in the context.
func Example() {
	moduleLogger.Redaction().AddKeys("password", "token")
	logger := moduleLogger.Named("http")

	ctx := log.WithLogger(context.Background(), logger)
	ctx = log.WithAttrs(ctx, "tenant_id", "t-42")
	log.FromContext(ctx).InfoContext(ctx, "request served", "duration_ms", 12)
}

// ExampleFromContext shows what a call site gets when its context carries no logger: the process
// logger, not a no-op and not a panic.
func ExampleFromContext() {
	ctx := context.Background()

	fmt.Println(log.FromContext(ctx) == log.Default())
	// Output:
	// true
}

// ExampleWithLogger shows the injection point: the logger goes into the context once, and every
// call site below it takes the same one back.
func ExampleWithLogger() {
	logger := log.Default().With("request_id", "r-7")
	ctx := log.WithLogger(context.Background(), logger)

	fmt.Println(log.FromContext(ctx) == logger)
	fmt.Println(log.FromContext(context.Background()) == logger)
	// Output:
	// true
	// false
}

// ExampleWithAttrs shows attributes accumulating on the logger a context carries, so the call
// sites below do not repeat what the request already knows.
func ExampleWithAttrs() {
	ctx := log.WithAttrs(context.Background(), "tenant_id", "t-42")
	ctx = log.WithAttrs(ctx, "user_id", "u-9")

	fmt.Println(log.FromContext(ctx) != log.Default())
	// Output:
	// true
}

// ExampleMatcher shows a value-shape rule: it reports the spans of a value that need masking and
// leaves the rest of the text alone.
func ExampleMatcher() {
	var bearer log.Matcher = func(value string) []log.Span {
		const prefix = "Bearer "
		if !strings.HasPrefix(value, prefix) {
			return nil
		}
		return []log.Span{{Start: len(prefix), End: len(value)}}
	}

	fmt.Println(bearer("Bearer abc123"))
	fmt.Println(bearer("no credential here"))
	// Output:
	// [{7 13}]
	// []
}
