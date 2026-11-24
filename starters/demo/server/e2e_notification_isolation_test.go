package main

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// TestDefaultBuildDeclaresTheNotificationType holds that the binary registers the fixture module and
// declares its notification type.
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
