# AGENTS.md — @speed/console-ui

## What this package is

The platform-staff console — the second shell. It renders the three
backend administration surfaces (the tenant roster over `/tenant/admin`,
the settings read-write surface over `/settings/admin`, the audit query
over `/audit/admin`) as one console frame with three pages. Those
endpoints are mounted on hosts that do not resolve a tenant, so the
console has **no tenant switcher and no tenant-scoped query key**: it is
not a page of `@speed/tenancy-ui` or `@speed/product-shell`, and neither
of those packages belongs in its import graph.

The package ships one entry (`src/index.ts`, the export set in the
README's "What ships") and no subpath. Everything under `src/internal/`
— the error-text resolver, the `InlineError` binding and the translation
hook — is family plumbing and is deliberately not exported.

## The second shell (the one thing this package's frame decides)

`ConsoleShell` is the platform frame: a `header` banner (the console
title and any host header actions), a `nav` landmark derived from the
labelled declarations, and a `main` landmark holding the host's routed
content. It is deliberately **not** `@speed/layout-kit`'s `AppShell`:
this package does not depend on layout-kit, so the frame is built from
MUI primitives directly, and it is simpler than the tenant-facing frame
on purpose — no responsive drawer, no tenant affordances.

The shell does no routing. It never matches a URL, never builds a link
from a path and never decides which navigation entry is selected: the
declarations are collected and rendered by the application's route layer
(a consumer of `@speed/route-kit`, or the host's own), and the console's
navigation entries are plain links to each labelled declaration's path.
The frame therefore renders only what the host gives it — the labelled
declarations for the navigation, and `children` for the content.

## The page declarations

Each administration capability contributes one declaration to the
declarative registration seam: `tenantAdminRoutes`, `settingsAdminRoutes`
and `auditAdminRoutes`, or the three combined as `consoleRoutes`. A
declaration carries a path pattern, a view, an optional navigation label
and the permission code the page's data is held to (`tenant.read`,
`settings.read`, `audit.read`). The shapes in `src/route.ts` are kept
structurally identical to `@speed/route-kit`'s registration surface so a
declaration this package exports is assignable to route-kit's
`RouteDeclaration` **without importing route-kit** — same-tier packages
never import one another.

A declaration's label is a `ConsoleNavLabel` element, not a string: the
declarations stay plain module-level values (the seam wants a value)
while their labels resolve their text through the console-ui namespace
when they render, so the navigation translates reactively with the
host's language.

The permission code is data. This package never decides on it: the
server judges every request, and the host reads the code to compose any
route-level guard. A page renders its write controls whether or not the
caller holds the write permission and lets the server refuse — the
server is the authority, and the page must not invent a client-side gate.

## The operations seam (why the pages read through a bound bundle)

The pages read and write through the `ConsoleOperations` bundle the host
mounts in `ConsoleOperationsProvider` (`useConsoleOperations`): a plain
object whose members are the react-query read hooks and write mutations
of the three admin surfaces. The hooks run on the host's query client,
the mutations go through the same bound client, and no page builds a
query client of its own or writes a query key — the keys live inside the
hooks the host binds.

Why the pages read through a bundle and not a direct `@speed/api-sdk`
call each: the bundle is the substitution point. The shipped
`consoleOperations` is a thin adapter over the generated `@speed/api-sdk`
operations of the three admin surfaces (`/tenant/admin`,
`/settings/admin`, `/audit/admin`): the reads run the generated query
options through `useQuery` with a `select` that flattens the page
envelope to the view rows, and the writes call the generated mutation
functions and invalidate the list through the generated key factories.
`@speed/api-sdk` is generated from the platform's merged contract
(`contracts/api.json`), which carries all three surfaces, so the adapter
imports real generated operations. The provider seam stays because a host
may mount its own bundle and a suite does so to script one (see Testing).

What the pages reach through the bundle, one member per route of the
three modules:

| Member | Route |
|---|---|
| `useTenants` | `GET /tenant/admin/tenants` |
| `useCreateTenant` | `POST /tenant/admin/tenants` |
| `useSetTenantAlias` | `PATCH /tenant/admin/tenants/:id` |
| `useSetTenantStatus` | `POST /tenant/admin/tenants/:id/status` |
| `useSettings` | `GET /settings/admin/settings[/:namespace]` |
| `useSetSetting` | `PUT /settings/admin/settings/:namespace/:key` |
| `useRemoveSetting` | `DELETE /settings/admin/settings/:namespace/:key` |
| `useAuditRecords` | `GET /audit/admin/records` |

The view types (`ConsoleTenant`, `ConsoleSetting`,
`ConsoleAuditRecord`) are the generated response rows of each module's
admin surface (`TenantAdminTenant`, `SettingsAdminSetting`,
`AuditAdminRecord`), named for the console's own pages; only the write
inputs stay hand-written, because they carry route parameters beside the
body.

One address detail the bundle encodes: the settings route addresses an
item by its namespace and **the rest of the key under it** (`:key`), the
full key being `<namespace>.<key>` — and a module name may itself contain
a dot (`authn.session`), so the rest cannot be derived by splitting on the
first dot. `settingKeyRest(fullKey, namespace)` computes the rest (the
page uses it on a row's full key); the roster and audit routes carry no
such split.

## Non-negotiable rules

- **No tenant concept, anywhere.** No tenant switcher, no
  tenant-scoped query key, no `useCurrentTenant`, no import of
  `@speed/tenancy-ui` or `@speed/auth-core`. The console is the platform
  side; the tenant side is a different shell. A change that wants to
  read "the current tenant" here is scope drift — that is tenancy-ui's.
- **No routing in package code.** No path matching, no link
  construction, no `react-router`. `ConsoleShell` derives the navigation
  from labelled declarations and links each to its path; matching a URL
  to a view is the application's route layer's.
- **The pages read and write only through the operations bundle.** No
  page constructs a `QueryClient`, and none writes a query key. If a
  change looks like it wants to `fetch`, add an HTTP concern or build a
  key, that is scope drift — the read is an operation on the bundle.
- **No second masking and no client-side authorization.** A sensitive
  setting's value arrives masked from the server and renders as-is; the
  permission a declaration carries is never a gate this package applies.
- **Framework peers stay peers.** `react`, `react-dom`, `@mui/material`,
  `@emotion/*` and `@tanstack/react-query` are peer (required)
  dependencies; `i18n`, `ui-kit` and `api-sdk` are regular dependencies.
  The manifest is pinned by `src/package.json.test.ts`.
- **Every built-in string is a console-ui namespace key.** The
  `speed/no-literal-text` rule (workspace config) enforces it over
  `src/`; the two bundles under `src/locales/` carry identical leaf key
  sets, pinned by `src/resources.test.ts` and
  `src/internal/error-text.test.ts`.

## Testing

Unit tests are vitest + jsdom, one file per source under `src/`, shared
helpers only in `test-utils/`. `renderWithProviders` mounts the unit
under the tree a real host builds — `I18nextProvider` around `ui-kit`'s
`AppThemeProvider` around `QueryClientProvider` around
`ConsoleOperationsProvider` — with a fresh i18n instance (the console-ui
and ui-kit namespaces registered), a fresh no-retry query client, and a
caller-supplied operations bundle. `test-utils/operations.tsx` provides
that bundle: `createConsoleOperations(backend)` builds real react-query
hooks over a scripted in-memory backend (`makeConsoleBackend`), so the
pages exercise genuine react-query states — the first-load pending
branch, a settled empty answer, a failed read, and a write whose success
invalidates the list — with no HTTP transport.

Suites: `ConsoleShell` (the three landmarks, the labelled-only
navigation, the in-place skip link, the page-tier axe scan);
`TenantRosterPage`, `SettingsPage` and `AuditPage` (their reads, writes
and failure branches, each with a widget-tier axe scan — the page
renders its own h1); `routes` (the declarations' paths, permissions and
translated labels through the shell); `resources` and
`internal/error-text` (bundle key parity and whitelist pairing);
`package.json` (the manifest contract); and `usage-example` (the README's
Quick start composition, with the operations bundle substituted from
test-utils).

## Deferrals (recorded, do not re-open silently)

- **The refusal-code mapping.** The shipped bundle reads the generated
  `@speed/api-sdk` admin operations, so those are wired (see "The
  operations seam"). Still open is the wire shape of a refusal: the
  module answers RFC 9457 problem details with a `type` URN, and how
  that `type` becomes the frontend `code` the api-client derives is not
  settled — the error-text whitelist's colon-free codes
  (`tenant.conflict`, `settings.invalid_value`) are this package's
  reading of the modules' error surfaces and move when the mapping is
  fixed.
- **Responsive frame.** The frame is a persistent sidebar (stacked on
  narrow viewports), not a drawer: no `matchMedia`-driven responsive
  behaviour is shipped. A drawer is additive and belongs to the frame if
  a host needs one.
- **Audit detail view.** Only the list route is surfaced; a record's
  detail route (`GET /audit/admin/records/:id`) and an export surface
  are not. Export is another permission and another module's to carry.
- **Storybook.** No preview harness exists; the components are covered by
  jsdom tests + axe, and color-contrast is verified nowhere — the same
  as the packages below.
