package main

// The notification fixture's compile-time side: the fixture — its type, worker
// and recipient wiring — is compiled into the capability ceiling.
//
// The type is part of the available set even in a run that never dispatches it:
// notification.preferences and notification.delivery collect their taxonomy
// over the available set, and the compile-time ceiling is that set. Whether a
// run actually publishes and dispatches the type is the module's own Prepare
// decision, reported by the startup diagnostics; it is not a different binary.

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// TestDefaultBuildDeclaresTheNotificationType holds the ceiling: the binary
// registers the fixture module and therefore declares its notification type.
func TestDefaultBuildDeclaresTheNotificationType(t *testing.T) {
	t.Parallel()
	declared := make(map[string]string)
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		declared[resource.Value.Key] = resource.Module
	}
	module, ok := declared[demoNotificationTypeKey]
	if !ok {
		t.Fatalf("the binary declares no %q notification type; the fixture is part of the "+
			"capability ceiling every build carries", demoNotificationTypeKey)
	}
	if module != demoNotificationModuleName {
		t.Errorf("the type %q is declared by %q, want %q", demoNotificationTypeKey, module,
			demoNotificationModuleName)
	}
}
