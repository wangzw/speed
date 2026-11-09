//go:build review

package main

// The review build's two control routes, exercised on a running binary.
//
// Both routes exist so a browser walkthrough can drive a state a real
// deployment reaches through a vendor: the SMS verdict route moves one
// language's review verdict, and the notice route asks the fixture to dispatch
// one notice to the caller. The e2e specs click controls that call them, but a
// deleted mount would only surface there; this case signs in to the review
// binary itself and posts each route, so an unmounted or misregistered route
// fails in the Go suite.
//
// The actor is the seeded platform operator. Their sign-in completes the TOTP
// factor the review fixture seeds, whose secret is read back through the
// product's own encrypted serializer, so the session is a real one rather than
// a manufactured token. The notice route is tenant-scoped: the operator is not
// a member of the seeded tenant by design (the impersonation fixture requires
// that), so the case reads their personal tenant's name from
// GET /api/v1/auth/me/tenants and posts under it.

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

// The verdict the control is asked to set. The code need not be one the
// template registry holds: the carrier stores a verdict per vendor code, and
// the route's work is that store's write.
const (
	reviewControlVerdictCode  = "review-control-en"
	reviewControlVerdictState = sms.ApprovalApproved
)

// TestReviewOnlyControlRoutesAreMounted signs in as the seeded platform
// operator and posts each review-only control route: the SMS verdict route on
// the admin endpoint answers 2xx, and so does the tenant notice route on the
// api endpoint once the operator's personal tenant is current.
func TestReviewOnlyControlRoutesAreMounted(t *testing.T) {
	base, token := reviewImpersonationSession(t)
	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{"Authorization": "Bearer " + token}

	verdict := send(t, client, http.MethodPost, base+reviewSMSControlPath, map[string]string{
		"code":  reviewControlVerdictCode,
		"state": string(reviewControlVerdictState),
	}, headers)
	if verdict.status < http.StatusOK || verdict.status >= http.StatusMultipleChoices {
		t.Fatalf("POST %s answered %d, want a 2xx:\n%s",
			reviewSMSControlPath, verdict.status, verdict.body)
	}

	// The notice route reads the tenant from the request context and checks the
	// caller's membership; the operator's personal tenant is the one the review
	// build creates for them.
	headers["X-Tenant"] = personalTenantName(t, client, base, token)
	notice := send(t, client, http.MethodPost, base+reviewNotificationControlPath, nil, headers)
	if notice.status < http.StatusOK || notice.status >= http.StatusMultipleChoices {
		t.Fatalf("POST %s answered %d, want a 2xx:\n%s",
			reviewNotificationControlPath, notice.status, notice.body)
	}
}
