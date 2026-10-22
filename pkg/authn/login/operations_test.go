package login

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// TestOnlyTheRoutesThatCanRefuseForCapacityDeclareRetryAfter holds each 503
// declaration to the one producer of a retry delay: the password derivation
// refused for capacity. A route that never derives one must not promise a
// header that can never appear on its answer.
func TestOnlyTheRoutesThatCanRefuseForCapacityDeclareRetryAfter(t *testing.T) {
	canRefuseForCapacity := map[string]bool{
		"authn.login.start":              true,
		"authn.login.startAuthenticated": true,
		"authn.login.answer":             true,
		"authn.login.changePassword":     true,
		"authn.login.register":           true,
	}
	for _, operation := range operations {
		descriptor := operation.Descriptor()
		for _, response := range descriptor.Responses {
			if response.Status != http.StatusServiceUnavailable {
				continue
			}
			declares := false
			for _, header := range response.Headers {
				if header.Name == "Retry-After" {
					declares = true
				}
			}
			if declares != canRefuseForCapacity[descriptor.ID] {
				t.Errorf("%s declares Retry-After on its 503 = %v, want %v",
					descriptor.ID, declares, canRefuseForCapacity[descriptor.ID])
			}
		}
	}
}

// TestTheIdentifierRemovalDeclaresTheLoginNameConflict holds that the route
// declares the 409 login-identifier-required its account-module call answers
// when the removal would leave a password account with no login name.
func TestTheIdentifierRemovalDeclaresTheLoginNameConflict(t *testing.T) {
	found := false
	for _, operation := range operations {
		descriptor := operation.Descriptor()
		if descriptor.ID != "authn.login.removeIdentifier" {
			continue
		}
		found = true
		declared := false
		for _, response := range descriptor.Responses {
			if response.Status == http.StatusConflict && response.ProblemType == typePrefix+mount.CodeLoginIdentifierRequired {
				declared = true
			}
		}
		if !declared {
			t.Error("authn.login.removeIdentifier does not declare the 409 login-identifier-required of a password account's last login name")
		}
	}
	if !found {
		t.Fatal("authn.login.removeIdentifier is not declared")
	}
}

// TestEveryBodyRouteDeclaresTheBodyLimit holds the 413 every route with a
// declared body answers with when a request announces more than the endpoint
// reads. The capability's own 64 KiB limit is answered 400 before this one.
func TestEveryBodyRouteDeclaresTheBodyLimit(t *testing.T) {
	for _, operation := range operations {
		descriptor := operation.Descriptor()
		if descriptor.Request == nil || descriptor.Request.MediaType == "" {
			continue
		}
		declared := false
		for _, response := range descriptor.Responses {
			if response.Status == http.StatusRequestEntityTooLarge {
				declared = true
			}
		}
		if !declared {
			t.Errorf("%s declares a request body but no 413", descriptor.ID)
		}
	}
}
