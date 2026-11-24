package main

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/invite"
	"github.com/vislake/speed/pkg/core"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// TestTheDemoAssemblyProvidesWhatTheInvitationModuleRequires holds that the catalog module satisfies
// the invitation module's tenant description requirement.
func TestTheDemoAssemblyProvidesWhatTheInvitationModuleRequires(t *testing.T) {
	t.Parallel()
	invitation, ok := core.ProcessRegistry.Lookup(invite.ModuleName)
	if !ok {
		t.Fatalf("the demo build registers no %q", invite.ModuleName)
	}
	requires := false
	for _, requirement := range invitation.Requires {
		if requirement.Token == (*authn.TenantDescriptionResolver)(nil) && !requirement.Optional {
			requires = true
		}
	}
	if !requires {
		t.Fatalf("%q does not require authn.TenantDescriptionResolver", invite.ModuleName)
	}

	catalog, ok := core.ProcessRegistry.Lookup(tenantcatalog.ModuleName)
	if !ok {
		t.Fatalf("the demo build registers no %q", tenantcatalog.ModuleName)
	}
	provides := false
	for _, provision := range catalog.Provides {
		if provision.Token == (*authn.TenantDescriptionResolver)(nil) {
			provides = true
		}
	}
	if !provides {
		t.Errorf("%q does not deliver authn.TenantDescriptionResolver", tenantcatalog.ModuleName)
	}
	if !slices.Contains(baselineServe, invite.ModuleName) {
		t.Errorf("the serve roster does not name %q", invite.ModuleName)
	}
	if !slices.Contains(baselineServe, tenantcatalog.ModuleName) {
		t.Errorf("the serve roster does not name %q", tenantcatalog.ModuleName)
	}
}
