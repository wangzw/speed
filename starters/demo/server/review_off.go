//go:build !review

package main

import (
	"context"

	"github.com/vislake/speed/pkg/core"
)

// reviewModules is empty in a default build: the test controls and the
// loopback guard are compiled only under the review tag. The capabilities
// themselves are named by capabilityModules, which this build also carries;
// what a run starts is decided by configuration, not by this list. See
// review.go.
var reviewModules []string

// reviewFeatureNames is empty in a default build: the review controls and the
// features they declare are compiled only under the review build tag. See
// review.go.
var reviewFeatureNames []string

// reviewSeedExtras seeds nothing in a default build: the impersonation fixture's
// second factors are a review-only exception, and the baseline seed writes no
// factor at all.
func reviewSeedExtras(context.Context, *core.Registry, seedInput) error { return nil }

// reviewSeedsFactors reports that the baseline seed writes no second factor,
// which is what the command's closing note says, whatever the deployment asked
// for: the impersonation fixture lives in the review build only.
func reviewSeedsFactors(seedInput) bool { return false }
