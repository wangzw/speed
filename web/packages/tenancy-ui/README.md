# @speed/tenancy-ui

The tenant-switch affordance of a frontend built on speed: one
controlled component, `TenantSwitcher`, that renders the host's current
tenant as a trigger and the host's tenant list as a menu, and reports the
user's pick through `onSelect`. The tenant is a request's routing fact --
the server resolves the current tenant per request from the host, a
subdomain or a path, and authentication carries no tenant -- so switching
tenants is a HOST ROUTE CHANGE, not an operation on a session. The
component sits at the same tier as `ui-kit` and `auth-ui` and is entirely
controlled: it knows no session, consumes none of the auth-core hooks,
never navigates and never touches the network. Every built-in string
renders from the bilingual `tenancy-ui` namespace registered through
`@speed/i18n`.

## What ships

| Module | Exports |
|---|---|
| `TenantSwitcher.tsx` | `TenantSwitcher`, `TenantSwitcherProps`, `TenantOption` |
| `resources.ts` | `TENANCY_UI_NAMESPACE`, `tenancyUiResources` |

Everything else (`src/internal/`) is shared plumbing -- the translation
hook -- and is deliberately not exported.

## Quick start

A host holds the current tenant (derived from its own route), mounts it on
`@speed/auth-core`'s `TenantProvider`, and renders the switcher where the
current tenant belongs. Selecting a tenant calls `onSelect`, and the host
does all of the work there: it switches its route, updates the tenant
context it hands back as `currentTenantId` (so the trigger converges),
evicts the departing tenant's query cache, and re-attaches `/me`-derived
permission lists. This package is the auth-agnostic neighbour of `auth-ui`
at the same tier, never its importer.

```tsx
import { useState } from 'react'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import { TenantProvider, useCurrentTenant } from '@speed/auth-core'
import {
  TENANCY_UI_NAMESPACE,
  tenancyUiResources,
  TenantSwitcher,
} from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'

// The bilingual instance, both namespaces registered exactly once. The
// ui-kit namespace is registered because a host app composes under
// ui-kit (the AppThemeProvider below) and renders ui-kit chrome, which
// reads that namespace. registerNamespace validates identical leaf key
// sets before it mutates.
const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)

// The host's tenant list -- which tenants the signed-in user may switch
// between is the host's data, fetched through its own generated
// operations. The switcher renders it verbatim.
const tenants: TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
]

// The host's app chrome: currentTenantId flows from useCurrentTenant, so
// a committed switch re-renders the trigger onto the new tenant.
function Header({ onSwitch }: { onSwitch: (id: string) => void }) {
  const current = useCurrentTenant()
  return (
    <header>
      <TenantSwitcher
        tenants={tenants}
        currentTenantId={current?.tenantId ?? null}
        onSelect={(tenantId) => {
          // The host's own post-selection work, all of it the host's:
          //   - evict the departing tenant's cache with the id read
          //     before the switch:
          //       evictTenantQueries(queryClient, current!.tenantId)
          //   - re-attach the /me-derived permission lists;
          //   - then switch the route:
          onSwitch(tenantId)
        }}
      />
    </header>
  )
}

// The host's route: this state stands in for the tenant the route
// resolves to, and switching it is the host's own navigation.
function App() {
  const [tenantId, setTenantId] = useState<string>('tenant-1')
  return (
    <TenantProvider tenantId={tenantId}>
      <Header onSwitch={setTenantId} />
    </TenantProvider>
  )
}

export function Root() {
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <App />
      </AppThemeProvider>
    </I18nextProvider>
  )
}
```

This exact composition is compiled and executed by the package suite
(`src/usage-example.test.tsx`): it shows the trigger on the current
tenant, opens the list, picks a second tenant — `onSelect` fires once
with its id, the host switches its state, the trigger converges onto the
new tenant and the switch is announced — then switches back. The
documented usage cannot drift from the API.

## TenantSwitcher

`TenantSwitcher` props:

| Prop | Type | Notes |
|---|---|---|
| `tenants` | `readonly TenantOption[]` (required) | the host-supplied list; `TenantOption` is `{ id: string; name: string }`, both readonly |
| `currentTenantId` | `string \| null` (required) | the tenant the host's route resolves to, from the host's own data source (typically `useCurrentTenant()`) |
| `onSelect` | `(tenantId: string) => void` (required) | fired exactly once when the user picks a non-current row; the host's route-change hook |
| `menuFooter` | `(close: () => void) => ReactNode` (optional) | host entries rendered after the tenant rows under a divider in the same menu; `close()` dismisses the menu |
| `announcement` | `'inline' \| 'visually-hidden'` (optional; default `inline`) | whether the switched-to confirmation is painted beside the trigger or stays visually hidden while announcing |
| `triggerProps` | `Pick<ButtonProps, 'variant' \| 'size' \| 'sx' \| 'startIcon'>` (optional) | presentation spread onto the trigger button, so it can match the host's chrome |

