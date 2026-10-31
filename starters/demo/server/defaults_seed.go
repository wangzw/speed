//go:build !deploy

package main

// demoSeedTenants is the business tenant the default build's seed section names:
// a walkthrough tenant whose only administrator is the development operator, so
// the console's tenant-addressed pages answer out of the box.
//
// The deploy build leaves it out, because the Fly configuration names the
// deployment's own accounts and no business tenant, and the seed tenant list is
// a container leaf that file does not replace. See defaults_seed_deploy.go.
func demoSeedTenants() []any {
	return []any{
		map[string]any{
			"name":   "acme",
			"admins": []any{"operator"},
		},
	}
}
