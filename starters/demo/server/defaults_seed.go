package main

func demoSeedTenants() []any {
	return []any{
		map[string]any{
			"name":   "acme",
			"admins": []any{"operator"},
		},
	}
}

// demoSeedAccounts is the one source of the default account list, so the
// document and the seed guard's fallback comparison cannot disagree.
func demoSeedAccounts() []seedAccount {
	return []seedAccount{{
		Identifier:    "operator",
		Password:      demoOperatorLogin,
		DisplayName:   "Demo Operator",
		PlatformAdmin: true,
	}}
}
