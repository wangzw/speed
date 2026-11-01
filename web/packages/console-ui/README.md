# @speed/console-ui

The platform-staff console of a frontend built on speed: the second
shell. Where `@speed/product-shell` is the tenant-facing front door,
this is the console platform operators use -- and the difference is the
point. The administration surfaces it renders are mounted on endpoints
that do not resolve a tenant, so the console has **no current-tenant
switcher and no current-tenant query key**. It is not, and cannot be, a
page of `@speed/tenancy-ui` or `@speed/product-shell`.

It renders the platform's backend administration surfaces as one console
frame, the console root being a dashboard:

- the **dashboard** at the console root (`/`) -- a KPI row derived from
  the roster read (its length and status split) and the newest page of the
  audit query as the recent-activity list;
- the **tenant roster** over `/api/v1/system/tenants` -- list, register, rename,
  enable/disable, and a row opens that tenant's detail page;
- the **account surface** over `/api/v1/system/auth/accounts` -- list, detail,
  and the writes whose request body the contract declares;
- the **session surface** over `/api/v1/system/auth/accounts/:id/sessions` --
  list one account's sessions and revoke one;
- the **impersonation surface** over `/api/v1/system/tenants/{tenant}/impersonations` -- list
  a tenant's records (the read takes the tenant as a required query
  argument) and terminate one;
- the **authorization surface** over `/api/v1/system` -- a tenant's roles,
  their grants, the permission catalog (each permission shows the
  server-rendered description beneath its code, both in the catalog and
  in the grant picker) and the account bindings;
- the **invitation and membership surface** over
  `/api/v1/tenants/:tenant/invitations` and `/members` -- a tenant's
  invitations and members;
- the **SSO surface** over `/api/v1/tenants/{tenant}/sso/providers` -- a tenant's
  providers and their domains;
- the **settings read-write surface** over `/api/v1/system/settings`;
- the **SMS template surface** over `/api/v1/system/sms`;
- the **audit query** over `/api/v1/system/audit` -- filtered, cursor-paged, with
  readable actor identities (resolved through the account read, which
  needs `authn.account.read` beside the page's own `audit.read`), and a
  row opens that record's detail page.

The tenant detail and audit detail pages are declared without a navigation
label -- reachable from a list row, not listed in the navigation.

Each page opens with `@speed/ui-kit`'s `PageHeader`: the page title as the
level-one heading and a breadcrumb trail from the console to the page. The
refresh action that re-reads the page's data sits in the header on pages
that are not addressed by a selector; a page whose body is addressed by a
selector (the sessions page's account, or the tenant-addressed pages'
tenant) puts it in the selector's row, immediately to the selector's
right.

The pages whose routes carry the tenant as a path segment (authorization,
invitations and membership, SSO), and the impersonation page (whose read
takes the tenant as a required query argument), carry a tenant selector
fed by the platform roster read. The tenant it names is an argument of the
request, not the session's current tenant: the console still has no
current-tenant concept.

Like the tenant-facing shell, the console reuses the declarative
registration seam: each page is a declaration collected by the
application's composition layer. The console imports no router and does
no path matching.

The frame itself is `@speed/layout-kit`'s `AppShell` -- the same frame
the tenant-facing shell builds on. Its default `'topbar'` form is a fixed
top bar, a responsive navigation sidebar (permanent on wide viewports, a
hamburger drawer with an overlay on narrow ones) and a single scrolling
content region. Passing `shellLayout="sidebar"` selects the other form:
at `md` and up no app bar renders, the console title sits in the drawer
paper's top row and the action slots in its bottom row, and the content
gets the full width under a minimal top bar on narrow viewports. The
console derives the platform navigation from the labelled declarations
and hands it to that frame, so the two shells are one shape. It passes the
frame's desktop sidebar-collapse pair (`sidebarCollapsed` /
`onSidebarCollapsedChange`) straight through as well: give the frame the
callback and it renders the collapse toggle and reports each flip, so a
host that holds the collapse state gives the operator the same
host-controlled collapse the tenant-facing shell carries. Omit the pair and
the sidebar stays open.

The shell also takes the host's account settings destination as
`accountHref` (a host-relative path or an absolute URL). The step-up
dialog's "no active factor" state renders it as a link when supplied and
text only when absent; the dialog never holds a host path itself.

