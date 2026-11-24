package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

const smsFactorModuleName = "authn.mfa.sms"

const baselineMaxAuthAge = 5 * time.Minute

// TestDefaultBuildCompilesTheSMSFactor holds that the binary registers the SMS second factor so a
// deployment can enable the step-up branch.
func TestDefaultBuildCompilesTheSMSFactor(t *testing.T) {
	t.Parallel()
	if _, ok := core.ProcessRegistry.Lookup(smsFactorModuleName); !ok {
		t.Fatalf("the binary registers no module %q; the SMS second factor is part of the "+
			"capability ceiling every build carries", smsFactorModuleName)
	}
}

// TestDefaultBuildKeepsTheImpersonationDefaults holds that the declared default of
// authn.impersonation.max-auth-age stays five minutes.
func TestDefaultBuildKeepsTheImpersonationDefaults(t *testing.T) {
	t.Parallel()
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
					"the one-minute value belongs to the e2e configuration alone", got, baselineMaxAuthAge)
			}
			return
		}
	}
	t.Fatalf("the binary declares no authn.impersonation settings schema")
}
