# @speed/console-ui

The platform-staff console of a frontend built on speed: the second
shell. Where `@speed/product-shell` is the tenant-facing front door,
this is the console platform operators use -- and the difference is the
point. The administration surfaces it renders are mounted on endpoints
that do not resolve a tenant, so the console has **no tenant switcher and
no tenant-scoped query key**. It is not, and cannot be, a page of
`@speed/tenancy-ui` or `@speed/product-shell`.

It renders three backend administration surfaces as one console frame
with three pages:

- the **tenant roster** over `/tenant/admin` -- list, register, rename,
  enable/disable;
- the **settings read-write surface** over `/settings/admin` -- list the
  declared items, write one value, remove one override;
- the **audit query** over `/audit/admin` -- list the journal's records.

Like the tenant-facing shell, the console reuses the declarative
registration seam: each page is a declaration collected by the
application's composition layer. The console imports no router and does
no path matching.

## What ships

| Module | Exports |
|---|---|
| `ConsoleShell.tsx` | `ConsoleShell`, `ConsoleShellProps` |
| `route.ts` | `ConsoleRoute`, `ConsoleRouteContext`, `ConsoleRouteParams`, `ConsoleRouteTable` |
| `operations.tsx` | `ConsoleOperations`, `consoleOperations`, `ConsoleOperationsProvider`, `useConsoleOperations`, `settingKeyRest`, `ConsoleTenant`, `ConsoleSetting`, `ConsoleAuditRecord`, the write input types |
| `routes.tsx` | `tenantAdminRoutes`, `settingsAdminRoutes`, `auditAdminRoutes`, `consoleRoutes` |
| `TenantRosterPage.tsx` | `TenantRosterPage` |
| `SettingsPage.tsx` | `SettingsPage` |
| `AuditPage.tsx` | `AuditPage` |
| `resources.ts` | `CONSOLE_UI_NAMESPACE`, `consoleUiResources` |

Everything else (`src/internal/`) is shared plumbing -- the error-text
resolver, the `InlineError` binding and the translation hook -- and is
deliberately not exported.

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
generated `@speed/api-sdk` surface -- the `/tenant/admin`,
`/settings/admin` and `/audit/admin` operations of every read, through
the generated query keys and the generated mutation functions -- so a host
mounts it unchanged over a client already bound into the api-sdk runtime
seam, and the pages never change. The provider is an override: a host that
wants different wiring, or a suite that scripts a backend, mounts its own
bundle and the pages read that instead.

## What this console does not do

- **No tenant concept.** It has no tenant switcher and uses no
  tenant-scoped query key: its endpoints do not resolve a tenant.
  Switching the current tenant is `@speed/tenancy-ui`'s;
  `@speed/product-shell`'s job.
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
