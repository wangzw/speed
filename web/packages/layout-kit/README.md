# @speed/layout-kit

Shared app chrome for any project built on speed: `AppShell` (a
responsive header, nav drawer and content region) and `RouteGuard` (a
route/content gate driven entirely by a host-injected status). Both
components are controlled and props-driven, at the same architectural
tier as `@speed/ui-kit` -- no business, tenant, or auth-mechanism
semantics live here. `RouteGuard` in particular takes its
allow/deny/pending decision as a plain `status` value, never a
callback it invokes and never a concrete authentication or routing
import, so `product-shell` (the tenant-facing shell) and the
platform-staff console `@speed/console-ui` both build their frame on
this exact package while `RouteGuard` is composed host-side. Every built-in string renders from the
bilingual `layout-kit` namespace registered through `@speed/i18n`, the
same discipline `ui-kit` established, and the workspace's
`no-literal-text` ESLint rule refuses inline text in this package's
`src`.

## What ships

| Module | Exports |
|---|---|
| `components/AppShell.tsx` | `AppShell`, `AppShellProps`, `AppShellShellLayout`, `AppShellNavItem`, `AppShellNavGroup`, `AppShellNavEntry`, `AppShellNavSlot`, `AppShellNavSlotState` |
| `components/Avatar.tsx` | `Avatar`, `AvatarProps` |
| `components/RouteGuard.tsx` | `RouteGuard`, `RouteGuardProps`, `RouteGuardStatus` |
| `isPlainPrimaryClick.ts` | `isPlainPrimaryClick` |
| `resources.ts` | `LAYOUT_KIT_NAMESPACE`, `layoutKitResources` |

Everything else (`src/internal/`) is shared component plumbing and is
deliberately not exported.

## Quick start

