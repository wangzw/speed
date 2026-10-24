//go:build !review

package main

// The default-build side of the impersonation fixture's isolation. The review
// build carries the SMS second factor and a one-minute impersonation freshness
// window so the journey is walkable; neither may reach a baseline binary.

import (
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

// smsFactorModuleName is the SMS second factor's module name. It is written as a
// literal on purpose: importing the package would run its init and register the
// module, which is exactly what a default build must not do.
const smsFactorModuleName = "authn.mfa.sms"

// baselineMaxAuthAge is the declared default of authn.impersonation.max-auth-age.
const baselineMaxAuthAge = 5 * time.Minute

// TestDefaultBuildCarriesNoSMSFactorModule holds the baseline roster: a binary
// built without the review tag registers no module named authn.mfa.sms, so a
// session signed in with TOTP has no SMS factor to step up with and the review
// assembly's step-up branch is absent.
func TestDefaultBuildCarriesNoSMSFactorModule(t *testing.T) {
	if module, ok := core.ProcessRegistry.Lookup(smsFactorModuleName); ok {
		t.Fatalf("the default build registers the module %q (%+v); the SMS second factor belongs "+
			"to the review assembly and must be imported only behind //go:build review",
			smsFactorModuleName, module)
	}
}

// TestDefaultBuildKeepsTheImpersonationDefaults holds the other half: the review
// configuration lowers authn.impersonation.max-auth-age to one minute, but the
// declared default the baseline reads stays at five minutes. The value is read
// from the settings schema the impersonation module declares, so a change to the
// declared default fails here even though no baseline configuration file names
// the key.
func TestDefaultBuildKeepsTheImpersonationDefaults(t *testing.T) {
	for _, resource := range core.Resources[settings.Schema](core.ProcessRegistry) {
		if resource.Value.Namespace != "authn.impersonation" {
			continue
		}
		for _, mount := range resource.Value.Mounts {
			value := reflect.ValueOf(mount.Value)
			if value.Kind() == reflect.Pointer {
				value = value.Elem()
			}
			field := value.FieldByName("MaxAuthAge")
			if !field.IsValid() || !field.CanInterface() {
				t.Fatalf("the impersonation settings mount %T carries no readable MaxAuthAge field",
					mount.Value)
			}
			got, ok := field.Interface().(time.Duration)
			if !ok {
				t.Fatalf("the impersonation MaxAuthAge default is a %T, want a time.Duration",
					field.Interface())
			}
			if got != baselineMaxAuthAge {
				t.Errorf("the declared authn.impersonation.max-auth-age default = %v, want %v; "+
					"the one-minute value belongs to the review configuration alone", got, baselineMaxAuthAge)
			}
			return
		}
	}
	t.Fatalf("the default build declares no authn.impersonation settings schema")
}
