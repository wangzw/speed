# @speed/route-kit

A product's **routing and navigation** in one package. Every page is declared
in one place — a `RouteTable` of `RouteDeclaration`s (path, view, optional
navigation label, optional permission code, optional required features) — and
one provider turns that declaration into rendering, deep-link matching,
navigation selection and link construction. It is the frontend's counterpart
to the backend's command tree: where a program names its modules in one place,
a product names its pages in one place.

Pages that depend on a backend feature can say so with `requires`. The host
hands `RouteProvider` its feature-presence answer; the package narrows the
registration surface accordingly, so an absent page is indistinguishable from a
path that does not exist — one `fallback`, one 404 — while the predicate is
still loading or has failed, such a page renders the host's pending or failed
view instead.

The package is **independent**: it imports no sibling frontend package, and
the shell (`@speed/product-shell`) neither imports it nor knows about it. A
product that adds a page adds a declaration to its table; it does not touch
the shell.

## Quick start

```tsx
import type { ReactNode } from 'react'
import {
  RouteProvider,
  RouteView,
  RouteLink,
  useNavItems,
  useNavigate,
  type RouteContext,
  type RouteTable,
} from '@speed/route-kit'

// The product names its pages in one place.
const routes: RouteTable = [
  { path: '/', view: () => <Home />, label: 'Home' },
  { path: '/cases', view: () => <CaseList />, label: 'Cases' },
  {
    path: '/cases/:caseId',
    view: ({ params }) => <CaseDetail id={params.caseId ?? ''} />,
  },
]

function App(): ReactNode {
  return (
    <RouteProvider
      table={routes}
      fallback={({ path }: RouteContext) => <NotFound path={path} />}
    >
      <TopNav />
      <RouteView />
    </RouteProvider>
  )
}

function TopNav(): ReactNode {
  const items = useNavItems()
  return (
    <nav>
      {items.map((item) => (
        <RouteLink key={item.id} to={item.id}>
          {item.label}
        </RouteLink>
      ))}
    </nav>
  )
}

function Home(): ReactNode {
  return <h1>Home</h1>
}

function CaseList(): ReactNode {
  return <h1>Cases</h1>
}

function CaseDetail({ id }: { readonly id: string }): ReactNode {
  const navigate = useNavigate()
  return (
    <section>
      <h1>{`Case ${id}`}</h1>
      <button onClick={() => navigate('/cases')}>Back</button>
    </section>
  )
}

function NotFound({ path }: { readonly path: string }): ReactNode {
  return <p>{`No page at ${path}`}</p>
}
```

A product's assembly wires the shell around this: `RouteProvider` wraps the
shell's whole composition, `useNavItems()` feeds the shell's `navItems` slot,
and `RouteView` renders as the shell's content. The shell keeps rendering the
entries the host hands it; the routing lives in the layer around it.

## The registration surface

```ts
type RouteParams = Readonly<Record<string, string>>

interface RouteContext {
  readonly path: string   // the matched path, without the query string
  readonly params: RouteParams
}

interface RouteDeclaration {
  readonly path: string                              // e.g. '/cases/:caseId'
  readonly view: (context: RouteContext) => ReactNode
  readonly label?: ReactNode                          // absent => no nav item
  readonly permission?: string                        // data; this package never reads it
  readonly requires?: readonly string[]               // feature names; data this package hands to the predicate
}

type RouteTable = readonly RouteDeclaration[]

type RoutePresence =
  | { readonly state: 'pending' }
  | { readonly state: 'failed' }
  | { readonly state: 'ready'; readonly has: (name: string) => boolean }

interface NavItem {
  readonly id: string      // the declaring path
  readonly label: ReactNode
  readonly href: string     // built from the declaring path
  readonly selected: boolean
  // A plain primary click navigates in-app; every other click is left to
  // the browser (new tab, download, default navigation).
  readonly onClick?: MouseEventHandler<HTMLElement>
}
```

- **`path` is a pattern.** Literal segments match by equality; a `:name`
  segment matches any one segment and is captured into `params`.
- **`label` decides navigation.** A declaration with a label contributes a
  nav item; one without (a detail page, a callback) still matches and renders
  but stays out of navigation. Hiding a page from the nav means giving its
  declaration no label — never filtering by permission here.
- **`permission` is data.** The package carries it and does nothing with it.
  Route-level authorization is the host composing `@speed/layout-kit`'s
  `RouteGuard` in the content region, fed a status derived from
  `@speed/auth-core`'s permission hook. A page whose path is mis-wired to its
  permission is not caught at the routing layer.
- **`requires` is data.** It lists the feature names a page needs, and the
  package does not interpret them: it hands each name to the presence
  predicate's `has`. A declaration is *gated* when the list is non-empty; a
  declaration without the field (or with an empty list) is never affected by
  the predicate.
