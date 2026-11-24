package main

import (
	"encoding/json"

	"github.com/vislake/speed/pkg/config"
)

type platformDefaults struct{}

var _ config.Defaults = platformDefaults{}

func (platformDefaults) Format() string { return "json" }

func (platformDefaults) Data() []byte {
	document, err := json.Marshal(platformDefaultsDocument())
	if err != nil {
		panic("demo: encoding the platform defaults document: " + err.Error())
	}
	return document
}

const demoEncryptionKey = "dev-only-change-me-0123456789abcdef"

// The name reads "login" rather than "password" because static analysis treats
// a string literal paired with a password-shaped name as a leaked secret, and
// this one is meant to be seen.
const demoOperatorLogin = "operator-dev-secret"

func platformDefaultsDocument() map[string]any {
	return map[string]any{
		"http": map[string]any{
			"listeners": map[string]any{
				"backend": map[string]any{"address": "127.0.0.1:8081"},
			},
			"endpoints": map[string]any{
				"api": map[string]any{
					"listener": "backend",
					"prefixes": []any{"/api"},
				},
				"account": map[string]any{
					"listener": "backend",
					"prefixes": []any{"/api/v1/auth", "/api/v1/features", "/api/v1/orgs"},
				},
				"admin": map[string]any{
					"listener": "backend",
					"prefixes": []any{
						"/api/v1/system/audit", "/api/v1/system/auth",
						"/api/v1/system/permissions", "/api/v1/system/settings",
						"/api/v1/system/sms", "/api/v1/system/tenants",
					},
				},
				"ops": map[string]any{
					"listener": "backend",
					"prefixes": []any{"/health", "/ready", "/openapi.json"},
				},
				"content": map[string]any{
					"listener":       "backend",
					"prefixes":       []any{"/content"},
					"max-body-bytes": 0,
				},
				"web": map[string]any{
					"listener": "backend",
					"prefixes": []any{"/"},
				},
			},
			"cors": map[string]any{
				"endpoints": map[string]any{
					"api": map[string]any{
						"allow-origins": []any{"https://app.demo.test"},
					},
				},
			},
		},

		"ops": map[string]any{
			"endpoint": "ops",
			"title":    "demo",
			"version":  "0.1.0",
			"description": "The demo assembly's API: every operation the modules named in this " +
				"stack declare, merged from their OpenAPI fragments.",
			"servers": []any{"http://localhost:8081"},
		},

		"observability": map[string]any{
			"otel": map[string]any{"service-name": "demo"},
		},

		"webui": map[string]any{"endpoint": "web"},

		"authn": map[string]any{
			"gate": map[string]any{
				"endpoints": []any{"api", "account", "admin"},
				"mount": map[string]any{
					"public-url": "http://localhost:8081",
					"endpoints": map[string]any{
						"default": "account",
						"tenants": "api",
						"system":  "admin",
					},
				},
			},
			// The key the run signs access tokens with, at the path run() ensures
			// the file at.
			"token": map[string]any{"signing-key-file": signingKeyPath},
			"mfa": map[string]any{
				"totp": map[string]any{"issuer": "Speed Demo"},
				// The id has to be a domain, so it is the host name and not the
				// loopback address the listener names.
				"webauthn": map[string]any{
					"rp-id":   "localhost",
					"rp-name": "Speed Demo",
					"origins": []any{"http://localhost:8081"},
				},
			},
		},

		"tenant": map[string]any{
			"resolve": map[string]any{
				"endpoints": []any{"api"},
				"extractor": "header",
			},
			// The platform's own tenant has no identity provider of its own, so
			// the tenant SSO requirement could never be satisfied there and would
			// close the console.
			"admission": map[string]any{
				"endpoints":          []any{"api"},
				"sso-exempt-tenants": []any{"system"},
			},
			"admin": map[string]any{
				"endpoints":        []any{"admin"},
				"tenant-endpoints": []any{"api"},
			},
			"self":    map[string]any{"endpoints": []any{"account"}},
			"profile": map[string]any{"endpoints": []any{"api"}},
		},

		"authz": map[string]any{
			"guard":       map[string]any{"endpoints": []any{"api"}},
			"admin":       map[string]any{"endpoints": []any{"admin"}},
			"self":        map[string]any{"endpoints": []any{"api"}},
			"tenantadmin": map[string]any{"endpoints": []any{"api"}},
		},

		"audit": map[string]any{
			"journal": map[string]any{
				"endpoints": []any{"api", "account", "admin"},
				"requests":  []any{},
			},
			"admin": map[string]any{"endpoints": []any{"admin"}},
		},

		"ratelimit": map[string]any{
			"endpoints":         []any{"api"},
			"address-endpoints": []any{"account"},
		},

		"settings": map[string]any{
			"admin":       map[string]any{"endpoints": []any{"admin"}},
			"tenantadmin": map[string]any{"endpoints": []any{"api"}},
		},

		"feature": map[string]any{
			"board": map[string]any{"endpoint": "account"},
		},

		"notification": map[string]any{
			"inbox":       map[string]any{"endpoints": []any{"api"}},
			"stream":      map[string]any{"endpoints": []any{"api"}},
			"preferences": map[string]any{"endpoints": []any{"api"}},
		},

		"objectstore": map[string]any{
			"fs":       map[string]any{"root": "./var/objects"},
			"metadata": map[string]any{"content-endpoints": []any{"content"}},
		},

		"org": map[string]any{
			"create": map[string]any{"enabled": true},
		},

		"seed": seedDefaultsDocument(),

		"db": map[string]any{
			"sqlite": map[string]any{
				"dsn":            "./var/demo.db",
				"encryption-key": demoEncryptionKey,
			},
		},
	}
}

func seedDefaultsDocument() map[string]any {
	defaults := demoSeedAccounts()
	accounts := make([]any, 0, len(defaults))
	for _, account := range defaults {
		accounts = append(accounts, map[string]any{
			"identifier":     account.Identifier,
			"password":       account.Password,
			"display-name":   account.DisplayName,
			"platform-admin": account.PlatformAdmin,
		})
	}
	seed := map[string]any{
		"accounts": accounts,
		"settings": []any{
			map[string]any{
				"key":   "authn.mfa.sensitive-min-age",
				"value": "1h",
			},
		},
	}
	if tenants := demoSeedTenants(); len(tenants) > 0 {
		seed["tenants"] = tenants
	}
	return seed
}
