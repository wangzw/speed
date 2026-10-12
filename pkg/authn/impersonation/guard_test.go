package impersonation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/guard"
)

// TestAnImpersonatingIdentityIsKeyedByItsActor holds the identity the module
// carries: the accountability key is the administrator, not the target, so a
// route's rate limit and audit are charged to the administrator.
func TestAnImpersonatingIdentityIsKeyedByItsActor(t *testing.T) {
	id := impersonatingIdentity(t, sessionBackend)
	if id.Key() != string(id.Actor()) || id.Key() != string(actorID) {
		t.Errorf("Key() = %q, want the actor %q", id.Key(), actorID)
	}
	if id.Account() != targetID {
		t.Errorf("Account() = %q, want the target %q", id.Account(), targetID)
	}
	if !id.Impersonating() {
		t.Error("the identity does not read as impersonating")
	}
}

// TestGuardRequireRefusesAnImpersonatingIdentity holds the acceptance the design
// names: because the level is capped at a single factor, a route that requires a
// multi-factor authentication refuses an impersonating identity with the step-up
// 401, whatever the administrator's own session carried.
func TestGuardRequireRefusesAnImpersonatingIdentity(t *testing.T) {
	ctx := authntest.Context(t, context.Background(), impersonatingOpts(sessionBackend)...)

	reached := false
	e := gin.New()
	e.GET("/x", guard.Require(authn.Requirement{Assurance: authn.AssuranceMulti}), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if reached {
		t.Error("the handler past the step-up middleware ran")
	}
}