- **`onClick` is filled in by the package.** `useNavItems` attaches it beside
  `href`, so a frame that accepts both gets an in-app navigation for a plain
  primary click and the browser's own behaviour (a new tab, a saved download,
  a modified click) for every other one. The handler navigates by the entry's
  `id`, the declaration's basename-relative path, because the router applies
  the basename itself. Its type matches the frame's own optional click field,
  which is why the entry still passes through without a shared type edge.

## Feature-gated pages

```tsx
<RouteProvider
  table={routes}
  fallback={(context) => <NotFound path={context.path} />}
  presence={presence}   // { state: 'pending' } | { state: 'failed' } | { state: 'ready', has }
  pending={() => <Loading />}
  failed={() => <LoadFailed onRetry={retry} />}
>
```

- **A gated table requires all three inputs.** If any declaration carries
  `requires`, `RouteProvider` throws at assembly unless `presence`, `pending`
  and `failed` are all given. An ungated table needs none of them, and its
  behavior is unchanged by their presence.
- **Absence is the same branch as a miss.** Matching runs over the whole table.
  An unmatched path renders `fallback`; a gated declaration whose features are
  absent renders the same `fallback` with the same context a miss gets — the
  requested path and no params — so the fallback cannot tell the two apart, and
  the declaration's `view` is never called. `<RouteView>` therefore cannot
  render a page that is not present.
- **Pending and failed are distinct states.** A gated path renders `pending`
  while the predicate is loading (a miss still renders `fallback`
  immediately), and `failed` once it has failed. Neither is treated as
  "absent".
- **Navigation follows the narrowed surface.** `useNavItems`, `useMatch` and
  `useHasRoute` all derive from `presentDeclarations(table, presence)`: an
  absent page is not in the navigation, is not returned by `useMatch`, and
  makes `useHasRoute` answer false — so an entry control pointing at it
  disappears together with the page. While the predicate is pending or
  failed, every gated declaration is withheld from that surface.

```ts
function presentDeclarations<T extends { readonly requires?: readonly string[] }>(
  table: readonly T[],
  presence: RoutePresence | undefined,
): readonly T[]
```

`presentDeclarations` is exported so a host can narrow its own declaration type
with the same rule — for example, a console's page declarations handed to a
second shell — keeping the console's sidebar and this package's navigation on
one set.

## Components and hooks

| Export | Purpose |
|---|---|
| `RouteProvider` | Installs the router and hands the table down. Props: `table`, `fallback` (required), `presence`, `pending`, `failed`, `basename`, `children`. |
| `RouteView` | Renders the declaration the current URL matches, the `fallback`, or a gated path's pending/failed view. |
| `useNavItems` | The narrowed surface's `label`-bearing entries, with the selected one marked and a click handler attached; each entry's `href` includes the router's `basename`. |
| `useMatch` | The declaration the current URL matches exactly on the narrowed surface, plus its context, or `undefined`. |
| `useHasRoute` | Whether a given path matches such a declaration exactly. |
| `presentDeclarations` | The narrowed surface: the declarations a presence predicate leaves in force. |
| `RouteLink` | A clickable link to a target path; navigates without a full reload and forwards every other prop to the router's `Link`. |
| `useRouteLinkProps` | `{ href, onClick }` for a link to one path, for hosts that render their own anchor or hand the pair to a MUI `component`. `href` includes the router's `basename`. |
| `useNavigate` | `(to: string) => void`, pushing a path onto history from an event handler. |
| `useRedirect` | `(to: string) => void`, replacing the current history entry (a callback page that must not stay in the back stack). |
| `useLocationPath` | `{ path, search }`: the raw path (matched or not) and the query string, without importing react-router-dom. |

**Selection.** An entry is selected when the current path matches its pattern
in full, or sits below its declared literal path — so a parent (`/cases`) is
selected while its child route (`/cases/:caseId`) is open. When several
entries qualify, the longest literal prefix wins; an exact match outranks a
prefix parent. Unmatched URLs select nothing.

**Exact matching has no prefix inheritance.** `useMatch` and `useHasRoute`
match a path against a declaration in full; a path below a nav entry that has
no declaration of its own does not inherit it. This is what lets a host take
the matched declaration's `permission` code for its guard without inheriting a
parent's.

## Extending an entry with an icon or a group

A navigation entry carries what every shell frame needs to render a row —
`id`, `label`, `href`, `selected` and `onClick` — and nothing about how one
product's frame decorates it. An icon, a section heading or an access rule is
that product's own vocabulary, so it belongs to the host's declaration type,
not to this package: a field added here would put one product's presentation
into every product's dependency, and the package knows no frame, no component
library and no product taxonomy.

