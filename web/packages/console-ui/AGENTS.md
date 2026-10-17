# AGENTS.md — @speed/console-ui

## What this package is

The platform-staff console — the second shell. It renders the platform's
backend administration surfaces as one console frame: the tenant roster
(`/tenant/admin`), the account, session and impersonation surfaces
(`/auth/admin`), the authorization surface (`/authz/admin`), the
invitation and membership surface (`/auth/admin/tenants/:tenant/...`),
the SSO surface (`/auth/admin/sso`), the settings read-write surface
(`/settings/admin`), the SMS template surface (`/sms/admin`) and the
audit query (`/audit/admin`). The console root renders a dashboard, and
the tenant roster and the audit query each link a row to that record's
detail page.

Those endpoints are mounted on hosts that do not resolve a tenant, so the
console has **no current-tenant switcher and no current-tenant query
key**: it is not a page of `@speed/tenancy-ui` or `@speed/product-shell`,
and neither of those packages belongs in its import graph.

The pages whose routes carry the tenant as a path segment (authorization,
invitations and membership, SSO) carry a **tenant selector** — an argument
of the request, fed by the roster read, not the session's current tenant.
The impersonation page carries one too: its list read takes the tenant as
a required query argument. The selector lives in
`src/internal/tenant-scope.tsx` and holds no query key of the
current-tenant kind.

