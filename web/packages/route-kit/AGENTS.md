# AGENTS.md — @speed/route-kit

## What this package is

A product's routing and navigation, as one declarative registration surface.
A product declares its pages once — a `RouteTable` of `RouteDeclaration`s
(path pattern, view, optional navigation label, optional permission code) —
and `RouteProvider` installs it over react-router-dom; `RouteView` renders
the matched page (or the provider's `fallback`), `useNavItems` derives the
navigation entries from the `label`-bearing declarations with the selected
one marked, and `RouteLink` / `useNavigate` move the URL. The table is the
single source of truth, so the page rendered and the entry highlighted cannot
disagree.

It is the frontend's counterpart to the backend's command tree: adding a page
is a declaration, the one place a product names what it is made of. It is
also deliberately the **opposite** of the shell: `product-shell` does no
navigation, and this package is where a product's navigation lives. The
public surface is `src/index.ts`; everything else in `src/` is internal and
not exported.

## Non-negotiable rules

- **No sibling `@speed` dependency.** This package's `package.json` may
  declare `react`, `react-dom` and `react-router-dom` — peers — and nothing
  else. It carries a structural `NavItem` shape precisely so a shell's frame
  can accept it by structure, with no shared type edge (`product-shell`
  imports neither this package nor the reverse). Adding an `@speed/ui-kit` or
  `@speed/auth-core` import here, however small, defeats the reason the
  routing layer was split out of the shell.
- **It renders no text of its own.** Every visible string is host content: a
  declaration's `label`, a view's output, the fallback's output. There is no
  i18n namespace and no locale file, because there is nothing to translate.
  Do not add a built-in string; if one seems needed, it belongs to the host.
- **It never reads `permission`.** The field is carried as data only. This
  package does not gate a route, filter a nav item, or derive anything from a
  permission code — route-level authorization is the host composing
  `@speed/layout-kit`'s `RouteGuard` in the content region. A change that
  inspects `permission` here is an architecture change to push back on.
- **It makes zero HTTP calls and holds no session or data state.** If a
  change wants to fetch anything, scope has drifted. The workspace's
  `speed/no-direct-http` ESLint rule enforces this (this package is absent
  from its one whitelist, `packages/api-client`).
- **The address model is the package's, and it is committed.** The provider
  mounts react-router-dom's `BrowserRouter` — real path URLs, deep links
  served by the platform's SPA fallback (see the package README's "Address
  model"). Do not add a host-facing prop to swap routers: the seam record
  (`docs/adr/adr-web-route-seam-2026-10-15.md`) makes the third-party router
  a package-level decision precisely so products do not each pick one.
- **It rides react-router-dom, it is not a second router.** Path matching
  uses react-router-dom's `matchPath`; links and history use its `Link` and
  `useNavigate`. The package adds the *registration surface* — which pages
  exist, which is selected, how they are collected — never a parallel
  matching engine or history stack.
- **The consistency check runs at assembly.** `RouteProvider` calls
  `assertRouteTable` before rendering children, so a duplicate/ambiguous
  pattern, a malformed declaration or a missing fallback throws where the
  product is composed, not later as a page that silently never renders. Keep
  the check in the render body (not behind a memo that React may skip).
- **Framework peers stay peers.** `react`, `react-dom` and `react-router-dom`
  are peer (required) dependencies — one copy in the host tree is what makes
  router context identity work; this package never depends on them directly.
  There are no regular dependencies.
- **The public API is frozen by convention.** Lockstep versioning makes an
  exported-signature change a breaking release; extend the surface only
  intentionally. A public change ships in one commit: the code, its tests,
  this AGENTS.md and the README (contract prose included).

## Testing

Unit tests are vitest + jsdom (every suite: the whole surface is render-time,
and a router reads `window.location`), one file per source file under `src/`.
The shared harness is `test-utils/render.tsx`: `setLocation(path)` pins
jsdom's address before a mount, and `renderRouteTable(table, ui, options)`
mounts the unit inside a real `RouteProvider` at that path — the composition
a host assembles, not a hand-rolled re-wrap. The provider mounts a
`BrowserRouter`, so a suite moves the URL through `setLocation` or through a
real link click; there is no `MemoryRouter` shortcut, because that would test
a router the product never runs.

Assertions pin a decision, not a mount: which declaration matches and with
what params, the fallback receiving the unmatched path, the selected nav
entry (exact match, "sits below", longest prefix), `permission` being inert,
and a click or imperative `navigate` re-rendering the view from the new URL.
The consistency check is asserted both directly (`table.test.ts`) and through
the provider (`RouteProvider.test.tsx`), where the throw is the assertion.
`src/usage-example.test.tsx` compiles and executes the README's Quick start,
so the documented composition cannot drift from the API; when that
composition changes, this file changes with it.

## Consumers

`starters/demo/web` is this package's in-tree consumer: it composes the
business slot and the console area's declarations into one `RouteTable` and
installs it with `RouteProvider`. `console-ui` -- the second shell -- mirrors
the declaration, context and table shapes structurally and imports nothing
from here, so the two packages share the registration surface without a type
edge between them.
