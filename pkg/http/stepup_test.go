package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Fail marks the answer it writes when that answer is the step-up 401, so every path that writes
// the type is covered without knowing about the marker: the mount helpers, the gateway middleware.
func TestFailMarksTheStepUpAnswerByItsType(t *testing.T) {
	cases := []struct {
		name   string
		status int
		typ    string
		want   bool
	}{
		{"the step-up 401", http.StatusUnauthorized, TypeStepUp, true},
		{"a refused credential", http.StatusUnauthorized, "urn:speed:authn:invalid_credentials", false},
		{"the step-up type on another status", http.StatusNotFound, TypeStepUp, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/things", nil)
			Fail(c, Problem{Status: tc.status, Type: tc.typ})
			if got := IsStepUp(c); got != tc.want {
				t.Fatalf("IsStepUp = %t, want %t", got, tc.want)
			}
		})
	}
}

// MarkStepUp records the distinction a layer charging by outcome needs: a 401 that asks for
// stronger authentication is not a 401 that refused a credential.
func TestMarkStepUpMarksOnlyTheRequestItIsCalledOn(t *testing.T) {
	marked, _ := gin.CreateTestContext(httptest.NewRecorder())
	if IsStepUp(marked) {
		t.Fatal("a fresh request reads as a step-up")
	}

	MarkStepUp(marked)
	if !IsStepUp(marked) {
		t.Fatal("a marked request does not read as a step-up")
	}

	other, _ := gin.CreateTestContext(httptest.NewRecorder())
	if IsStepUp(other) {
		t.Fatal("the marker reached a context it was not written on")
	}
}
