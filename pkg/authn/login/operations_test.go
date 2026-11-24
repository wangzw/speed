package login

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// TestOnlyTheRoutesThatCanRefuseForCapacityDeclareRetryAfter holds each 503 declaration to the capacity refusal that carries a retry delay.
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

// TestTheIdentifierRemovalDeclaresTheLoginNameConflict holds that the route declares the 409 login-identifier-required it can answer.
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

// TestTheIdentifierRemovalDeclaresTheImmutableUsername holds that the route declares the 409 username-immutable it can answer.
func TestTheIdentifierRemovalDeclaresTheImmutableUsername(t *testing.T) {
	found := false
	for _, operation := range operations {
		descriptor := operation.Descriptor()
		if descriptor.ID != "authn.login.removeIdentifier" {
			continue
		}
		found = true
		declared := false
		for _, response := range descriptor.Responses {
			if response.Status == http.StatusConflict && response.ProblemType == typePrefix+mount.CodeUsernameImmutable {
				declared = true
			}
		}
		if !declared {
			t.Error("authn.login.removeIdentifier does not declare the 409 username-immutable of a username removal")
		}
	}
	if !found {
		t.Fatal("authn.login.removeIdentifier is not declared")
	}
}

// TestEveryBodyRouteDeclaresTheBodyLimit holds that every route with a declared body answers the 413.
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