The rest of the frame is passed through unchanged: `shellLayout`,
`sidebarWidth`,
`userMenu`, `banner`, `mobileOpen` / `onMobileOpenChange` and
`navHeader` / `navFooter` reach `AppShell` as given, and `navLabel`
overrides the package's own navigation-landmark name. The console adds no
chrome to any of them, in either form.

Selection is the host's, because this package does no path matching:
supply `isSelected(path)` and each navigation entry's `selected` flag is
read from it. Without it no entry is selected.

Navigation entries and in-page links are real links whose `href` is
always present. A plain primary click is handed to `onNavigate` when
supplied; a Ctrl/Command/Shift/Alt click, a non-primary button, a link
that asks for a new tab or a download, and an activation another handler
already claimed keep the browser's own behavior.

The shell's package-level seams (navigation, route table, session,
account destination, invitation link, impersonation change and start,
role descriptions, feature presence) are available without the frame.
`ConsoleProvider` installs exactly the same contexts and renders no DOM:

```tsx
import { ConsoleProvider } from '@speed/console-ui'

<ConsoleProvider
  hasFeature={hasFeature}
  routes={narrowedRoutes}
  session={session}
  onNavigate={navigate}
>
  <ConsolePage />
</ConsoleProvider>
```

A host renders it when it wants a platform-staff frame of its own -- its
own chrome, a dialog, a side panel, an overlay outside the console's main
area -- while keeping the console pages' seams available. It remains a
platform-staff surface: it must not host tenant-facing pages, and it
carries no current-tenant affordance. `hasFeature` is required here too;
every other prop is optional and its absence leaves the documented
fallback in force.

The pages whose routes carry the tenant as a path segment (authorization,
invitations and membership, SSO) and the impersonation page read the
`tenant` query parameter as their selector's initial value. Each of those
pages also takes optional `tenant` (controlled: supplied locks the value
and hides the selector), `defaultTenant` (initial value, winning over the
URL) and `onTenantChange` (reported on every change); supplying none of
them keeps the URL-seeded behavior.

