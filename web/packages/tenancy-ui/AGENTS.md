# AGENTS.md -- @speed/tenancy-ui

Rules for working in this package and for consuming it. `@speed/tenancy-ui`
ships the tenant-switch affordance: one controlled `TenantSwitcher`
component, its props, the `tenancy-ui` i18n namespace resources, and
nothing else. It is installed into business projects via `npm install`,
so everything below that touches the public surface is contract, not
style.

## Public surface

`src/index.ts` exports `TENANCY_UI_NAMESPACE`, `tenancyUiResources`,
`TenantOption`, `TenantSwitcherProps` and `TenantSwitcher`, and nothing
more. Every other `src/` file is `internal/` plumbing: the translation
hook. It exists to be tested, never to be imported by a consumer.
Changes are classified by TypeScript's source semantics: a new export and
a new optional field are source-compatible; removing an export or field,
changing an existing export's or field's meaning or name, or changing a
signature (parameters or return type) is incompatible. `TenantOption` is
`{ id, name }` with both fields readonly -- adding an optional field is
source-compatible and leaves a host that does not set it unchanged;
adding a required field, removing a field, or changing a field's meaning
or name is incompatible. `TenantSwitcherProps` is `{ tenants,
currentTenantId, onSelect }`, all three required, with `onSelect`
`(tenantId: string) => void`, plus the optional `menuFooter`,
`announcement` and `triggerProps`. Making a required prop optional is
source-compatible in type terms but breaks this control's semantic
contract (a host that stops passing it still compiles), so it is a
breaking change; re-introducing a `session` prop is consumer-visible
breaking.

## Controlled discipline (non-negotiable in src/)

- The tenant list and the current tenant id are host data, passed as
  props; the component never derives the current tenant and never
  remembers a previous one. `src/` never calls `attachSession`, never
  consumes `useAuthState`/`useCurrentTenant`/`usePermission` (those are
  the host's observation points), and knows no session at all.
- Nothing in `src/` navigates, fetches, or touches the network in any
  form. Switching tenants is a HOST ROUTE CHANGE -- the server resolves
  the current tenant per request, so there is no switch endpoint to call
  -- and the component reports the user's pick through `onSelect` only.
- The component exists to report the user's pick and reports it through
  `onSelect` exactly once per selection, after which the host switches
  its route, updates the tenant context it mounts on @speed/auth-core's
  `TenantProvider`, and evicts the departing tenant's query cache.
- The menu must never offer a way to re-select the current tenant: its
  row stays disabled and its click guard returns before `onSelect` can
  be called.
- The selector is synchronous: it closes the menu, calls `onSelect` once,
  and announces the switched-to tenant. A throwing `onSelect` is
  contained -- it is not a selection failure, so it renders no error and
  never escapes as an unhandled rejection.

## Same-tier boundary

This package sits beside `auth-ui` and `ui-kit` at the same layer. It
never imports `auth-ui` (same-layer dependency is prohibited). It
depends on `@speed/i18n` for the translation hook, and renders MUI
primitives (`Button`/`Menu`/`MenuItem`) the host supplies through its
MUI/React tree. `@speed/auth-core`, `@speed/api-client` and
`@speed/api-sdk` are devDependencies: `src/` does not import them and no
public type of this package references them -- the test suite composes a
host over auth-core's tenant context, but the shipped component does
not. `src/` takes only the shared `visuallyHiddenSx` clip recipe from
`ui-kit`; its theme provider stays test-only: it appears only in
`test-utils/`, because the test tree must render under a real host
composition.

## i18n

Every built-in string lives in the bilingual `tenancy-ui` namespace,
two leaves per language (`tenantSwitcher.noCurrentTenant` and
`tenantSwitcher.switchedTo`). Keep the leaf key sets of `zh-CN.json` and
`en-US.json` identical -- `registerNamespace` refuses to register a
namespace whose languages' leaf key sets differ, so the failure surfaces
at registration, before any component can render. Tests must assert
language text by importing the shipped bundles (`src/locales/*.json`)
and reading the leaf -- never by inlining a translation string into a
test. The trigger label and the list rows are host text
(`TenantOption.name`), rendered verbatim and never translated; the
built-in strings cover only the component's own states.

## Testing layout

One test file per source file (`TenantSwitcher.tsx` ->
`TenantSwitcher.test.tsx`), shared helpers only in `test-utils/`. There
is no network to script: the component sends no request, so the tests
drive it through `renderWithProviders` (the host composition: bilingual
i18n + the ui-kit theme provider) and assert what the user sees
(the trigger label, the menu rows and their disabled state, the
`role="status"` announcement) and what the host receives (`onSelect`
calls). `src/usage-example.test.tsx` compiles and executes the README
quick start, composing the host over auth-core's `TenantProvider` /
`useCurrentTenant`, so the documented usage cannot drift from the API.

Behaviour tests assert what the user sees and what observably changed
(the fired callbacks), never internal call counts of the component under
test.

## Host checklist

Consumers wiring `TenantSwitcher` must (all documented in the README
quick start): pass the current tenant from their own routing source
(`currentTenantId`), switch their route in `onSelect`, re-attach
`/me`-derived permission lists after the switch, and evict the previous
tenant's query cache (auth-core's `evictTenantQueries`, called with the
tenant id read before the switch). None of that happens here, by design.

## Language

Code, comments, TSDoc and this file are English. User-visible product
text is bilingual and lives only in the namespace resources.