A host registers the `layout-kit` namespace once, alongside `ui-kit`'s
(reused by `RouteGuard`'s default denied fallback), inside the same
provider tree `ui-kit`'s own README documents:

```tsx
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  AppShell,
  LAYOUT_KIT_NAMESPACE,
  layoutKitResources,
  RouteGuard,
  type RouteGuardStatus,
} from '@speed/layout-kit'
import { useState } from 'react'

const i18n = createI18n()
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)

function AppContent() {
  // Stands in for the host's real authorization source: RouteGuard
  // never imports an auth package, so a host wires whatever it uses
  // (a hook backed by a real package, a stub like this one in a test)
  // and passes the resulting status value down.
  const [status] = useState<RouteGuardStatus>('allowed')
  return (
    <AppShell
      navItems={[{ id: 'home', label: 'Home', href: '/', selected: true }]}
      header="My App"
    >
      <RouteGuard status={status}>{/* protected screen content */}</RouteGuard>
    </AppShell>
  )
}

export function App() {
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppContent />
      </AppThemeProvider>
    </I18nextProvider>
  )
}
```

`registerNamespace` validates before it mutates (identical leaf key
sets across both languages) and must run exactly once per instance,
same as every other speed namespace. This exact composition is
compiled and executed by the package suite
(`src/usage-example.test.tsx`), so the documented usage cannot drift
from the API.

## AppShell

The responsive app-chrome shell: a nav `Drawer` (a labelled `nav`
landmark: permanent at the `md` breakpoint and up, temporary and
overlaid below it -- driven off `useMediaQuery(theme.breakpoints.up('md'))`,
no new breakpoint introduced) and a `main` content landmark, arranged
in one of two chrome forms selected by `shellLayout`: `'topbar'` (the
default) places a fixed `AppBar` (the `header` landmark) above the
content, while `'sidebar'` moves the app-bar content into the drawer
paper and renders no app bar at `md` and up. A visually-hidden
skip-to-content link is the first focusable element in the shell;
activating it focuses the `main` landmark directly (never a fragment
navigation, which would rewrite `location.hash` and lose a
hash-routed host's current route).

AppShell carries no navigation logic of its own: `navItems` is a
`readonly AppShellNavEntry[]` the host computes in full, including
which item is `selected` -- AppShell never does path-matching, since
different hosts use different routers.

An entry is either a plain `AppShellNavItem` or a titled, collapsible
`AppShellNavGroup`. A group's heading is itself the disclosure control
(`aria-expanded`), open by default unless `defaultCollapsed` is set, and
its `items` sit beneath it. A group may carry an optional `icon`; in the
collapsed rail it replaces the clipped heading, exactly as an item's icon
replaces its label. A plain item may carry `children`, which
turns it into an expandable parent whose child entries render indented,
each a real link or button; a parent with children is a disclosure, not a
navigation row (its own `href` / `onClick` / `selected` are not
rendered). Both levels reuse the leaf activation and the
`selected`/`aria-current` convention. This is purely additive: an
`AppShellNavItem[]` host that declares no groups and no children is
byte-for-byte unchanged.

### Shell layouts

`shellLayout` is the one structural choice the shell accepts, and it is
made at assembly time. It is an enum rather than a free slot because the
landmark structure, focus order, head measurement and the drawer's two
variants are this component's accessibility guarantees; content
variation is what the slots already cover.

At `md` and up:

| Region | `topbar` (default) | `sidebar` |
|---|---|---|
| Fixed head | `AppBar` carrying `header`, `headerActions` and `userMenu` | no `AppBar`; a `banner`, when given, is the head's only child at the very top |
| Drawer paper, top row | -- | `header` plus the desktop collapse toggle, when `onSidebarCollapsedChange` is supplied |
| Drawer paper, middle | `navHeader` -> `nav` (the paper scrolls) -> `navFooter` | the same slots in the same order, with `nav` taking the remaining height and scrolling itself |
| Drawer paper, bottom row | -- | `headerActions` then `userMenu`, pinned to the bottom |
| Content | `main` below the fixed head | `main` below the fixed head when one renders (a desktop shell with no banner starts at the viewport top); otherwise unchanged |

The sidebar paper is a fixed column: the paper itself stops scrolling
(`overflow: hidden`), the `nav` between the two rows takes
`flex: 1 1 auto` with `minHeight: 0` and scrolls vertically, and the
bottom row stays put (`flexShrink: 0`). Those three declarations are
added only to the `sidebar` layout -- giving the nav `flexGrow`
unconditionally would push `navFooter` to the bottom of an
always-scrolling paper in the `topbar` layout. The content region
remains the page's one scrolling layer in both layouts, and
`navHeader`/`navFooter` remain siblings of the `nav` landmark with the
same accessible name and slot contract; `narrow` still reflects the
mounted drawer.

Below `md`, a `sidebar` shell renders a minimal top bar holding only the
drawer trigger and `header`; `headerActions` and `userMenu` stay in the
temporary drawer's bottom row, and the temporary drawer renders no top
row -- the brand is already in the minimal bar, so it appears once per
page. That minimal bar is the `banner` landmark. A desktop `sidebar`
shell renders no app bar, so without a banner it has no `banner`
landmark at all; with a banner, the banner is the head. This landmark
difference between the layouts is deliberate.

The fixed head is rendered only when it has content:
`shellLayout === 'topbar'`, a `banner` is given, or the viewport is
below `md`. Otherwise the shell renders no head container and its three
offset placeholders (`spacer`) render `null`. Without that rule jsdom's
unmeasured fallback would leave the theme toolbar height (56/64px) as a
permanent gap above the content. When a head exists, one
`ResizeObserver` measures it and every placeholder clears that height;
switching layouts or crossing the breakpoint resets the measurement, so
a height measured for one arrangement never survives into another.

The collapsed rail's width is `max(theme.spacing(7), 44)`: at the
default spacing base the result is unchanged, and the 44px floor keeps
the rail at the touch-target minimum when a theme preset shrinks the
spacing base (the `compact` preset is the shipped case). The collapse
toggle sits in the AppBar in the `topbar` layout and at the end of the
drawer paper's top row in the `sidebar` layout. The mobile drawer
trigger sits in the AppBar (`topbar`) or the minimal top bar
(`sidebar`), and both positions reuse the existing
`appShell.openNav`/`closeNav` strings.

The bottom row is a wrapping horizontal flex container; in the
collapsed rail it stacks vertically and centers. The rail is only 280px
wide by default, so a host stacking wide controls (a tenant switcher,
say) arranges them in its own slot -- AppShell does not re-lay-out host
content.

### Adding a shell layout

The `AppShellShellLayout` union is the registry of built-in forms. A new
form is a platform change, not a host configuration: add the union
member, give it a region mapping in `AppShell`, cover it in both the
jsdom suite and the Chromium geometry suite, and update the design
document and this package's docs in the same change. Hosts then select
it through `shellLayout` alone, with no host-side change.

A customer that changes the content of the chrome -- brand, actions,
account menu -- but not its structure uses the existing slots and leaves
`shellLayout` alone. A host shipping its own replacement `AppShell` is
out of scope: it would duplicate the breakpoint split, landmarks, head
measurement and drawer variants this component owns.

### Props

| Prop | Type | Notes |
|---|---|---|
| `shellLayout` | `'topbar' \| 'sidebar'` | the shell's chrome arrangement, chosen at assembly time; defaults to `'topbar'`, the arrangement every host had before the prop existed. `'sidebar'` moves the brand and the desktop collapse toggle to the drawer paper's top row and `headerActions`/`userMenu` to its bottom row; at `md` and up no app bar renders (see "Shell layouts") |
| `navItems` | `readonly AppShellNavEntry[]` (required) | each element is an `AppShellNavItem` (`{ id, label, icon?, href?, onClick?, selected?, children? }`) or an `AppShellNavGroup` (`{ id, label, icon?, items, defaultCollapsed? }`); `href` renders a link, `onClick` fires regardless |
| `header` | `ReactNode` | start-of-AppBar content (logo, product name). The `sidebar` layout moves it to the drawer paper's top row, and to the minimal top bar below `md` |
| `headerActions` | `ReactNode` | end-of-AppBar content (search, notifications). The `sidebar` layout moves it to the drawer paper's bottom row |
| `userMenu` | `ReactNode` | far end-of-AppBar content (account menu trigger). The `sidebar` layout moves it after `headerActions` in the bottom row |
| `banner` | `ReactNode` | a full-width banner below the app bar, host content and already translated. It is part of the fixed shell head: one fixed container holds the app bar first (where one renders) and the banner after it in normal flow, so the two never overlap; the content spacers reserve the head's measured height (each height counted once). A desktop `sidebar` shell renders no app bar, so there the banner is the head's only child, at the very top. The banner stays in view while the page scrolls, at every breakpoint, and the head sits above page content but below modal panels and dialogs. The slot adds no padding, border or minimum height -- an empty node adds no height. It belongs to the shell, so a route change never unmounts it. It adds no landmark and no live region -- the node the host passes carries the roles it needs, and the banner landmark remains the app bar (the minimal top bar below `md` in a sidebar shell; a desktop sidebar shell has no banner landmark). Absent, `null` or `false` renders no slot container and adds no height |
| `navLabel` | `string` | the navigation landmark's accessible name (and the temporary drawer's dialog name); defaults to the `appShell.navLabel` string. A host mounting AppShell beside a second shell's navigation supplies a distinguishing name so the landmarks do not read alike |
| `navHeader` | `ReactNode \| ((state: AppShellNavSlotState) => ReactNode)` | host content inside the drawer paper, immediately before the nav landmark, in both drawer variants. A render function receives `{ narrow, closeDrawer }`: `narrow` is true only in the collapsed rail (never in the temporary drawer), and `closeDrawer` closes an uncontrolled temporary drawer, reports `false` through `onMobileOpenChange` when the drawer is controlled, and is a no-op for the permanent drawer. The slot is a sibling of the landmark, so the landmark and its name are unchanged. Absent, `null` or `false` renders nothing and adds no height |
| `navFooter` | `ReactNode \| ((state: AppShellNavSlotState) => ReactNode)` | host content inside the drawer paper, immediately after the nav landmark, with the same state and rendering contract as `navHeader` |
| `children` | `ReactNode` (required) | the content region, rendered inside the `main` landmark |
| `mobileOpen` / `onMobileOpenChange` | `boolean` / `(open: boolean) => void` | optional controlled pair for the mobile drawer; omit both to let AppShell manage the toggle itself (the one interaction-local exception, matching `ui-kit`'s `ConfirmDialog` double-confirm arm -- the same carve-out covers the uncontrolled temporary drawer closing itself when a nav item is activated; a controlled host keeps the existing contract, closing through its own next render) |
| `sidebarCollapsed` / `onSidebarCollapsedChange` | `boolean` / `(collapsed: boolean) => void` | optional controlled pair for the desktop sidebar; omit both for the always-open permanent drawer (the drawer is what it was before the collapse existed; the one change that applies to every host is the touch-device floor on each nav entry, which follows the accessibility requirement rather than the collapse). Supplying `onSidebarCollapsedChange` renders the collapse/expand toggle -- in the AppBar in the `topbar` layout, at the end of the drawer paper's top row in the `sidebar` layout. `sidebarCollapsed` (default `false`) is honored only at `md` and up -- the mobile drawer is unaffected -- and collapsing narrows the permanent drawer to the icon-only rail of `max(theme.spacing(7), 44)` px: it stays mounted and named, each entry keeps its icon with the label shown as a tooltip, and an icon-less entry keeps its (clipped) label rather than going blank |
| `sidebarWidth` | `number` | drawer width in px, both variants; defaults to 280 |
| `sx` | `SxProps<Theme>` | escape hatch, merged onto the root layout box |

Slots render exactly the content passed in -- an omitted slot renders
nothing, never a placeholder. The desktop sidebar collapse is an
explicit, host-controlled pair, not an implicit responsive behavior: a
host that omits it keeps the always-open permanent drawer, and the toggle
appears only when the host supplies the change callback.

## Plain-primary-click judgement

`isPlainPrimaryClick(event)` is the shared judgement in-app navigation
links use to decide whether to intercept a click and route in place. It
returns true only for the primary button (or an event carrying no button
value), with no ctrl/meta/shift/alt modifier, on an activation whose
nearest anchor has neither `target="_blank"` (matched case-insensitively)
nor a `download` attribute, and on an event that is not already
default-prevented. The anchor is the clicked element's nearest `a`
ancestor, so a click on a child of the link is judged the same as one on
the link itself.

```ts
onClick={(event) => {
  if (isPlainPrimaryClick(event)) {
    event.preventDefault()
    router.navigate(href)
  }
}}
```

`@speed/console-ui` consumes this function. `@speed/route-kit` keeps a
private variant by design -- it is independent of every sibling package and
must not import this one -- and that variant is deliberately stricter:
`button` must be exactly `0` (an absent value is rejected rather than read
as the primary button), and every `target` other than absent, empty or
`_self` is rejected, so `_top`, `_parent` and named windows are left to the
browser. This function rejects only `target="_blank"` and reads an absent
`button` as the primary button. A change to either set of criteria belongs
in both copies in the same change.

## Avatar

The identity chip: an image when `src` is given (and loads), else up to two
uppercase initials from `name` (first and last word). A named,
non-decorative avatar reads as its name (`role="img"` with `aria-label`),
so a name-only avatar announces the name, not its bare initials; a
`decorative` avatar leaves the accessibility tree entirely, for when a
wrapping control (an `aria-label`led button next to the account name)
already carries the identity.

| Prop | Type | Notes |
|---|---|---|
| `name` | `string` | display name; supplies the initials and (unless decorative) the accessible name |
| `src` | `string` | image URL; the initials render when it is absent or fails to load |
| `size` | `number` | side length in px; defaults to 40 |
| `decorative` | `boolean` | leave the glyph out of the accessibility tree; defaults to false |
| `sx` | `SxProps<Theme>` | escape hatch, merged onto the avatar |

**Narrow-viewport protections (CSS-only, no prop changes)**: the
mobile (temporary) Drawer's paper
width is capped to a CSS `min(sidebarWidth, 85vw)` -- a very narrow
viewport (~320px and below) never gets a near-full-screen or
overflowing drawer -- while the desktop (permanent) Drawer's paper
width is untouched, still the plain `sidebarWidth`. The AppBar's
Toolbar row and the `headerActions` group both carry a responsive
`flexWrap: 'wrap'`, so a host supplying several header actions wraps
onto a second line under real overflow pressure instead of being
squeezed or clipped -- AppShell still renders only what the host
gives it (this is not an auto-collapsing overflow menu; inventing one
would be new host-facing behavior this package's design deliberately
avoids elsewhere). No prop was added or changed, and every existing
consumer is visually unaffected outside the narrow-viewport cases
these protections exist for. Because a wrapped header row is taller
than the theme's single-row toolbar height, the fixed head container's
rendered height is measured (one ResizeObserver on the container that holds
the app bar or minimal top bar, where one renders, and the banner below it,
where the browser provides one) and the three
offset placeholders that keep content clear of that fixed head -- inside the
drawer paper and inside `main`, for whichever drawer variant is mounted --
derive their height from that measurement, so a wrapped header never covers
the top of the content below it. The measurement is reset when the layout or
the desktop/mobile split changes, so a height measured for one arrangement
never survives into another. jsdom has no ResizeObserver and no layout,
so the placeholders keep the theme toolbar height there and the
measurement path is proven through a stubbed observer; the real
browser round trip is what the mechanism exists for.

## RouteGuard

Gates `children` on a host-computed `status`, never a callback
RouteGuard invokes:

```ts
export type RouteGuardStatus = 'allowed' | 'denied' | 'pending'

export interface RouteGuardProps {
  readonly status: RouteGuardStatus
  readonly children?: ReactNode
  readonly pendingFallback?: ReactNode
  readonly deniedFallback?: ReactNode
  // ui-kit's own h1..h6 union; default 'h1'. Reaches only the default
  // denied composition (see below).
  readonly headingLevel?: EmptyStateProps['headingLevel']
  readonly onDenied?: () => void
}
```

| `status` | Renders |
|---|---|
| `'allowed'` | `children` |
| `'pending'` | `pendingFallback`, or a default centered, labelled MUI `CircularProgress` |
| `'denied'` | `deniedFallback`, or `@speed/ui-kit`'s own `EmptyState variant="noPermission"` composed at `headingLevel` -- the package's one concrete `ui-kit` coupling, never anything auth- or routing-shaped |

The default denied composition's title is the page's own heading: the
gate replaces the page content it guards, so the fallback renders as
an `h1` by default, and `headingLevel` (forwarded to ui-kit's own
`EmptyState.headingLevel` contract) lets a host whose page heading
precedes the gate pass the level that continues the page's order
without a skip -- the usage example's page shape does exactly that,
rendering its own `h1` and composing the gate below it at
`headingLevel="h2"`. A host replacing the whole fallback through
`deniedFallback` composes its own heading structure and needs none of
this; `headingLevel` is consulted only when no `deniedFallback` is
supplied.

`onDenied` fires exactly once per transition *into* `'denied'` (a
`useEffect` keyed on `status`, guarded so a re-render that leaves
`status` at `'denied'` does not re-fire) -- the seam for a host's
router redirect or telemetry call, fully decoupled from render.

The three states are mutually exclusive by construction: there is no
separate boolean "loading" flag that could disagree with "allowed".

## Text and i18n

| Key | Purpose |
|---|---|
| `appShell.skipToContent` | the visually-hidden skip-to-content link |
| `appShell.navLabel` | the nav drawer landmark's accessible name |
| `appShell.openNav` / `appShell.closeNav` | the mobile nav toggle button's `aria-label`, by state |
| `appShell.collapseNav` / `appShell.expandNav` | the desktop sidebar collapse/expand toggle button's `aria-label`, by state |
| `routeGuard.pending` | the default pending spinner's `aria-label` |

`routeGuard`'s denied fallback carries no text of its own -- it is
entirely `ui-kit`'s `emptyState.noPermission.*` bundle, by design (see
RouteGuard above). The two files (`src/locales/zh-CN.json`,
`en-US.json`) carry identical leaf key sets; parity is enforced when the
namespace is registered (`registerNamespace`'s leaf-parity and
plural-coverage validation, `web/packages/i18n/src/register.ts`). The
package has no `resources.test.ts`, so nothing here asserts the pair at
file level; a package-level test over the two catalogs would be the
natural home if one is wanted. Hosts reword the kit by
registering their own identical-key bundle pair under
`LAYOUT_KIT_NAMESPACE` at bootstrap -- never by editing component
text.

## Dependencies

| Package | Kind | Why |
|---|---|---|
| `react`, `react-dom` | peer (required, ^18 or ^19) | the host owns the React tree |
| `@mui/material` | peer (required, ^9.5) | `AppBar`/`Drawer`/`CircularProgress` and the ambient theme (breakpoints, z-index) AppShell reads through `useTheme()` |
| `@emotion/react`, `@emotion/styled` | peer (required, ^11) | MUI's own runtime requirements |
| `@speed/i18n` | dependency | the namespace registration and translation hook every component renders through |
| `@speed/ui-kit` | dependency | `RouteGuard`'s default denied fallback (`EmptyState variant="noPermission"`) -- the package's only concrete coupling, sanctioned for chrome/primitives |

No direct dependency on `@speed/tokens`: `AppShell` reads
`breakpoints.values` and `zIndex.values` through the ambient MUI theme
a host's `AppThemeProvider` already builds, exactly as `ui-kit`'s own
components do -- both are already MUI-identical defaults, so nothing
layout-relevant is missing (`tokens/src/breakpoints.ts`,
`z-index.ts`). No dependency on `auth-core`, `api-client`, `api-sdk`,
or any concrete authentication or routing package, anywhere in this
package -- see the AGENTS.md non-negotiable rules.

## Deferrals and recorded decisions

- **`auth-core` wiring**: `RouteGuard`'s `status` is fed by a local
  `useState` stub in the Quick start above -- the host computes the
  status and passes it in, and no auth package is imported here (see
  the AGENTS.md non-negotiable rules). The prop shape needs no change
  for any authorization source: a host only computes a
  `RouteGuardStatus` and passes it in.
- **`admin-shell`**: the platform-staff console ships as
  `@speed/console-ui`, which builds its frame on `AppShell` (see
  AGENTS.md); this package ships only the
  shared chrome the shells assemble.
  `product-shell`, the tenant-facing consumer, composes `AppShell`
  as its authenticated frame, and its gated-journey suite proves the
  `RouteGuard` composition shape -- a stand-in host mounting the gate
  inside the shell's children and feeding it a status it computes
  from the role lists it attached, exactly the host-injected-status
  contract this package's rules require.
- **Browser-driving leg against a server**: no host ships in the tree.
  The layout's real geometry, focus order and focusability are measured
  in Chromium by `src/components/AppShell.browser.test.tsx` (the
  workspace's `make web-browser` tier), and the package-level
  `src/usage-example.test.tsx` is the in-form leg; nothing drives the
  composed frame against a real backend.
- **Storybook / browser-side visual verification**: no preview
  harness exists, the same deferral `ui-kit` carries; `color-contrast`
  stays axe-disabled for the same jsdom reason.
- **Skip-to-content beyond a single visible-on-focus link**: no
  broader "landmark navigation menu" affordance is in scope -- a
  `product-shell`-level concern once real pages exist.

## Development

From `web/packages/layout-kit`: `pnpm lint`, `pnpm typecheck`, `pnpm
test`, `pnpm build`. The test suite runs in
jsdom (`vitest.config.ts`); shared helpers live in `test-utils/`
(`renderWithProviders` mounts the unit under the real host tree --
`I18nextProvider` plus `ui-kit`'s own `AppThemeProvider`, both
namespaces registered -- `expectNoAxeViolations` runs axe;
`mockMatchMedia` stubs jsdom's missing `window.matchMedia` for
`AppShell`'s desktop/mobile split, its returned handle's
`changeMatches` flipping a mounted shell across the breakpoint --
`resize-observer.ts`'s `stubResizeObserver` stubs the header-height
measurement -- and `@speed/test-utils/emitted-css`'s
`emittedStyleText()` reads the CSS text emotion has injected into the
document, used by the mobile-drawer-width-cap test: jsdom evaluates no
real layout, so that assertion is a property/snapshot proof the CSS
`min()` cap was wired into the render, not a proof it looks correct at
a real narrow viewport). Bilingual fixtures are the shipped
locale files under `src/locales/`, imported by sources and tests;
`src/usage-example.test.tsx` compiles and executes the Quick start
composition above, so the documented usage cannot drift from the API.
The Chromium tier lives beside the component
(`src/components/AppShell.browser.test.tsx`, run by `make web-browser`):
it measures the fixed head, the offset placeholders, the pinned sidebar
bottom row and the collapsed rail in both layouts -- the geometry and
focus invariants jsdom cannot evaluate. A new shell layout adds its
cases there, and in the jsdom suite, in the same change.
