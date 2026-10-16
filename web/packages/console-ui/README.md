# @speed/console-ui

The platform-staff console of a frontend built on speed: the second
shell. Where `@speed/product-shell` is the tenant-facing front door,
this is the console platform operators use -- and the difference is the
point. The administration surfaces it renders are mounted on endpoints
that do not resolve a tenant, so the console has **no current-tenant
switcher and no current-tenant query key**. It is not, and cannot be, a
page of `@speed/tenancy-ui` or `@speed/product-shell`.

It renders the platform's backend administration surfaces as one console
frame with one page each:

- the **tenant roster** over `/tenant/admin` -- list, register, rename,
  enable/disable;
- the **account surface** over `/auth/admin/accounts` -- list, detail,
  and the writes whose request body the contract declares;
- the **session surface** over `/auth/admin/accounts/:id/sessions` --
  list one account's sessions and revoke one;
- the **impersonation surface** over `/auth/admin/impersonations` -- list
  a tenant's records (the read takes the tenant as a required query
  argument) and terminate one;
- the **authorization surface** over `/authz/admin` -- a tenant's roles,
  their grants, the permission catalog and the account bindings;
- the **invitation and membership surface** over
  `/auth/admin/tenants/:tenant/invitations` and `/members` -- a tenant's
  invitations and members;
- the **SSO surface** over `/auth/admin/sso/providers` -- a tenant's
  providers and their domains;
- the **settings read-write surface** over `/settings/admin`;
- the **SMS template surface** over `/sms/admin`;
- the **audit query** over `/audit/admin` -- filtered, cursor-paged, with
  readable actor identities (resolved through the account read, which
  needs `authn.account.read` beside the page's own `audit.read`).

Each page opens with `@speed/ui-kit`'s `PageHeader`: the page title as the
level-one heading, a breadcrumb trail from the console to the page, and a
refresh action that re-reads the page's data.

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

## What ships

| Module | Exports |
|---|---|
| `ConsoleShell.tsx` | `ConsoleShell`, `ConsoleShellProps` |
| `route.ts` | `ConsoleRoute`, `ConsoleRouteContext`, `ConsoleRouteParams`, `ConsoleRouteTable` |
| `operations.tsx` | `ConsoleOperations`, `consoleOperations`, `ConsoleOperationsProvider`, `useConsoleOperations`, `settingKeyRest`, the view row types, the write input types |
| `routes.tsx` | `tenantAdminRoutes`, `accountsAdminRoutes`, `sessionsAdminRoutes`, `impersonationsAdminRoutes`, `permissionsAdminRoutes`, `invitationsAdminRoutes`, `ssoAdminRoutes`, `settingsAdminRoutes`, `smsAdminRoutes`, `auditAdminRoutes`, `consoleRoutes` |
| `TenantRosterPage.tsx` | `TenantRosterPage` |
| `AccountsPage.tsx` | `AccountsPage` |
| `SessionsPage.tsx` | `SessionsPage` |
| `ImpersonationsPage.tsx` | `ImpersonationsPage` |
| `PermissionsPage.tsx` | `PermissionsPage` |
| `InvitationsPage.tsx` | `InvitationsPage` |
| `SsoPage.tsx` | `SsoPage` |
| `SettingsPage.tsx` | `SettingsPage` |
| `SmsPage.tsx` | `SmsPage` |
| `AuditPage.tsx` | `AuditPage` |
| `resources.ts` | `CONSOLE_UI_NAMESPACE`, `consoleUiResources` |

Everything else (`src/internal/`) is shared plumbing -- the error-text
resolver, the `InlineError` binding, the translation hook, the tenant
selector and the timestamp formatter -- and is deliberately not exported.

## Quick start

The console presumes a platform operator's session: an access token is
already bound into the client (the same memory-token store the
tenant-facing app uses), and the operator's permissions are the server's
to judge -- the console renders the answer and never decides. The
composition is the four parts of every authenticated host -- the
bilingual i18n instance with both namespaces a rendered surface reads,
the query client, the console operations bundle, and the provider stack
around the console -- plus the host's own route layer rendering the
matched declaration's view inside the shell.

```tsx
import type { ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
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
            <ConsoleShell routes={consoleRoutes} title="Speed Console">
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
- **No routing.** It exports page declarations and never matches a URL,
  builds a link from a path or decides navigation selection. Collecting
  the declarations and rendering them is the application's route layer's.
  The frame does expose one seam for the host's router: given an
  `onNavigate` callback, a plain click on a navigation entry is handed to
  it (with the entry's path) so the host routes the entry in place, while
  the entry stays a real link for a middle-click or a copied URL. Absent
  the callback, an entry is a plain link that navigates the browser.
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