Behaviour, all of it controlled and test-pinned:

- **The trigger shows the current tenant.** Its label is the matched
  `tenants` row's `name` -- host data, rendered verbatim, never
  translated. With no matching row, or with `currentTenantId` null, the
  trigger is disabled and shows the `tenantSwitcher.noCurrentTenant`
  text.
- **Opening the list shows every tenant; the current row is disabled and
  can never re-trigger a selection.** Its `onClick` guard returns before
  `onSelect` can be called, so even a synthetic click on the disabled row
  changes nothing.
- **Picking another row calls `onSelect(id)` once.** The selection is a
  synchronous hand-off: the menu closes, `onSelect` fires exactly once
  with the picked tenant's id, and the component announces the
  switched-to tenant. The host switches its route in the callback -- the
  component itself never navigates, never fetches, and knows no session.
- **A selection is announced, never silent.** One `role="status"` live
  region reports the `tenantSwitcher.switchedTo` text naming the picked
  tenant -- a context change that silently alters which rows the host
  shows must say so out loud, and it belongs in a live region because a
  person using a screen reader cannot glance at the chrome to check. The
  confirmation stays until the next selection replaces it (no
  auto-dismiss timer), so its lifetime never races a screen reader's
  processing of it. The region is mounted from the first render, empty
  and clipped, so the first selection fills a region the screen reader
  already knows. The `announcement` prop chooses whether the
  confirmation is painted (`inline`, the default) or stays clipped for
  its whole lifetime (`visually-hidden`, for a host whose chrome has no
  room for it). The component renders no error surface: the switch
  itself is the host's, and its failures belong to the host's route and
  data layer.
- **Host entries can follow the tenant rows.** `menuFooter(close)`
  renders its content after the list, under a divider, inside the same
  menu. The supplied `close` dismisses the menu, so an entry that
  navigates elsewhere calls it first. Without it the menu contains only
  the tenant rows.
- **The trigger accepts presentation.** `triggerProps` carries
  `variant`, `size`, `sx` and `startIcon` onto the trigger button, so the
  control can match the host's own chrome; without it the trigger is the
  outlined button that inherits the ambient text color.
- **The component is controlled and never changes its own display.** The
  trigger keeps showing the host-supplied `currentTenantId`'s row until
  the host hands back a new id; it never derives the current tenant and
  never remembers a previous one.
- **A throwing `onSelect` is contained.** It is not a selection failure:
  the component renders no error, the throw does not escape the
  fire-and-forget row handler as an unhandled rejection, and the
  selection is still announced.

## Text and i18n

Every built-in string ships in the bilingual `tenancy-ui` namespace,
identical leaf sets per language under one section:

- `tenantSwitcher` -- the two component states: `noCurrentTenant` and
  `switchedTo`.

`registerNamespace` enforces the standing discipline -- canonical
language keys, full coverage, identical leaf key sets across languages --
before it mutates, so a key added to one language file and forgotten in
the other fails registration. The suite pins the bilingual values by
importing the shipped JSON bundles, never by inlining language. The
trigger label and the list rows are host text (`TenantOption.name`),
rendered verbatim and never translated; the built-in strings cover only
the component's own states.

## Accessibility

The trigger is a `Button` with `aria-haspopup="menu"`, `aria-expanded`
and `aria-controls` on the open list; the menu renders MUI's menu
semantics, with the current-tenant row `disabled` -- announced and
skipped by assistive tech, and inert even to synthetic clicks. The
switched-to confirmation is a `role="status"` live region (announced
without interrupting) mounted from the first render; with
`announcement="visually-hidden"` it is clipped to the 1x1 box but stays
in the accessibility tree, never `display: none`. The component suite
runs axe over the open list, with and without a host menu footer.
As in `ui-kit` and `auth-ui`, `color-contrast` stays axe-disabled in
jsdom (no real paint); contrast is asserted at the theme-value level in
the component suite.

## Testing

Unit tests are vitest + jsdom, one file per source file under `src/`,
shared helpers only in `test-utils/`. The vitest config aliases the
`@speed/*` specifiers onto the sibling packages' `src` entries, so tests
run against live sources -- no test file imports another package's
`dist/`:

- `render.tsx` -- `renderWithProviders` mounts a unit under the tree a
  real host builds (`I18nextProvider` around `AppThemeProvider`), with a
  fresh bilingual instance per call and both namespaces a rendered
  component can read registered. Bilingual assertions import the shipped
  bundles (`../locales/zh-CN.json`, `en-US.json`), never inline a
  language literal.
- `axe.ts` -- the widget-tier axe assertion (shared with `auth-ui`).

The component sends no request, so there is no network rig to script:
`src/TenantSwitcher.test.tsx` drives the component through
`renderWithProviders` and asserts what the user sees (trigger label, list
rows and their disabled state, the `role="status"` announcement) and what
the host receives (`onSelect` calls); `src/usage-example.test.tsx`
compiles and executes the Quick start composition, composing the host
over auth-core's `TenantProvider` / `useCurrentTenant`.

## Dependencies

| Package | Kind | Why |
|---|---|---|
| `react`, `react-dom` | peer (required, ^18 or ^19) | the host owns the React tree |
| `@mui/material` | peer (required, ^9.5) | the `Button`/`Menu`/`MenuItem` primitives and the ambient theme |
| `@emotion/react`, `@emotion/styled` | peer (required, ^11) | MUI's own runtime requirements |
| `@speed/i18n` | dependency | the namespace registration and the translation hook every string renders through |
| `@speed/ui-kit` | dependency | the shared `visuallyHiddenSx` clip recipe, the MUI theme layer the host composes under, and the `ui-kit` namespace a host app registers |

`@speed/auth-core`, `@speed/api-client` and `@speed/api-sdk` are
devDependencies: no `src/` code references them and no public type of
this package does either -- the test suite composes a host over
auth-core's tenant context, but the shipped component does not. The
component's `src/` takes only the shared `visuallyHiddenSx` clip recipe
from `ui-kit`; its theme provider is test-only -- it appears only in
`test-utils/`, never in `src/`, because the test tree must render under a
real host composition. No direct HTTP exists anywhere in `src/` -- the component
sends no request at all -- and the workspace's `speed/no-direct-http`
rule enforces that (this package is simply absent from the rule's one
whitelist, `packages/api-client`); the `speed/no-literal-text` rule
enforces the namespace discipline over `src/` the same way it does in
every package.

## Known limitations

- **The trigger label and list entries are host text.** The switcher
  renders the `tenants` rows verbatim; tenant names are the host's data,
  translated by nobody. The built-in strings cover only the component's
  own states.
- **The current tenant is the host's fact.** `TenantSwitcher` shows
  `currentTenantId`'s row and reports a selection through `onSelect`; a
  host that never updates the prop after a selection shows a stale
  trigger. The quick start's `useCurrentTenant` flow is the intended
  shape.
- **The switch itself is the host's.** The component does not switch a
  route, fetch data, re-attach permission lists or evict caches -- the
  host does all of that in `onSelect`, and every failure of the switch
  (no membership, a failed token check, an unreachable route) is the
  host's to surface in its own data layer. This package carries no error
  table.

## Deferrals and recorded decisions

- **The tenant list has no in-package source.** No endpoint answers
  "which tenants may this principal switch between" in the shipped
  surface, so the list is host data by contract.
- **Auth-core stays host-side by design.** The switcher takes the tenant
  list and the current tenant as props and reports through a callback;
  the host derives the current tenant (typically through auth-core's
  `useCurrentTenant`) and observes transitions with `useAuthState`. A
  component that consumed the hooks would have to be mounted under the
  host's `attachSession` anyway.
- **Storybook / browser-side visual verification**: no preview harness
  exists (the same deferral `ui-kit`, `layout-kit` and `auth-ui` carry);
  `color-contrast` stays axe-disabled for the same jsdom reason, and
  contrast is asserted at the theme-value level in the component suite.

## Development

From `web/packages/tenancy-ui`: `pnpm lint`, `pnpm typecheck`, `pnpm
test`, `pnpm build`. The build compiles `@speed/ui-kit` and `@speed/i18n`
first -- the sibling declarations this package resolves to -- and then
emits this package's `dist/`; `pnpm build` from the `web/` root does the
same for every package. Shared test helpers live in `test-utils/`,
bilingual fixtures are the shipped locale files under `src/locales/`, and
`src/usage-example.test.tsx` compiles and executes the Quick start
composition above, so the documented usage cannot drift from the API.
