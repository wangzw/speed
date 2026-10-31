//go:build !review

package main

// The default build's side of the notification fixture's split: the fixture —
// its type, worker and recipient wiring — is compiled into the capability
// ceiling, while the review-only dispatch control is not.
//
// The type is part of the available set even in a run that never dispatches it:
// notification.preferences and notification.delivery collect their taxonomy
// over the available set, and the compile-time ceiling is that set. Whether a
// run actually publishes and dispatches the type is the module's own Prepare
// decision, reported by the startup diagnostics; it is not a different binary.

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/notification"
)

// TestDefaultBuildDeclaresTheNotificationType holds the ceiling: a binary built
// without the review tag registers the fixture module and therefore declares
// its notification type. Before the capability roster became unconditional this
// declaration existed only under the review tag.
func TestDefaultBuildDeclaresTheNotificationType(t *testing.T) {
	declared := make(map[string]string)
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		declared[resource.Value.Key] = resource.Module
	}
	module, ok := declared[reviewNotificationTypeKey]
	if !ok {
		t.Fatalf("the default build declares no %q notification type; the fixture is part of the "+
			"capability ceiling every build carries", reviewNotificationTypeKey)
	}
	if module != reviewNotificationModuleName {
		t.Errorf("the type %q is declared by %q, want %q", reviewNotificationTypeKey, module,
			reviewNotificationModuleName)
	}
}

// TestDefaultBuildDeclaresNoNotificationControl holds the other direction: the
// review-only dispatch control's feature is absent from the default build's
// vocabulary, so the console never offers a control that would post to a route
// this binary does not mount.
func TestDefaultBuildDeclaresNoNotificationControl(t *testing.T) {
	for _, resource := range core.Resources[feature.Spec](core.ProcessRegistry) {
		if resource.Value.Name == "review.notification.control" {
			t.Fatalf("the default build declares %q; the dispatch control is test-only and must "+
				"stay behind the review tag", resource.Value.Name)
		}
	}
}
