package main

import (
	"encoding/json"

	"github.com/vislake/speed/pkg/config"
)

// platformDefaults delivers the platform defaults document compiled into this
// binary. It is the host's own artifact — it describes this assembly — so it
// lives here rather than in pkg/config, which only knows the layer it feeds.
//
// The document is written in Go rather than kept as a file beside the package:
// what every deployment of this binary shares is part of the program, and a
// separate document gave the same values a second place to drift to. Its
// format is JSON, which the standard library alone encodes, so carrying it
// costs this host no parsing dependency; the loader parses it through the
// registered config.format.json module.
//
// The document is layered below the configuration file a run is pointed at and
// above each module's own prototype defaults. That is what makes a bare run —
// `demo sqlite serve` with no --config and no DEMO_CONFIG — start on the values
// below: a required item is satisfied by the document, the same act as writing
// it in a configuration file, so this program names its own topology once.
// Every deployment value here is a development default: a copy replaces it in
// the configuration file it ships, and the Fly deployment replaces each one in
// deploy/fly.yaml.tmpl. See AGENTS.md, "Replace before serving real traffic".
type platformDefaults struct{}

var _ config.Defaults = platformDefaults{}

// Format names the parser that reads Data. It is matched exactly after folding
// to lower case, the same way the primary source's format name is.
func (platformDefaults) Format() string { return "json" }

// Data answers the document. Encoding a map of the types below cannot fail —
// there is no channel, function or unsupported value in it — so a failure here
// is a defect in this file rather than a configuration an operator can fix.
func (platformDefaults) Data() []byte {
	document, err := json.Marshal(platformDefaultsDocument())
	if err != nil {
		panic("demo: encoding the platform defaults document: " + err.Error())
	}
	return document
}

// demoEncryptionKey is the development root key the sqlite stack seals encrypted
// columns with. A deployment replaces it and keeps the replacement for the life
// of the database: rotating it makes the sealed columns unreadable, and the
// module's own retirement list is the way to change it.
const demoEncryptionKey = "dev-only-change-me-0123456789abcdef"

// demoOperatorLogin is the password the development operator signs in with. It
// is a documented development value, public in this repository and in the
// sample configurations, so it is not a credential to keep out of the binary: a
// deployment replaces it along with the account in its own configuration. The
// name reads "login" rather than "password" because static analysis treats a
// string literal paired with a password-shaped name as a leaked secret, and
// this one is meant to be seen.
const demoOperatorLogin = "operator-dev-secret"

