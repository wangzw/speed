package main

// demoSeedTenants is the business tenant the platform defaults document's seed
// section names: a walkthrough tenant whose only administrator is the
// development operator, so the console's tenant-addressed pages answer out of
// the box.
//
// The Fly deployment replaces the seed tenant list with an empty one in
// deploy/fly.yaml.tmpl, because it names the deployment's own accounts and no
// business tenant. The seed tenant list is a container leaf that a
// configuration file replaces whole, so the template's empty list wins over this
// one.
func demoSeedTenants() []any {
	return []any{
		map[string]any{
			"name":   "acme",
			"admins": []any{"operator"},
		},
	}
}

// demoSeedAccounts is the account list the defaults document seeds: the
// development operator the walkthrough signs in with. It is the one source of
// that list, so the document and the seed guard's fallback comparison cannot
// disagree about which accounts are the demonstration ones.
func demoSeedAccounts() []seedAccount {
	return []seedAccount{{
		Identifier:    "operator",
		Password:      demoOperatorLogin,
		DisplayName:   "Demo Operator",
		PlatformAdmin: true,
	}}
}
