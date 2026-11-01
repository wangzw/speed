package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// MarkStepUp records the distinction a layer charging by outcome needs: a 401
// that asks for stronger authentication is not a 401 that refused a credential,
// and the status alone cannot tell them apart. A request nothing marked reads as
// not a step-up.
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
