//go:build !review

package main

// The default-build side of the impersonation fixture. The SMS second factor is
// compiled into the capability ceiling every build carries (see
// capabilities.go); whether a run enables it is a configuration decision. The
// impersonation fixture's one-minute freshness window stays in the review
// configuration alone, and the declared default a baseline run reads is
// unchanged.

import (
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

// smsFactorModuleName is the SMS second factor's module name. It is written as
// a literal on purpose: this case asserts the ceiling without importing the
// package directly.
const smsFactorModuleName = "authn.mfa.sms"

// baselineMaxAuthAge is the declared default of authn.impersonation.max-auth-age.
const baselineMaxAuthAge = 5 * time.Minute

// TestDefaultBuildCompilesTheSMSFactor holds the ceiling: a binary built
// without the review tag registers the SMS second factor, so a deployment can
// enable the step-up branch from configuration. Before the capability roster
// became unconditional the module was imported only behind //go:build review.
func TestDefaultBuildCompilesTheSMSFactor(t *testing.T) {
	t.Parallel()
	if _, ok := core.ProcessRegistry.Lookup(smsFactorModuleName); !ok {
		t.Fatalf("the default build registers no module %q; the SMS second factor is part of the "+
			"capability ceiling every build carries", smsFactorModuleName)
	}
}

// TestDefaultBuildKeepsTheImpersonationDefaults holds the other half: the review
// configuration lowers authn.impersonation.max-auth-age to one minute, but the
// declared default the baseline reads stays at five minutes. The value is read
// from the settings schema the impersonation module declares, so a change to the
// declared default fails here even though no baseline configuration file names
// the key.
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
					"the one-minute value belongs to the review configuration alone", got, baselineMaxAuthAge)
			}
			return
		}
	}
	t.Fatalf("the default build declares no authn.impersonation settings schema")
}
