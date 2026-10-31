//go:build review

package main

// The review assembly's control over the review SMS carrier.
//
// The carrier reviews each template and each language, but a process that
// reaches no vendor has nothing to answer from: its verdicts have to come from
// somewhere a person can drive. This module mounts one write route that sets the
// verdict of one vendor code, which is one language of one template. It is a
// review-only control: it is compiled and mounted only under the review tag, it
// exists so the SMS template journey can observe a language settle while its
// sibling stays pending, and it is absent from every other assembly.
//
// The route hangs on the admin endpoint, whose paths the authentication gateway
// identifies and whose routes the handler-internal guards decide; this harness
// makes no permission decision, and the review build binds loopback addresses
// only (see review.go's guard). The body names a vendor code rather than a
// template name and language because the carrier is the seam that holds a
// reviewer's answer, and the code is the vendor's per-language identifier the
// operator supplied when the template was registered.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
	smslog "github.com/vislake/speed/pkg/sms/log"
)

// reviewSMSControlModuleName is the module name of the review-only control. The
// serve roster names it; a default build registers nothing under it.
const reviewSMSControlModuleName = "review.sms.control"

// reviewSMSControlEndpoint is the endpoint carrying the control route. The
// admin endpoint is already reached by the console's authenticated writes, and
// it claims the /api/v1/system/sms prefix the route below sits under.
const reviewSMSControlEndpoint = "admin"

// reviewSMSControlPath is the control route. It is a review-only path: no other
// assembly mounts anything under it.
const reviewSMSControlPath = "/api/v1/system/sms/review/verdicts"

// reviewSMSControlFeature is the static feature the console's verdict control
// reads. It is present exactly while this module is in force, which is only in
// a review run, so a baseline deployment offers no such control.
const reviewSMSControlFeature = "review.sms.control"

// init registers the control module. The import is the wiring; reviewModules is
// what names it for a run.
func init() { core.ProcessRegistry.Register(reviewSMSControlModule()) }

// reviewVerdictRequest is the control body: the vendor code whose verdict moves,
// the state it moves to, and the vendor's explanation when it rejects or
// withdraws a template.
type reviewVerdictRequest struct {
	Code   string `json:"code"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// reviewSMSControlModule is the control's descriptor.
//
// The carrier provider and the HTTP entry point are required: without the
// former there is no verdict store to move, and without the latter a route has
// nothing to mount on. Both absences are wiring failures a run reports rather
// than a control that quietly does nothing.
func reviewSMSControlModule() core.Module {
	return core.Module{
		Name: reviewSMSControlModuleName,
		Requires: []core.Requirement{
			{Token: (*sms.Provider)(nil)},
			{Token: (*speedhttp.Router)(nil)},
		},
		Resources: []any{
			feature.Spec{Name: reviewSMSControlFeature},
		},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			provider, err := core.Resolve[sms.Provider](reg)
			if err != nil {
				return fmt.Errorf("%s: taking up the review carrier: %w", reviewSMSControlModuleName, err)
			}
			carrier, ok := provider.(smslog.Reviewer)
			if !ok {
				return fmt.Errorf("%s: the mounted SMS provider is %T, not the log carrier",
					reviewSMSControlModuleName, provider)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return fmt.Errorf("%s: taking up the HTTP entry point: %w", reviewSMSControlModuleName, err)
			}
			endpoint, err := router.Endpoint(reviewSMSControlEndpoint)
			if err != nil {
				return fmt.Errorf("%s: looking up the %q endpoint: %w",
					reviewSMSControlModuleName, reviewSMSControlEndpoint, err)
			}
			endpoint.Route(http.MethodPost, reviewSMSControlPath, reviewVerdictHandler(carrier))
			return nil
		},
	}
}

// reviewVerdictHandler moves one code's verdict through the carrier's Review
// control. A body it cannot read, or a state the carrier does not accept, is a
// 400; a verdict that landed is a 204 with no body.
func reviewVerdictHandler(carrier smslog.Reviewer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in reviewVerdictRequest
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the body is not a review verdict"})
			return
		}
		if err := carrier.Review(in.Code, sms.ApprovalState(in.State), in.Reason); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		log.Default().Info("review sms: a template review verdict was set",
			"code", in.Code, "state", in.State)
		c.Status(http.StatusNoContent)
	}
}
