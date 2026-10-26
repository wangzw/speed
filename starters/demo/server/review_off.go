//go:build !review

package main

import (
	"context"

	"github.com/vislake/speed/pkg/core"
)

// reviewModules is empty in a default build. The review assembly's identity
// providers and its SMS sender are compiled only under the review build tag,
// so a production binary names none of them and carries no code path of
// theirs. See review.go.
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
