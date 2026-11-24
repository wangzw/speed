package personal

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/tenant"
)

func TestTheDescriptorIsStaticAndDeliversTheThreeCapabilities(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module name is %q, want %q", m.Name, ModuleName)
	}
	want := map[core.Token]bool{
		(*authn.AccountLifecycle)(nil):           false,
		(*provisioning.AccountProvisioning)(nil): false,
		(*tenant.KindResolver)(nil):              false,
	}
	for _, provision := range m.Provides {
		if _, ok := want[provision.Token]; ok {
			want[provision.Token] = true
		}
	}
	for token, found := range want {
		if !found {
			t.Errorf("the module does not provide %v", token)
		}
	}
	var static bool
	for _, resource := range m.Resources {
		switch value := resource.(type) {
		case feature.Spec:
			if value.Name != FeatureName || value.Conditional {
				t.Errorf("the feature is %+v, want %q static", value, FeatureName)
			}
			static = true
		case config.Schema:
			t.Errorf("the module declares configuration %q; it is always in force", value.Namespace)
		}
	}
	if !static {
		t.Error("the module declares no static feature")
	}
}