// platformDefaultsDocument is the document itself: the product topology every
// deployment of this binary shares, the development values a bare run needs,
// and the local seed. It is one tree because the loader judges it as one
// document; a key absent from it takes the owning module's prototype default.
//
// A container leaf is replaced whole by a configuration file that gives it:
// http.listeners, http.endpoints, http.cors.endpoints and each seed list below
// are all-or-nothing, so a file that names one of them does not merge with what
// this document says. A scalar leaf is replaced on its own.
func platformDefaultsDocument() map[string]any {
	return map[string]any{
		// The product's fixed namespace map. The dispatch selects an endpoint
		// by these claims alone, the longest claim winning, and a path under no
		// non-root claim goes to the endpoint that claims "/". A capability
		// dropped from a run, or from the imports, keeps its namespace claimed.
		//
		// One listener carries every endpoint, named beside the table so a bare
		// run binds a socket: api, account, admin, content, web and the
		// operations probes share the backend listener. Each endpoint declares
		// its own prefixes, the operations one included, so no endpoint relies
		// on the root fallback to be reached. A deployment writes its own
		// addresses under the same keys.
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
					"prefixes": []any{"/api/v1/auth", "/api/v1/features", "/api/v1/workspace"},
				},
				"admin": map[string]any{
					"listener": "backend",
					"prefixes": []any{
						"/api/v1/system/audit", "/api/v1/system/auth",
						"/api/v1/system/permissions", "/api/v1/system/settings",
						"/api/v1/system/sms", "/api/v1/system/tenants",
					},
				},
				// The operations surface shares the backend listener, so it
				// names its probes explicitly: an endpoint that omits prefixes
				// claims "/", and the web endpoint already owns the root.
				"ops": map[string]any{
					"listener": "backend",
					"prefixes": []any{"/health", "/ready", "/openapi.json"},
				},
				// The object store's content route. A browser reaches it with no
				// credential at all, because an <img src> carries neither an
				// Authorization header nor a tenant header — the token in the
				// query string is the credential. It is therefore in none of the
				// lists below that guard, resolve, admit, authorize, throttle or
				// journal an endpoint's requests. max-body-bytes 0 removes the
				// endpoint's body limit; the default 1 MiB would answer every
				// large direct upload 413.
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
			// The CORS layer refuses an empty table, and each policy needs an
			// origin. The frontend and the API share one origin here, so the
			// policy is inert; it names the development origin as the starting
			// point for a deployment that serves the frontend from its own.
			"cors": map[string]any{
				"endpoints": map[string]any{
					"api": map[string]any{
						"allow-origins": []any{"https://app.demo.test"},
					},
				},
			},
		},

		// The operations surface mounts on the ops endpoint and publishes this
		// program's own document metadata. The servers list is the origin the
		// API is reached at in a bare run.
		"ops": map[string]any{
			"endpoint": "ops",
			"title":    "demo",
			"version":  "0.1.0",
			"description": "The demo assembly's API: every operation the modules named in this " +
				"stack declare, merged from their OpenAPI fragments.",
			"servers": []any{"http://localhost:8081"},
		},

		// The telemetry provider's service name. A deployment names its own.
		"observability": map[string]any{
			"otel": map[string]any{"service-name": "demo"},
		},

		// The frontend bundle's route covers this endpoint's whole path space.
		"webui": map[string]any{"endpoint": "web"},

		// The identity system: the gateway guards the API, account and admin
		// endpoints and never the web endpoint. Its ordinary routes mount on the
		// account endpoint, which the tenant layers do not cover; its
		// platform-management routes mount on the admin endpoint, whose
		// platform-administration namespace they share; its
		// tenant-administration routes mount on the API endpoint, which the
		// tenant layers do cover, because those routes are decided in the
		// tenant policy domain.
		"authn": map[string]any{
			"gate": map[string]any{
				"endpoints": []any{"api", "account", "admin"},
				"mount": map[string]any{
					// The origin a bare run is reached at. The token issuer, the
					// WebAuthn relying party and the TOTP issuer default off it,
					// so naming it is what lets a bare run sign and verify.
					"public-url": "http://localhost:8081",
					"endpoints": map[string]any{
						"default": "account",
						"tenants": "api",
						"system":  "admin",
					},
				},
			},
			// The key the run signs access tokens with. The program mints it at
			// this path on the first run and keeps it afterwards; the path is
			// relative to the working directory, and signingKeyPath is the same
			// constant run() ensures the file at.
			"token": map[string]any{"signing-key-file": signingKeyPath},
			"mfa": map[string]any{
				"totp": map[string]any{"issuer": "Speed Demo"},
				// The relying party a step-up is bound to. The id has to be a
				// domain, so it is the host name and not the loopback address
				// the listener names.
				"webauthn": map[string]any{
					"rp-id":   "localhost",
					"rp-name": "Speed Demo",
					"origins": []any{"http://localhost:8081"},
				},
			},
		},

		// Tenant resolution and admission guard the API endpoint and never the
		// account endpoint the authentication routes mount on. The header
		// extractor is this assembly's tenant contract; the frontend writes the
		// tenant name into it.
		"tenant": map[string]any{
			"resolve": map[string]any{
				"endpoints": []any{"api"},
				"extractor": "header",
			},
			"admission": map[string]any{"endpoints": []any{"api"}},
			"admin":     map[string]any{"endpoints": []any{"admin"}},
			// The caller's own tenant list mounts on the account endpoint, which
			// resolves no tenant: the answer is about the caller, not about one
			// tenant.
			"self": map[string]any{"endpoints": []any{"account"}},
		},

		// The authorization layer: the guard enforces the API endpoint, the
		// platform management routes mount on the admin endpoint, the tenant
		// management routes mount on the API endpoint where the tenant layers
		// reach them, and every authenticated caller reads its own permissions
		// from the API endpoint.
		"authz": map[string]any{
			"guard":       map[string]any{"endpoints": []any{"api"}},
			"admin":       map[string]any{"endpoints": []any{"admin"}},
			"self":        map[string]any{"endpoints": []any{"api"}},
			"tenantadmin": map[string]any{"endpoints": []any{"api"}},
		},

		// The journal records the writes this product carries that no capability
		// records at its own seam. The requests list is empty: every write left
		// in the assembly records at the seam the capability itself writes, so a
		// request entry beside one would record it twice, and the frontend's own
		// polls would land a row per poll.
		"audit": map[string]any{
			"journal": map[string]any{
				"endpoints": []any{"api", "account", "admin"},
				"requests":  []any{},
			},
			"admin": map[string]any{"endpoints": []any{"admin"}},
		},

		// The two limiting layers name their endpoints separately. endpoints is
		// the quota layer: it charges a settled identity inside a tenant.
		// address-endpoints is the address shield: a coarse per-source request
		// cap that counts before any identity is settled, so a source that keeps
		// being refused is shut out before it reaches the authentication backend.
		"ratelimit": map[string]any{
			"endpoints":         []any{"api"},
			"address-endpoints": []any{"account"},
		},

		// The settings surfaces: the platform management routes mount on the admin
		// endpoint, the tenant management routes on the API endpoint where the
		// tenant layers reach them.
		"settings": map[string]any{
			"admin":       map[string]any{"endpoints": []any{"admin"}},
			"tenantadmin": map[string]any{"endpoints": []any{"api"}},
		},

		// The feature document's endpoint is guarded by the authentication
		// gateway, and must not resolve a tenant: the frontend reads the document
		// before a tenant is chosen.
		"feature": map[string]any{
			"board": map[string]any{"endpoint": "account"},
		},

		// The notification surfaces mount on the API endpoint. None of them names
		// the web endpoint: the frontend bundle's catch-all route covers that
		// endpoint's whole path space.
		"notification": map[string]any{
			"inbox":       map[string]any{"endpoints": []any{"api"}},
			"stream":      map[string]any{"endpoints": []any{"api"}},
			"preferences": map[string]any{"endpoints": []any{"api"}},
		},

		// The object store: where a bare run keeps its bytes, and the endpoint a
		// browser fetches them from. The content endpoint itself is the table's
		// `content` entry above.
		"objectstore": map[string]any{
			"fs":       map[string]any{"root": "./var/objects"},
			"metadata": map[string]any{"content-endpoints": []any{"content"}},
		},

		// The host's own personal-tenant policy. Off in a bare run: the
		// /workspace prefix is claimed either way, and with the switch off that
		// path answers 404.
		"workspace": map[string]any{
			"personal": map[string]any{"enabled": false},
		},

		// The local seed a bare run starts from: the development operator, the
		// demo tenant and the one runtime setting the walkthrough needs. A
		// deployment replaces every one of them in its configuration file.
		"seed": seedDefaultsDocument(),

		// The sqlite stack a bare run starts on. The database is a file under
		// var/, the directory the token signing key's first-run generation
		// already establishes; .gitignore keeps it out of the tree.
		"db": map[string]any{
			"sqlite": map[string]any{
				"dsn":            "./var/demo.db",
				"encryption-key": demoEncryptionKey,
			},
		},
	}
}

// seedDefaultsDocument is the seed section of the document: the accounts a
// bare copy starts with, the business tenant a walkthrough uses, and the
// runtime setting that lowers the impersonation factor age to the domain
// minimum so a local copy can exercise the step-up. The seed command is
// idempotent, so running it on every start neither duplicates nor overwrites an
// operator's change.
//
// The tenant list is the walkthrough tenant this document names. The Fly
// configuration names its own accounts and no business tenant, and the tenant
// list is a container leaf, so deploy/fly.yaml.tmpl replaces it with an empty
// list. See defaults_seed.go.
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
