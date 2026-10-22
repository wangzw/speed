package login

import (
	"net/http"
	"testing"
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

// TestTheIdentifierRemovalDeclaresNoConflict holds that the route declares no
// 409: the account module's identifier removal never answers the
// last-login-method refusal, which belongs to unbinding an external identity.
// The route's own description says the last identifier can be removed.
func TestTheIdentifierRemovalDeclaresNoConflict(t *testing.T) {
	found := false
	for _, operation := range operations {
		descriptor := operation.Descriptor()
		if descriptor.ID != "authn.login.removeIdentifier" {
			continue
		}
		found = true
		for _, response := range descriptor.Responses {
			if response.Status == http.StatusConflict {
				t.Error("authn.login.removeIdentifier declares a 409 its account-module call cannot produce")
			}
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
