//go:build !review

package main

// The isolation test the default build carries: the review assembly's
// notification type must not exist in a baseline binary's available set. It is
// the half of the pair that fails if the type is moved into an untagged file —
// notification.preferences and notification.delivery collect their taxonomy
// over the available set, so an untagged declaration would be part of the
// baseline's preference matrix even though no baseline command runs it. The
// review-tagged half (review_notification_test.go) holds the other direction.

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// TestDefaultBuildDeclaresNoNotificationType holds the baseline side of the
// isolation: a binary built without the review tag registers no module that
// declares a notification type, and names no review module for a run.
func TestDefaultBuildDeclaresNoNotificationType(t *testing.T) {
	if len(reviewModules) != 0 {
		t.Fatalf("the default build names review modules %v, want none; a module "+
			"named here would run in the baseline", reviewModules)
	}
	declared := core.Resources[notification.TypeSpec](core.ProcessRegistry)
	if len(declared) == 0 {
		return
	}
	keys := make([]string, 0, len(declared))
	for _, resource := range declared {
		keys = append(keys, resource.Module+": "+resource.Value.Key)
	}
	t.Fatalf("the default build's available set declares notification type(s) %v; the "+
		"review type belongs in a //go:build review file, because both taxonomy "+
		"consumers collect over the available set and would show it in the baseline", keys)
}
