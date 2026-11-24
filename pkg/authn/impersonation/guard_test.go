package impersonation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/guard"
)

// TestAnImpersonatingIdentityIsKeyedByItsActor checks that the key is the actor, not the target.
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

// TestGuardRequireDecidesAnImpersonatingIdentityByTheOperatorsLevel checks that the
// operator's assurance decides a step-up requirement on an impersonating identity.
func TestGuardRequireDecidesAnImpersonatingIdentityByTheOperatorsLevel(t *testing.T) {
	cases := []struct {
		name       string
		assurance  authn.Assurance
		wantStatus int
		wantPass   bool
	}{
		{"a multi-factor operator meets a multi-factor requirement", authn.AssuranceMulti, http.StatusOK, true},
		{"a single-factor operator falls short of it", authn.AssuranceSingle, http.StatusUnauthorized, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := authntest.Context(t, context.Background(),
				authntest.WithAccount(targetID),
				authntest.WithActor(actorID),
				authntest.WithSession(sessionID),
				authntest.WithScope(tenantA),
				authntest.WithAssurance(c.assurance),
				authntest.WithAuthTime(time.Now()),
				authntest.WithBackend(sessionBackend),
			)

			reached := false
			e := gin.New()
			e.GET("/x", guard.Require(authn.Requirement{Assurance: authn.AssuranceMulti}), func(c *gin.Context) {
				reached = true
				c.Status(http.StatusOK)
			})
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx))

			if w.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, c.wantStatus)
			}
			if reached != c.wantPass {
				t.Errorf("the handler past the step-up middleware ran = %t, want %t", reached, c.wantPass)
			}
		})
	}
}
