//go:build review

package main

// The review build's notification dispatch control: a route a tenant member
// clicks to ask the fixture to dispatch one notice to themselves.
//
// The route is a test control, not product API. It is compiled only under the
// review tag and mounted only by the review build's fixture; a deployment or
// baseline binary carries neither the route nor this file. It is registered
// open on the authorization guard because it is an ordinary member's action,
// not a permission; the handler answers for the signed-in caller and the review
// build binds loopback addresses only (see review.go). The frontend still gates
// the control on the demo.notification.control feature, which this file
// declares and the default build does not.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// reviewNotificationControlFeature is the review-only feature the console's
// notice control reads. It is present exactly while this build's fixture is in
// force, so a baseline deployment offers no such control.
const reviewNotificationControlFeature = "demo.notification.control"

// reviewNotificationControlPath is the control's route on the api endpoint. The
// tenant control posts to it to dispatch one notice for the calling member; no
// other assembly mounts anything under it, because this file is compiled only
// under the review build tag.
const reviewNotificationControlPath = "/api/v1/notifications/review/notice"

// reviewControlResources is the fixture's review-only resource contribution:
// the feature the console reads before it offers the control. A default build's
// build-tag counterpart answers nothing.
func reviewControlResources() []any {
	return []any{feature.Spec{Name: reviewNotificationControlFeature}}
}

// mountReviewControl mounts the dispatch control for the fixture instance. A
// default build's counterpart is a no-op.
func mountReviewControl(reg *core.Registry, notifier *demoNotifier) error {
	return wireReviewControl(reg, notifier)
}

// wireReviewControl mounts the review-only control route and opens it on the
// authorization guard's declaration surface.
//
// The route is a review-assembly artefact and nothing more: this file is
// compiled only under the review build tag, so a baseline binary carries
// neither the route nor this function. It is registered open because the tenant
// control is an ordinary member's action, not a permission; the handler answers
// for the signed-in caller. Both surfaces are optional in the same way
// wireDemoRecipient's are: a run without the HTTP entry point has no route to
// mount, and one without the guard has no declaration table to write.
func wireReviewControl(reg *core.Registry, notifier *demoNotifier) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point for the control: %w",
			demoNotificationModuleName, err)
	}
	endpoint, err := router.Endpoint(demoNotificationEndpoint)
	if err != nil {
		return fmt.Errorf("%s: looking up the %q endpoint for the control: %w",
			demoNotificationModuleName, demoNotificationEndpoint, err)
	}
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case err == nil:
		gate.Open(http.MethodPost, reviewNotificationControlPath)
	case !errors.Is(err, core.ErrMissingProvider):
		return fmt.Errorf("%s: taking up the route-declaration surface: %w",
			demoNotificationModuleName, err)
	}
	endpoint.Route(http.MethodPost, reviewNotificationControlPath, notifier.handleControl)
	return nil
}

// handleControl answers the tenant control: one signed-in member asks the
// review assembly to dispatch one notice to themselves.
//
// The caller is the recipient, so the target is their own account and their own
// identifiers; the tenant comes from the request context the resolution layer
// set. Membership is checked here rather than assumed: the route is open on the
// permission guard, so nothing else would stop an account that does not belong
// to the tenant from asking for a tenant-scoped dispatch. The response is 204:
// the dispatch is enqueued, and the delivery is the pipeline's to settle.
func (n *demoNotifier) handleControl(c *gin.Context) {
	ctx := c.Request.Context()
	identity, ok := authn.FromContext(ctx)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "the review notice control requires a signed-in account"})
		return
	}
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the review notice control requires a tenant"})
		return
	}
	member, err := n.members.IsMember(ctx, identity.Account(), string(tenantID))
	if err != nil {
		n.logger.Error(demoNotificationModuleName+": the control could not read the membership",
			"tenant", string(tenantID), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the membership could not be read"})
		return
	}
	if !member {
		c.JSON(http.StatusForbidden, gin.H{"error": "the review notice control is for the tenant's members"})
		return
	}
	plan, ok, err := n.notify(ctx, tenantID, identity.Account(), uuid.NewString())
	if err != nil {
		n.logger.Error(demoNotificationModuleName+": the control could not dispatch the notice",
			"tenant", string(tenantID), "account", string(identity.Account()), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the review notice could not be dispatched"})
		return
	}
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "the account has neither a proved phone number nor an email address to dispatch to",
		})
		return
	}
	n.logger.Info(demoNotificationModuleName+": the review notice control dispatched a notice",
		"tenant", string(tenantID), "account", string(identity.Account()), "channels", plan.Channels)
	c.Status(http.StatusNoContent)
}