The navigation is a flat single column by default -- one entry per
labelled declaration, in declaration order. Pass `navGroups` (the
package's own `consoleNavGroups`, or a host-supplied table) to group the
pages by capability domain instead: each declaration names its group
through its `group` field, and the shell renders the groups in the
table's order with each group's entries in declaration order beneath it.
A group may also carry a decorative `icon`, which the shell passes through
to `AppShellNavGroup.icon` and which replaces the clipped heading in the
collapsed sidebar's icon rail. Omit `navGroups` and the flat column is
unchanged.

The package's own labelled declarations carry a decorative `icon`, which
the shell passes to the frame's `AppShellNavItem.icon`; a host's own
declaration may carry one too. Collapsing the sidebar therefore gives the
icon rail the layout design intends -- each entry keeps its icon and its
label is clipped to the entry's accessible name -- rather than a column of
truncated text. The icons ship as hand-authored 24px stroke SVGs
(`src/internal/icons.tsx`) and render `aria-hidden`, so the label alone
names each entry.

**Feature gating is the host's.** A declaration may carry `requires`, a
list of generated `FeatureName`s the page depends on (this package's own
pages do). The shell does not filter by it: the host narrows the table
with its own feature-presence predicate (for example `@speed/route-kit`'s
`presentDeclarations`) before passing it, and hands the shell a required
`hasFeature(name)` answer, which the shell publishes to the routed pages
through the package's feature context. A page can therefore hide a
feature-gated control without importing the host's session package.

## What ships

| Module | Exports |
|---|---|
| `ConsoleShell.tsx` | `ConsoleShell`, `ConsoleShellProps` |
| `ConsoleProvider.tsx` | `ConsoleProvider`, `ConsoleProviderProps` |
| `internal/describe-role.tsx`, `internal/impersonation-starting.tsx`, `internal/impersonation-change.tsx` | Type-only: `ConsoleDescribeRole`, `ConsoleImpersonationStarting`, `ConsoleImpersonationChange` (the callback types the shell and provider props reference) |
| `internal/session.tsx` | `ConsoleSessionProvider`, `ConsoleSessionProviderProps` — the shell mounts it from its `session` prop; a host rendering pages without the shell mounts it itself |
| `route.ts` | `ConsoleRoute`, `ConsoleRouteContext`, `ConsoleRouteParams`, `ConsoleRouteTable`, `ConsoleNavGroup` |
| `nav-groups.tsx` | `consoleNavGroups` |
| `operations.tsx` | `ConsoleOperations`, `consoleOperations`, `ConsoleOperationsProvider`, `useConsoleOperations`, `ConsoleAuditRecordsOptions`, `settingKeyRest`, the view row types, the write input types |
| `routes.tsx` | `dashboardAdminRoutes`, `tenantAdminRoutes`, `accountsAdminRoutes`, `sessionsAdminRoutes`, `impersonationsAdminRoutes`, `permissionsAdminRoutes`, `invitationsAdminRoutes`, `ssoAdminRoutes`, `settingsAdminRoutes`, `smsAdminRoutes`, `auditAdminRoutes`, `consoleRoutes` |
| `DashboardPage.tsx` | `DashboardPage`, `KpiCard`, `KpiRow`, `DashboardPageProps` |
| `TenantRosterPage.tsx` | `TenantRosterPage`, `TenantRosterPageProps` |
| `TenantDetailPage.tsx` | `TenantDetailPage`, `ConsoleTenantRelatedPage`, `TenantDetailPageProps` |
| `AccountsPage.tsx` | `AccountsPage`, `AccountsPageProps`, `AccountsPageExtraColumn`, `AccountsPageAttributeField` |
| `SessionsPage.tsx` | `SessionsPage` |
| `ImpersonationsPage.tsx` | `ImpersonationsPage`, `ImpersonationsPageProps` |
| `PermissionsPage.tsx` | `PermissionsPage`, `PermissionsPageProps` |
| `InvitationsPage.tsx` | `InvitationsPage`, `InvitationsPageProps` |
| `SsoPage.tsx` | `SsoPage`, `SsoPageProps` |
| `SettingsPage.tsx` | `SettingsPage` |
| `SmsPage.tsx` | `SmsPage` |
| `AuditPage.tsx` | `AuditPage` |
| `AuditDetailPage.tsx` | `AuditDetailPage`, `AuditDetailPageProps` |
| `resources.ts` | `CONSOLE_UI_NAMESPACE`, `consoleUiResources` |

Everything else (`src/internal/`) is shared plumbing -- the error-text
resolver, the `InlineError` and `WriteFeedbackRegion` bindings, the
translation hook, the tenant selector, the account identity unit, the
zero-instant sentinel and the package's internal contexts -- and is
deliberately not exported. The callback types above and the session
provider are the exceptions: the types are type-only exports, because a
host annotating the shell's `describeRole`, `onImpersonationStarting` or
`onImpersonationChange` by hand needs them, and `ConsoleSessionProvider`
is the public provider the shell mounts (a host composing pages without
the shell mounts it itself); no other internal value or provider is
re-exported.

## Quick start

The console presumes a platform operator's session: an access token is
already bound into the client (the same memory-token store the
tenant-facing app uses), and the operator's permissions are the server's
to judge -- the console renders the answer and never decides. The
composition is the four parts of every authenticated host -- the
bilingual i18n instance with every namespace a rendered surface reads,
the query client, the console operations bundle, and the provider stack
around the console -- plus the host's own route layer rendering the
matched declaration's view inside the shell.

```tsx
import type { ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  CONSOLE_UI_NAMESPACE,
  ConsoleOperationsProvider,
  ConsoleShell,
  consoleOperations,
  consoleRoutes,
  consoleUiResources,
} from '@speed/console-ui'

const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
registerNamespace(i18n, CONSOLE_UI_NAMESPACE, consoleUiResources)

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
})

export function ConsoleApp(): ReactElement {
  // The host's route layer renders the matched declaration's view; the
  // console's navigation is derived from the labelled declarations.
  const route = consoleRoutes[0]
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <ConsoleOperationsProvider operations={consoleOperations}>
            <ConsoleShell
              routes={consoleRoutes}
              title="Speed Console"
              // The host's synchronous feature answer, published to the
              // routed pages. Pass the host's real predicate; `() => true`
              // is only a stand-in for a deployment serving every named
              // feature.
              hasFeature={() => true}
            >
              {route?.view({ path: route.path, params: {} })}
            </ConsoleShell>
          </ConsoleOperationsProvider>
        </QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>
  )
}
```

## Binding the operations

The pages read and write through the `ConsoleOperations` bundle the host
mounts in `ConsoleOperationsProvider`: a plain object of react-query read
hooks and write mutations. The hooks run on the host's query client (the
host supplies it as it supplies the rest of the tree); no page builds a
query client of its own and none writes a query key.

