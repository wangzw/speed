//go:build deploy

package main

// demoSeedTenants returns nothing in the deploy build. The Fly configuration
// names the deployment's own accounts and starts with no business tenant; the
// seed tenant list is a container leaf that file does not replace, so a
// document naming the demo tenant here would create it on every deployment.
// The files of the other build are defaults_seed.go.
func demoSeedTenants() []any { return nil }
