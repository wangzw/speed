package demotest

// ExpectedOwners returns the prefix-to-endpoint table the claim cases judge
// against. It is written here rather than read from the configuration under
// test, so the table cannot agree with a run whose selection derives from the
// assembly. Each call answers a fresh map.
func ExpectedOwners() map[string]string {
	return map[string]string{
		"/api":                       "api",
		"/api/v1/auth":               "account",
		"/api/v1/features":           "account",
		"/api/v1/orgs":               "account",
		"/api/v1/system/auth":        "admin",
		"/api/v1/system/audit":       "admin",
		"/api/v1/system/permissions": "admin",
		"/api/v1/system/settings":    "admin",
		"/api/v1/system/sms":         "admin",
		"/api/v1/system/tenants":     "admin",
		"/content":                   "content",
		// The operations surface shares the backend listener, so its probes are
		// non-root claims of that listener like the API namespaces.
		"/health":       "ops",
		"/ready":        "ops",
		"/openapi.json": "ops",
	}
}
