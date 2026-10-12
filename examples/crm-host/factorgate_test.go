package main

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestTheFactorGateSummaryRefusesAnonymousCallersWithAChallenge is the case for
// the factor-gate route's handler when it runs with no identity on the request,
// which is what a mounting that left the guard out gives it: it refuses, and the
// refusal carries a WWW-Authenticate challenge, which RFC 9110 requires of every
// 401. The handler is driven alone, ahead of no guard, so the branch that answers
// an anonymous caller is the one under test rather than the guard's own refusal.
func TestTheFactorGateSummaryRefusesAnonymousCallersWithAChallenge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, path := splitRoute(factorGateRoute)
	router := gin.New()
	router.GET(path, factorGateSummary)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, path, nil))

	if recorder.Code != nethttp.StatusUnauthorized {
		t.Fatalf("the handler answered an anonymous caller %d, want 401", recorder.Code)
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	if challenge == "" {
		t.Fatalf("the 401 carries no WWW-Authenticate challenge, which RFC 9110 requires of a 401")
	}
	if !strings.Contains(challenge, "Bearer") {
		t.Errorf("the 401's challenge %q names no scheme, want one beginning with Bearer", challenge)
	}
}
