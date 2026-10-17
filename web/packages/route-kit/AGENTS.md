# AGENTS.md — @speed/route-kit

## What this package is

A product's routing and navigation, as one declarative registration surface.
A product declares its pages once — a `RouteTable` of `RouteDeclaration`s
(path pattern, view, optional navigation label, optional permission code,
optional required feature names) — and `RouteProvider` installs it over
react-router-dom; `RouteView` renders the matched page (or the provider's
`fallback`, `pending` or `failed` view), `useNavItems` derives the navigation
entries from the `label`-bearing declarations with the selected one marked,
`useMatch` and `useHasRoute` answer exact-match questions, and `RouteLink` /
`useNavigate` move the URL. The table is the single source of truth, so the
page rendered and the entry highlighted cannot disagree.

A declaration's `requires` names the backend features the page depends on.
`RouteProvider` takes the host's feature-presence answer (`presence`) plus the
views a gated path renders while that answer is pending or failed; the package
narrows the registration surface with `presentDeclarations`, and a gated page
whose features are absent renders the same `fallback` as a path that does not
exist. Rendering, navigation, exact matching and the entry-control answer all
read that one narrowed set.

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
  declaration's `label`, a view's output, the fallback's, pending's and
  failed's output. There is no i18n namespace and no locale file, because
  there is nothing to translate. Do not add a built-in string; if one seems
  needed, it belongs to the host.
- **It never reads `permission`.** The field is carried as data only. This
  package does not gate a route, filter a nav item, or derive anything from a
  permission code — route-level authorization is the host composing
  `@speed/layout-kit`'s `RouteGuard` in the content region. A change that
  inspects `permission` here is an architecture change to push back on.
- **Feature presence is host input, not something this package knows.** The
  package never fetches a feature set and never interprets a feature name: it
  hands each name to the host's `presence.has` and uses the boolean. `pending`
  is not absence and `failed` is not absence; only a ready answer that says a
  required feature is missing makes a page absent.
- **Matching runs over the whole table; navigation, `useMatch` and
  `useHasRoute` run over the narrowed surface.** That split is what lets a
  gated path render `pending`/`failed` before the predicate is ready, while an
  unregistered path falls to `fallback` in every state. Absence and a miss
  must render the same `fallback` — never give an absent page its own view or
  a different 404.
- **The narrowing rule lives in one function.** `useNavItems`, `useMatch` and
  `useHasRoute` derive from `presentDeclarations(table, presence)`; do not
  filter in a second place, or the page listed and the page matched can drift.
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
  pattern, a malformed declaration, a missing fallback, or a gated table
  missing `presence`/`pending`/`failed` throws where the product is composed,
  not later as a page that silently never renders. Keep the check in the
  render body (not behind a memo that React may skip).
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
a host assembles, not a hand-rolled re-wrap — optionally with a presence
predicate and pending/failed views. The provider mounts a `BrowserRouter`, so
a suite moves the URL through `setLocation` or through a real link click;
there is no `MemoryRouter` shortcut, because that would test a router the
product never runs.

Assertions pin a decision, not a mount: which declaration matches and with
what params, the fallback receiving the unmatched path, the selected nav
entry (exact match, "sits below", longest prefix), `permission` being inert,
and a click or imperative `navigate` re-rendering the view from the new URL.
The gating suites assert the decisions too: an ungated table renders
identically under every presence value; a ready answer with a feature absent
reaches `fallback` without calling the view; an absent parameterized page and
a miss at the same URL render byte-identically, and each hands `fallback` the
requested path with empty params; pending and failed reach their own views and
never `fallback` (an unregistered path still reaches it);
navigation, `useMatch` and `useHasRoute` agree on the same narrowed set;
`useMatch` has no prefix inheritance. The consistency check is asserted both
directly (`table.test.ts`) and through the provider
(`RouteProvider.test.tsx`), where the throw is the assertion.
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