Two caller-session surfaces sit outside the bundle. The impersonation
page's own-session half calls the generated caller-session operations
directly (`useAuthnImpersonationStatus`, `useAuthnImpersonationStart`,
`useAuthnImpersonationEnd`), and the step-up dialog reads the operator's
own factors through `GET /api/v1/auth/factors`. Both act on the caller's own
session rather than an administration surface, so neither is replaceable
through the bundle.

The dialog renders each parked step through the package's step renderer
(`src/internal/flow-step.tsx`). A `webauthn` step renders the factor its
options name: a TOTP enrolment's `otpauth_uri` is parsed by
`@speed/auth-core/otpauth` and drawn through `@speed/ui-kit`'s
`TotpEnrollmentPanel` as a scannable QR symbol with a manual-entry
disclosure for the base32 secret and its parameters, beside a six-digit
code field, and the URI itself is never shown as text; a passkey's
options drive `@speed/auth-core/webauthn`. Each passkey ceremony failure
renders its own sentence: an unsupported browser, a dismissed prompt and
a device that already holds the credential each have their own wording,
and the generic failure sentence names the ceremony, so a failed
registration does not read as a failed sign-in.

The package ships that bundle. `consoleOperations` is bound to the
generated `@speed/api-sdk` surface -- every administration read and write
across the modules' admin routes, through the generated query keys and
the generated mutation functions -- so a host mounts it unchanged over a
client already bound into the api-sdk runtime seam, and the pages never
change. The provider is an override: a host that wants different wiring,
or a suite that scripts a backend, mounts its own bundle and the pages
read that instead.

## What this console does not do

- **No current tenant.** It has no current-tenant switcher and uses no
  current-tenant query key: its endpoints do not resolve a tenant.
  Switching the current tenant is `@speed/tenancy-ui`'s. A tenant-named
  selector on the pages whose routes carry the tenant as a path segment
  names an argument of the request, not the session's current tenant.
- **No routing.** It exports page declarations and never matches a URL or
  decides navigation selection; collecting the declarations and rendering
  them is the application's route layer's. It does link from a known path:
  the frame links each navigation entry to its declaration's path, and a
  list page links each row to that record's detail path. The frame exposes
  one seam for the host's router: given an `onNavigate` callback, a plain
  click on a navigation entry -- or on a page's detail link, which the frame
  publishes the callback to -- is handed to it (with the target path) so the
  host routes in place, while the link stays a real link for a middle-click
  or a copied URL. Absent the callback, it is a plain link that navigates the
  browser.
- **No failure conflation.** A refused read (HTTP 403, or a bare 403
  surfaced as the `client.http.403` code) renders the permission
  placeholder with no retry and replaces whatever content was on screen.
  Any other failure over an already settled payload keeps that content
  and renders the stale notice above it, withdrawn while the section is
  re-reading; any other failure with no settled payload renders the
  retryable error placeholder. The branch reads the answer the server
  wrote, not a route-level permission read.
- **No opaque collision copy.** An account create or identifier add the
  server refuses because the email address is already registered (409
  `authn.email_taken`) renders that code's text, naming the address and
  asking for another, rather than the generic fallback.
- **No authorization decisions.** The permission code a declaration
  carries is data; the server decides on every request and the host
  composes any route guard. A page renders its write controls and lets
  the server refuse a caller who lacks the permission.
- **No second masking.** A sensitive setting arrives already masked from
  the server; the page renders the mask it was sent.
- **No query client of its own.** The reads and writes run on the host's
  query client, through the generated @speed/api-sdk operations the
  shipped `consoleOperations` bundle binds (or the bundle the host mounts
  instead).
- **Only the writes the contract declares.** A write's request body is
  the one the platform's OpenAPI fragment declares for its endpoint, so
  the generated client sends it and the page renders its control. The
  account surface's create/patch/status/MFA-requirement/password/
  identifier-add/secret-create writes, the SSO provider create/update and
  domain claim, and the member write and SSO exemption all declare a
  request body and all render a control.