Extend the declaration type where the host declares its pages:

```tsx
import type { ReactNode } from 'react'
import type { RouteDeclaration } from '@speed/route-kit'

export interface ProductRoute extends RouteDeclaration {
  readonly icon?: ReactNode
  readonly group?: string
}

export const routes: readonly ProductRoute[] = [
  { path: '/', view: Home, label: 'Home', icon: <HomeIcon />, group: 'main' },
  { path: '/cases', view: CaseList, label: 'Cases', icon: <CaseIcon />, group: 'main' },
]
```

The table is still a `RouteTable` — the added fields are optional — so
`RouteProvider` accepts it unchanged. Look the metadata up by `NavItem.id`,
which is the declaration's path and is stable across renders:

```tsx
function TopNav(): ReactNode {
  const items = useNavItems()
  return (
    <nav>
      {items.map((item) => {
        const declaration = routes.find((route) => route.path === item.id)
        return (
          <a
            key={item.id}
            href={item.href}
            onClick={item.onClick}
            aria-current={item.selected ? 'page' : undefined}
          >
            {declaration?.icon}
            {item.label}
          </a>
        )
      })}
    </nav>
  )
}
```

The same lookup buckets entries by `declaration.group`. `href` and `onClick`
together are what make the row behave like a real link in any frame: a plain
click navigates in-app, and a new-tab or modified click is left to the
browser. `item.href` already includes the deployment `basename`, so the
browser's own navigation (a new tab, a copied link) reaches the served URL;
`item.id` is the declaration's path and is what `RouteLink`'s `to` takes. For
an anchor the host renders itself, `useRouteLinkProps` returns the same pair
without a nav item.

## Address model

The provider mounts react-router-dom's `BrowserRouter`: pages get real path
URLs, and a deep link requires the server to fall back unknown paths to the
entry document (the platform's static-hosting/SPA-fallback module provides
that). The model is the package's, not a per-product option — see the
`ADR-web-route-seam` decision record.

A deployment served under a path prefix passes it as `basename`:

```tsx
<RouteProvider table={routes} fallback={...} basename="/console">
```

`useHref`, `useRouteLinkProps` and a nav item's `href` then yield
`/console/cases` for a `/cases` declaration, while `useLocationPath().path`,
`RouteView`, `useMatch` and a nav item's `id` keep reading the
basename-relative path (`/cases`). Absent `basename`, the application is
served at the origin root, which is the default.

## Dependencies

| Dependency | Kind | Use |
|---|---|---|
| `react`, `react-dom` | peer (required) | the declarations, rendering and hooks |
| `react-router-dom` | peer (required) | the base for path matching, history navigation and link construction; products do not import it directly |

The package imports **no** `@speed` sibling package, and no shell imports it.

## Stability

`RouteDeclaration` (including `requires`), `RouteTable`, `RouteContext`,
`RouteParams`, `RoutePresence`, the `RouteProvider` props (including
`basename`), the
`RouteView`/`RouteLink`/`RouteLinkProps`/`useRouteLinkProps`/`RouteLinkTarget`/
`useNavItems`/`useNavigate`/`useRedirect`/`useLocationPath`/
`RouteLocationPath`/`presentDeclarations`/`useMatch`/`useHasRoute`
signatures, `RouteMatch` and `NavItem` are the host-visible surface. The
packages release as one locked version group (`.changeset/config.json`).
Adding an optional prop or field is source-compatible; removing or narrowing
an existing export, field or signature, or changing its meaning, is a
breaking change, labelled with a `!` scope and a `BREAKING CHANGE` footer.

## Failure semantics

| Situation | Behavior |
|---|---|
| No declaration matches the URL | `fallback` renders; nothing throws |
| A gated declaration's features are absent | the same `fallback` renders, with the requested path and no params, exactly as for a miss; the declaration's `view` is never called and it has no nav entry |
| A gated path while the predicate is pending | `pending` renders; `fallback` is not called and gated nav entries are withheld |
| A gated path while the predicate has failed | `failed` renders; ungated pages and nav entries are unaffected |
| A duplicate or ambiguous path pattern | assembly throws (`RouteProvider`), naming the conflicting declarations, before rendering |
| `fallback` missing | assembly throws (it is required) |
| A gated table missing `presence`, `pending` or `failed` | assembly throws, naming the missing input and a gated path |
| A deep link no declaration covers | same as no match — reaches `fallback` |
| A page renders a resource it lacks permission for | not caught at the routing layer; authorization is the host's content-region composition |

The package never degrades, retries or makes a request: a match is a
render-time value, and a failure is either a table configuration error
(thrown at assembly) or a miss (the fallback).
