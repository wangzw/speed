//go:build !review

package main

import "github.com/vislake/speed/pkg/core"

// reviewControlResources answers nothing in a default build: the notification
// dispatch control's feature is compiled only under the review tag. See
// notification_control_review.go.
func reviewControlResources() []any { return nil }

// mountReviewControl is a no-op in a default build: the dispatch control route
// is compiled only under the review tag, so there is nothing to mount.
func mountReviewControl(*core.Registry, *demoNotifier) error { return nil }