The package ships one entry (`src/index.ts`, the export set in the
README's "What ships") and no subpath. Everything under `src/internal/`
— the error-text resolver, the `InlineError` binding, the translation
hook, the tenant selector and the timestamp formatter — is family
plumbing and is deliberately not exported.

## The second shell (the one thing this package's frame decides)

`ConsoleShell` is the platform frame, rendered through `@speed/layout-kit`'s
`AppShell`: a fixed `header` banner (the console title and any host header
actions), a responsive `nav` drawer over the platform navigation (the
labelled declarations), and a `main` landmark holding the host's routed
content. The console names that `nav` landmark from its own namespace
(`shell.navLabel`) through the frame's `navLabel`, so the platform
navigation stays distinguishable from any other shell's. Reusing
`AppShell` is what makes the two shells one shape — a sidebar that stays
put while the content scrolls, a hamburger drawer on a narrow viewport
rather than the navigation stacked above the content, and the content
region as the single scroll layer. The frame also carries the frame's
desktop sidebar-collapse pair (`sidebarCollapsed` /
`onSidebarCollapsedChange`), passed straight through: supplying the
callback renders the collapse toggle and reports each flip, so a host
that holds the collapse state gives the operator the same host-controlled
collapse here that the tenant shell carries. The frame carries no
current-tenant affordances; that absence is the difference between the two
shells, not the chrome.

The shell does no routing. It never matches a URL, never builds a link
from a path and never decides which navigation entry is selected: the
declarations are collected and rendered by the application's route layer
(a consumer of `@speed/route-kit`, or the host's own), and the console's
navigation entries are links to each labelled declaration's path. The
entries stay real links, but when the host supplies `onNavigate` a plain
click is intercepted and handed to it (with the entry's path) instead of
reloading the page — that is how a host routes an entry in place without
this package importing a router.

The shell also publishes host-owned destinations to its routed content
through package context: `invitationLink`, the acceptance-link builder,
and `accountHref`, the host's account settings page. The step-up dialog
renders `accountHref` as its enrolment link when the host supplies it and
text only when it does not; the dialog never holds a host path.

## The page declarations

Each administration capability contributes declarations to the
declarative registration seam (`dashboardAdminRoutes`,
`tenantAdminRoutes`, `accountsAdminRoutes`, `sessionsAdminRoutes`,
`impersonationsAdminRoutes`, `permissionsAdminRoutes`,
`invitationsAdminRoutes`, `ssoAdminRoutes`, `settingsAdminRoutes`,
`smsAdminRoutes`, `auditAdminRoutes`, or the whole set as
`consoleRoutes`). A capability may contribute more than one declaration:
a list page carries a navigation label and icon and a detail page (the
tenant detail, the audit detail) carries neither, so it is reachable from
a row without appearing in the navigation. A declaration carries a path
pattern, a view, an optional navigation label, an optional decorative
icon and the permission code the page's data is held to (`tenant.read`,
`authn.account.read`, `authn.session.read`,
`authn.impersonation.read`, `authz.role.read`, `authn.membership.manage`,
`authn.idp.manage`, `settings.read`, `sms.template.read`, `audit.read`).
A list declaration's view carries the page's own matched path (`basePath`)
so the page links each row to `${basePath}/${id}`; a detail declaration's
view carries the id and the parent path (`parentHref`).
The shapes in `src/route.ts` are kept structurally identical to
`@speed/route-kit`'s registration surface so a declaration this package
exports is assignable to route-kit's `RouteDeclaration` **without
importing route-kit** — same-tier packages never import one another.

A declaration's label is a `ConsoleNavLabel` element, not a string: the
declarations stay plain module-level values (the seam wants a value)
while their labels resolve their text through the console-ui namespace
when they render, so the navigation translates reactively with the
host's language.

A declaration's icon is decorative host content, passed through to the
frame's `AppShellNavItem.icon`. The package's own pages carry hand-authored
24px stroke SVGs from `src/internal/icons.tsx`, each rendering
`aria-hidden`; the label remains the entry's accessible name, and a
collapsed sidebar shows the icon rail the layout design intends rather
than clipped text.

The permission code is data. This package never decides on it: the
server judges every request, and the host reads the code to compose any
route-level guard. A page renders its write controls whether or not the
caller holds the write permission and lets the server refuse — the
server is the authority, and the page must not invent a client-side gate.

## The operations seam (why the pages read through a bound bundle)

The pages read and write through the `ConsoleOperations` bundle the host
mounts in `ConsoleOperationsProvider` (`useConsoleOperations`): a plain
object whose members are the react-query read hooks and write mutations
of the administration surfaces. The hooks run on the host's query client,
the mutations go through the same bound client, and no page builds a
query client of its own or writes a query key — the keys live inside the
hooks the host binds.

Two caller-session surfaces sit outside the bundle. The impersonation
page's own-session half calls the generated caller-session operations
directly (`useAuthnImpersonationStatus`, `useAuthnImpersonationStart`,
`useAuthnImpersonationEnd`), and the step-up dialog reads the caller's own
factors through `GET /auth/factors`. Neither is an administration surface
the bundle substitutes: they act on the operator's own session, so they
run the generated hooks on the host's query client and are not
replaceable through the bundle.

Why the pages read through a bundle and not a direct `@speed/api-sdk`
call each: the bundle is the substitution point. The shipped
`consoleOperations` is a thin adapter over the generated `@speed/api-sdk`
operations: the reads run the generated query options through `useQuery`
with a `select` that flattens the page envelope to the view rows (or
keeps the envelope when the page offers server-side pagination), and the
writes call the generated mutation functions and invalidate the list
through the generated key factories. A read whose route carries a
parameter (a tenant, a role, an account) takes that parameter, so its
generated query key namespaces by it; a read whose parameter is undefined
declares itself disabled rather than firing a malformed request.

What the pages reach through the bundle, grouped by surface:

| Members | Routes |
|---|---|
| `useTenants`, `useTenant`, `useCreateTenant`, `useSetTenantAlias`, `useSetTenantStatus` | `/tenant/admin/tenants[/:id]` (list / get / create / alias / status) |
| `usePermissions`, `useRoles`, `useWriteRole`, `useRemoveRole`, `useGrants`, `useGrantPermission`, `useRevokePermission`, `useBindings`, `useAccountBindings`, `useBindRole`, `useUnbindRole` | `/authz/admin/permissions`, `/authz/admin/tenants/:tenant/roles[/:role[/grants[/:permission]]]`, `/authz/admin/tenants/:tenant/bindings[/:account[/:role]]` |
| `useAccounts`, `useAccount`, `useAccountFactors`, `useAccountSecrets`, `useAccountSessions`, `useCreateAccount`, `usePatchAccount`, `useSetAccountStatus`, `useSetMFARequired`, `useResetPassword`, `useAddIdentifier`, `useCreateSecret`, `useDeleteAccount`, `useRemoveIdentifier`, `useResetMFA`, `useRevokeAccountSessions`, `useRevokeSecret`, `useRevokeSession` | `/auth/admin/accounts[/:id[/factors|/secrets[/:secret]|/sessions|/identifiers/:identifier|/mfa-reset|/revoke-sessions|/status|/mfa-required|/password|/identifiers]]`, `/auth/admin/sessions/:id` |
| `useImpersonations`, `useRevokeImpersonation` | `/auth/admin/impersonations[/:id]` |
| `useInvitations`, `useIssueInvitation`, `useRevokeInvitation`, `useMembers`, `useWriteMember`, `useSetMemberSsoExempt`, `useRemoveMember` | `/auth/admin/tenants/:tenant/invitations[/:id]`, `/auth/admin/tenants/:tenant/members[/:account[/sso-exempt]]` |
| `useSsoProviders`, `useSsoProvider`, `useCreateSsoProvider`, `useUpdateSsoProvider`, `useClaimSsoDomain`, `useDeleteSsoProvider`, `useRemoveSsoDomain`, `useVerifySsoDomain`, `useRefreshSsoMetadata`, `useReverifySsoDomains` | `/auth/admin/tenants/:tenant/sso/providers`, `/auth/admin/sso/providers/:id[/domains[/:domain[/verify]]|/metadata/refresh|/reverify-domains]` |
| `useSettings`, `useSetSetting`, `useRemoveSetting` | `/settings/admin/settings[/:namespace[/:key]]` |
| `useSmsTemplates`, `useRegisterSmsTemplate`, `useOfferSmsTemplate`, `useSetSmsTemplateEnabled`, `useRemoveSmsTemplate` | `/sms/admin/templates[/submissions|/:name[/enabled]]` |
| `useAuditRecords`, `useAuditRecord` | `/audit/admin/records[/:id]` |

The view types are the generated response rows of each module's admin
surface, named for the console's own pages; only the write inputs stay
hand-written, because they carry route parameters beside the body.

The settings route addresses an item by its namespace and **the rest of
the key under it** (`:key`), the full key being `<namespace>.<key>` — and
a module name may itself contain a dot (`authn.session`), so the rest
cannot be derived by splitting on the first dot. `settingKeyRest(fullKey,
namespace)` computes the rest.

### The write request bodies are declared by the platform fragment

A write's request body comes from the platform OpenAPI fragment's
`requestBody` for that endpoint. The account surface's create, patch,
status, MFA-requirement, password-reset, identifier-add and secret-create
writes, the SSO provider create/update and domain-claim, and the member
write and SSO exemption all **declare a `requestBody` in the platform's
fragment**, so the generated client sends a body for each and the pages
render a control for each. The fragment's body matches the structure the
handler decodes with `mount.Decode`; `pkg/authn/admin/requestbody_test.go`
holds the two together — a handler that decodes a body the fragment does
not declare fails that check. The frontend still never hand-writes a
request body.

## Non-negotiable rules

- **No current tenant.** No current-tenant switcher, no
  current-tenant-scoped query key, no `useCurrentTenant`, no import of
  `@speed/tenancy-ui`, and no current-tenant reading. The console is the
  platform side; the tenant side is a different shell. The tenant selector
  on the pages whose routes carry the tenant as a **path segment** is not
  this: it names an argument of the request and is fed by the roster read.
  A change that wants to read "the current tenant" here is scope drift.
  The console DOES depend on `@speed/auth-core` for one thing only: the
  session whose step-up flow a privileged write elevates (the
  `session` prop of `ConsoleShell`, published to the pages through
  `ConsoleSessionProvider`, `session.isStepUpShortfall` for the gate, and
  `session.logout` for the challenge's re-login when this deployment
  cannot raise the session). That is the
  session operation, not a tenant read; `useCurrentTenant` stays unused.
- **No routing in package code.** No path matching, no `react-router`. The
  shell links each labelled entry to its declaration's path, and a page
  builds a detail link from its own matched path plus a row id; matching a
  URL to a view is the application's route layer's. `onNavigate` is a host
  callback the frame invokes on a click — and publishes to its routed pages
  through an internal context, so a page's detail link routes in place
  too — not a routing mechanism this package owns.
- **The pages read and write only through the operations bundle.** No
  page constructs a `QueryClient`, and none writes a query key. A query
  key is whatever a generated hook carries; a read's parameter is the
  argument the generated key factory takes.
- **No second masking and no client-side authorization.** A sensitive
  setting's value arrives masked from the server and renders as-is; the
  permission a declaration carries is never a gate this package applies.
- **A read's failure branches on the answer.** A refused read — HTTP 403,
  or a bare 403 surfaced as the `client.http.403` code — renders the
  permission placeholder with no retry (`ConsoleAsyncSection` in
  `src/internal/async-section.tsx`); any other failure renders the retryable
  error placeholder. The branch reads the answer the server wrote, never a
  route-level permission read.
- **Every write goes through a generated operation.** No page hand-writes
  a `fetch` or an endpoint path; a request body the generated client does
  not send is not hand-written here — the fragment declares it or the
  control is not rendered.
- **Framework peers stay peers.** `react`, `react-dom`, `@mui/material`,
  `@emotion/*` and `@tanstack/react-query` are peer (required)
  dependencies; `i18n`, `ui-kit`, `layout-kit` and `api-sdk` are regular
  dependencies. The manifest is pinned by `src/package.json.test.ts`.
- **Every built-in string is a console-ui namespace key.** The
  `speed/no-literal-text` rule (workspace config) enforces it over
  `src/`; the two bundles under `src/locales/` carry identical leaf key
  sets, pinned by `src/resources.test.ts` and
  `src/internal/error-text.test.ts`.
- **The error-text whitelist covers every module code a write can be
  answered with.** A console write the server refuses surfaces as the
  module code the api-client derives from the refusal's RFC 9457 problem
  `type` (`<module>.<code>`, each dash of the code part normalized to an
  underscore); the whitelist carries one leaf per language for each
  reachable code, so a refusal renders its own text rather than the
  generic fallback. A new write surface extends the whitelist with the
  codes it can meet.
- **Each page opens with ui-kit's `PageHeader`.** The page title is the
  level-one heading, a breadcrumb trail runs from the console to the
  page, and a refresh action re-reads the page's data (its `refetch`).
  The sections and `AsyncSection` placeholders continue from that `h1`
  (`headingLevel="h2"`), so the heading order never skips.

## Testing

Unit tests are vitest + jsdom, one file per source under `src/`, shared
helpers only in `test-utils/`. `renderWithProviders` mounts the unit
under the tree a real host builds — `I18nextProvider` around `ui-kit`'s
`AppThemeProvider` around `QueryClientProvider` around
`ConsoleOperationsProvider` — with a fresh i18n instance (the console-ui,
ui-kit and layout-kit namespaces registered, the last because the frame
renders `AppShell`'s built-in strings), a fresh no-retry query client, and
a caller-supplied operations bundle. `test-utils/operations.tsx` provides
that bundle: `createConsoleOperations(backend)` builds real react-query
hooks over a scripted in-memory backend (`makeConsoleBackend`), so the
pages exercise genuine react-query states — the first-load pending
branch, a settled empty answer, a failed read, and a write whose success
invalidates the list — with no HTTP transport. `test-utils/request-rig.ts`
binds a scripted request function into the api-sdk runtime seam, so the
shipped `consoleOperations` can be driven endpoint by endpoint.

Suites: `ConsoleShell` (the landmarks, the labelled-only navigation, the
`onNavigate` seam, the skip link, the page-tier axe scan); each page's
own suite (its reads, writes and failure branches, with an axe scan); the
`operations` suite (the shipped bundle's generated bindings for every
surface, driven over the request rig, plus the provider fallback and the
key-splitting helper); `routes` (the declarations' paths, permissions, icons and
translated labels); `resources`, `internal/error-text`,
`internal/identifier`, `internal/datetime` and `internal/async-section`
(bundle key parity, whitelist pairing, the identifier rule, the timestamp
formatting and the 403/retry split);
`package.json` (the manifest contract); and `usage-example` (the README's
composition, with the operations bundle substituted from test-utils).

## Deferrals (recorded, do not re-open silently)

- **Audit export surface.** An export route is not surfaced; export is
  another permission and another module's to carry.
- **Batched account-identity resolution.** The account identity unit (an
  audit record's actor, an invitation's inviter, a membership's account)
  resolves each distinct account id through `GET /auth/admin/accounts/:id`
  (react-query caches by id, so rows sharing an account share one read and a
  re-render reuses it), but a page of distinct accounts fires one read per
  account: no batch account-read surface exists. Resolving a page in one
  request needs a server surface this package does not have. The account
  read is gated on `authn.account.read`, which the page's own permission
  does not imply, so a viewer without it gets recorded ids marked
  unresolved rather than readable identities.
- **Storybook.** No preview harness exists; the components are covered by
  jsdom tests + axe, and color-contrast is verified nowhere.
