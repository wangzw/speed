# AGENTS.md — @speed/admin-ui

## What this package is

Two management surfaces share one set of page components, one declaration
shape and one operations seam. The platform surface is the platform-staff
console; the organization surface is the tenant-scoped half. The
organization surface ships its context provider, route declarations,
operation bundle
and the invitations/membership, SSO, tenant roles/permissions,
security and access, and organization profile page wrappers.

User-visible text in this package calls the tenant an organization
(「组织」 in the Chinese bundle). The tenancy mechanism keeps its own
name: the `tenant` prop, the `/api/v1/tenants/{tenant}` request paths and
the tenant-domain permission codes are tenant, not organization.

The package's pages address three kinds of endpoint, and the kinds are not
interchangeable:

- **Platform assets** under `/api/v1/system/...`, judged in the platform
  domain: the tenant roster, accounts, sessions, impersonation records,
  authorization, settings, SMS templates and the audit query.
- **The platform surface's tenant-addressed pages** (invitations and
  membership, SSO) under `/api/v1/tenants/{tenant}/...`, judged in that
  tenant's domain. Holding `platform-admin` alone does not admit the
  caller: the caller needs a separate binding in that tenant.
- **The organization surface** under `/api/v1/tenants/{tenant}/...`, judged in
  the current tenant's domain: invitations and membership, SSO, tenant-layer
  settings, tenant roles and permissions, and the organization profile, scoped
  to the tenant the host supplies.

**The platform surface** is the platform-staff admin — the second shell.
It renders the platform's backend administration surfaces as one admin
frame: the tenant roster
(`/api/v1/system/tenants`), the account, session and impersonation surfaces
(`/api/v1/system/auth`), the authorization surface (`/api/v1/system`), the
invitation and membership surface (`/api/v1/tenants/:tenant/...`),
the SSO surface (`/api/v1/tenants/:tenant/sso`), the settings read-write surface
(`/api/v1/system/settings`), the SMS template surface (`/api/v1/system/sms`) and the
audit query (`/api/v1/system/audit`). The admin root renders a dashboard, and
the tenant roster and the audit query each link a row to that record's
detail page. The roster's header links to the standalone `/tenants/new`
page (`TenantCreatePage`), which renders `@speed/org-create-ui`'s shared
form in platform mode; the alias, profile bounds and identifier domain are
that package's rules.

The platform surface has **no current-tenant switcher and no
current-tenant query key**: it is not a page of `@speed/tenancy-ui` or
`@speed/product-shell`, and neither of those packages belongs in its
import graph. The tenant its tenant-addressed invitation/membership and
SSO pages name is an argument of the request, not the session's current
tenant.

On the platform surface, the pages whose routes carry the tenant as a path
segment (authorization, invitations and membership, SSO) carry a **tenant
selector** — an argument of the request, fed by the roster read, not the
session's current tenant. The impersonation page carries one too: its list
read takes the tenant as a required query argument. The selector lives in
`src/internal/tenant-scope.tsx` and holds no query key of the
current-tenant kind.

**The organization surface** is the organization administrator's admin,
mounted under a host-chosen prefix (`/organization/admin` in the demo)
inside `@speed/product-shell`. It acts on the
**current tenant the host supplies** and addresses every request at
`/api/v1/tenants/{tenant}/...`: invitations and membership, SSO
providers, tenant roles and permissions, security and access, and the
organization profile. It reads nothing under `/api/v1/system`. It does not own or switch the
current tenant: the host mounts it inside the tenant frame and hands
`OrganizationAdminProvider` the resolved tenant. The organization surface
renders no `AppShell` — the tenant frame already renders one — and
imports neither `route-kit`, `tenancy-ui` nor `product-shell`. It is
offered to holders of the tenant-domain permission codes, not to platform
staff as such.

**What ships today.** `OrganizationAdminProvider.tsx` (the provider, its
props and the tenant/protected-role hooks), `OrganizationAdminBreadcrumb.tsx`
(the breadcrumb provider and its read hook), `organization-routes.tsx`
(`organizationAdminRoutes`), `organization-operations.tsx`
(`OrganizationAdminOperations`, `organizationAdminOperations`,
`OrganizationAdminOperationsProvider`, `useOrganizationAdminOperations`) and
`organization-pages.tsx` (`OrganizationInvitationsPage`, `OrganizationSsoPage`,
`OrganizationPermissionsPage`, `OrganizationSettingsPage`,
`OrganizationProfilePage`) exist in `src/`, and
`src/index.ts` exports them. The invitations wrapper renders the
organization-only `OrganizationMembersPage` with the current tenant: the
page carries the members table and the invitations table, each with
cursor pagination; the members section filters by a role drawn from the
tenant's role list; and each member row opens a management panel that
owns bind/unbind, suspend/resume and remove. The inviter identity resolves
from the members read's `display_name` rather than a platform account
read, the member table's name column takes `display_name` while its
account column shows the recorded account identifier without resolving
it, and an inviter that read cannot resolve renders its recorded
identifier with the unresolved note. The roles wrapper mounts the
organization-only `OrganizationRolesPage`: a role table with each role's
description, permission count and active-member count, a create form and a
description editor, the selected role's grants, a read-only usage section
with cursor paging, and a collapsible permission catalog at the bottom.
Binding and unbinding belong to the member panel, and the host's
`protectedRoles` are read-only beside the server's builtin roles. The settings
wrapper renders `OrganizationSecurityPage`: the two declared tenant
admission policies as switches, with their status, effect, impact and the
platform floor, saving each switch on its own and rendering no table. The
profile wrapper hands the current tenant and the host's `onTenantUpdated`
to the shared `TenantProfilePage`: a read-only identity card and an
editable profile card, whose landed save asks the host to reload the
caller tenant list the account menu and breadcrumb read. The
shared pages render through `AdminPageHeader`, which prefers the
organization breadcrumb context and falls back to the page's own platform
trail otherwise, so neither surface leaks the other's trail.
`OrganizationAdminOperations` carries the tenant-scoped invitation,
membership and SSO members, the
tenant settings members and the tenant roles/grants/bindings members; no
member addresses a platform asset or the platform account read.

The package ships one entry (`src/index.ts`, the export set in the
README's "What ships") and no subpath. The organization surface's exports
live in that same entry. Everything under `src/internal/`
— the error-text resolver, the `InlineError` and `WriteFeedbackRegion`
bindings, the translation hook, the tenant selector and the zero-instant
sentinel — is family plumbing and is deliberately not exported, except
the type-only callback types (`AdminImpersonationStarting`,
`AdminImpersonationChange`) a host
annotates its own handlers with, and `AdminSessionProvider` (with its
props), the provider the shell mounts from its `session` prop and a host
composing pages without the shell mounts itself.

## The second shell (the one thing this package's frame decides)

`AdminShell` is the platform frame, rendered through `@speed/layout-kit`'s
`AppShell` in either of its chrome forms: the default `'topbar'` form's fixed
`header` banner (the admin title and any host header actions) above a
responsive `nav` drawer over the platform navigation (the labelled
declarations), or the `'sidebar'` form, where the title and the action slots
move into the drawer paper and no app bar renders at `md` and up. A `main`
landmark holds the host's routed content in both. The admin names that
`nav` landmark from its own namespace
(`shell.navLabel`) through the frame's `navLabel`, so the platform
navigation stays distinguishable from any other shell's. Reusing
`AppShell` is what makes the two shells one shape — a sidebar that stays
put while the content scrolls, a hamburger drawer on a narrow viewport
rather than the navigation stacked above the content, and the content
region as the single scroll layer. The admin selects the frame's form
through layout-kit's `shellLayout` -- `'topbar'` by default, or
`'sidebar'` to move the admin title and the frame's action slots into
the drawer paper -- and passes it straight through, so the admin and
the tenant shell can present the same arrangement. The frame also
carries the frame's
desktop sidebar-collapse pair (`sidebarCollapsed` /
`onSidebarCollapsedChange`), passed straight through: supplying the
callback renders the collapse toggle and reports each flip, so a host
that holds the collapse state gives the operator the same host-controlled
collapse here that the tenant shell carries. The frame carries no
current-tenant affordances: the platform surface's shell never holds one,
and the organization surface takes its tenant from `OrganizationAdminProvider`
rather than from the frame. The difference between the two surfaces is
the scope they address, not the chrome.

The shell does no routing. It never matches a URL, never builds a link
from a path and never decides which navigation entry is selected: the
declarations are collected and rendered by the application's route layer
(a consumer of `@speed/route-kit`, or the host's own), and the admin's
navigation entries are links to each labelled declaration's path. The
host decides selection explicitly through `isSelected(path)`; without it
no entry is selected. The entries stay real links, but when the host
supplies `onNavigate` a plain primary click is intercepted and handed to
it (with the entry's path) instead of reloading the page — that is how a
host routes an entry in place without this package importing a router. A
click with Ctrl/Command/Shift/Alt, a non-primary button, a link that asks
for a new tab or a download, and an activation another handler already
claimed keep the browser's behavior; the judgement is layout-kit's
`isPlainPrimaryClick`, shared with the package's in-page links.

The frame's other slots and controlled props — `shellLayout`,
`sidebarWidth`,
`userMenu`, `banner`, `mobileOpen` / `onMobileOpenChange` and `navHeader`
/ `navFooter` — pass straight through to `AppShell`, and `navLabel`
overrides the package's own navigation-landmark name. In the `sidebar`
form the admin title and the account menu follow AppShell's region
mapping into the drawer paper's top and bottom rows; the package adds no
chrome of its own to either position.

The shell also publishes host-owned destinations and seams to its routed
content through package context: `invitationLink`, the acceptance-link
builder, `accountHref`, the host's account settings page,
`onImpersonationChange`, the identity-change action,
`onImpersonationStarting`, the attempt hook fired before an impersonation
start is written, and the route table the host supplied (pages resolve
sibling declarations by stable `id`). The step-up dialog renders
`accountHref` as its enrolment link when the host supplies it and text
only when it does not; the dialog never holds a host path. A release
returned by `onImpersonationStarting` runs when the start attempt does
not land (refused, taken over by the step-up gate, or thrown); a landed
start is the host's to release after its `onImpersonationChange('started')`
action settles.

`AdminProvider` installs those contexts without the frame and renders
no DOM. A host uses it when it wants a platform-staff frame of its own —
its own chrome, a dialog, a side panel — while keeping the admin pages'
seams available. It is not a tenant shell: it must not host tenant-facing
pages, and it carries no current-tenant affordance. `hasFeature` is
required, as on the shell; every other prop is optional.

The `OrganizationAdminProvider` is the organization surface's counterpart and
the one place this package accepts a current tenant. It installs the
organization contexts — the current tenant, the organization operations bundle,
the session, feature presence, the narrowed route table, the navigation
callback, the breadcrumb and the protected role names — and renders no
DOM, because the host has already rendered `@speed/product-shell`'s
`AppShell`. `tenant` is a required prop: the package never derives the
current tenant from the session or the URL, and it never switches it. The
`OrganizationAdminBreadcrumbProvider` publishes the organization breadcrumb on
its own, for a host that composes the organization pages without the full
provider; the shared pages' `PageHeader` prefers that context and falls
back to its own platform trail otherwise.

`hasFeature` is a required prop: the host's synchronous answer to "does
this process serve the named feature?", typed on the generated
`FeatureName`. The shell publishes it to the routed pages through the
package's feature context (`useFeature` inside the family's internal
plumbing), so a page can hide a feature-gated control without importing a
session package. The shell itself never asks it about its own routes: the
host has already narrowed the table it passed in.

## The page declarations

Each administration capability contributes declarations to the
declarative registration seam (`dashboardAdminRoutes`,
`tenantAdminRoutes`, `accountsAdminRoutes`, `sessionsAdminRoutes`,
`impersonationsAdminRoutes`, `permissionsAdminRoutes`,
`invitationsAdminRoutes`, `ssoAdminRoutes`, `settingsAdminRoutes`,
`smsAdminRoutes`, `auditAdminRoutes`, or the whole set as
`adminRoutes`). A capability may contribute more than one declaration:
a list page carries a navigation label and icon and a detail page (the
tenant detail, the audit detail) carries neither, so it is reachable from
a row without appearing in the navigation.

The `organizationAdminRoutes` table declares invitations and membership, SSO
providers, tenant roles and permissions, security and access, and the
organization profile.
Its declarations carry the tenant-domain permission codes
(`authn.membership.manage`, `authn.idp.manage`,
`authz.tenant.role.read`, `settings.tenant.read`, `tenant.profile.read`) and
the tenant-scoped features they need (`authn.admin` + `authn.invitations`,
`authn.admin` + `authn.admin.sso`, `authz.tenantadmin`,
`settings.tenantadmin`, `tenant.profile`). They never
name `tenant.admin`: the organization surface reads no roster and holds no
tenant selector. The write codes (`settings.tenant.write`,
`authz.tenant.role.manage`, `tenant.profile.write`) are not declaration permissions: a page
renders its write controls and lets the server refuse. A declaration
carries an optional stable `id` (the sibling-link address) — this
package's own declarations all carry one, a host's may omit it — a path
pattern, a view, an optional navigation label, an optional decorative
icon, the permission code the page's data is held to (`tenant.read`,
`authn.account.read`, `authn.session.read`,
`authn.impersonation.read`, `authz.role.read`, `authn.membership.manage`,
`authn.idp.manage`, `settings.read`, `sms.template.read`, `audit.read`)
and an optional `requires` list of generated `FeatureName`s the page
depends on. The shell never filters by `requires`: the host narrows the
table with its own presence predicate before passing it, and hands the
shell the required `hasFeature` answer (see "The second shell").
A list declaration's view carries the page's own matched path (`basePath`)
so the page links each row to `${basePath}/${id}`; a detail declaration's
view carries the id and the parent path (`parentHref`).
The shapes in `src/route.ts` are kept structurally identical to
`@speed/route-kit`'s registration surface so a declaration this package
exports is assignable to route-kit's `RouteDeclaration` **without
importing route-kit** — same-tier packages never import one another.

A declaration's label is a `AdminNavLabel` element, not a string: the
declarations stay plain module-level values (the seam wants a value)
while their labels resolve their text through the admin-ui namespace
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

**The organization lifecycle pair is the documented exception.** The
transfer-owner and delete-organization entries on the roster row and the
tenant detail render only for a caller whose attached platform permission
set holds `tenant.manage`. Neither action can complete for any other
caller, deletion is irreversible, and no other control on the page can
succeed for them; the entries are a visibility affordance, and the server
still judges both writes. The pair also never renders on a personal
organization (an identifier beginning with `personal-`) or on a tombstone.
The transfer dialog chooses its target path the same way: the
organization's member selector when the tenant permission set holds
`authn.membership.manage`, the platform account selector otherwise. That
choice is not a membership check — the endpoint still validates that the
target is an active member and answers 409, which the dialog states
inline.

## The operations seam (why the pages read through a bound bundle)

The pages read and write through the `AdminOperations` bundle the host
mounts in `AdminOperationsProvider` (`useAdminOperations`): a plain
object whose members are the react-query read hooks and write mutations
of the administration surfaces. The hooks run on the host's query client,
the mutations go through the same bound client, and no page builds a
query client of its own or writes a query key — the keys live inside the
hooks the host binds.

Two caller-session surfaces sit outside the bundle. The impersonation
page's own-session half calls the generated caller-session operations
directly (`useAuthnImpersonationStatus`, `useAuthnImpersonationStart`,
`useAuthnImpersonationEnd`), and the step-up dialog reads the caller's own
factors through `GET /api/v1/auth/factors`. Neither is an administration surface
the bundle substitutes: they act on the operator's own session, so they
run the generated hooks on the host's query client and are not
replaceable through the bundle.

Why the pages read through a bundle and not a direct `@speed/api-sdk`
call each: the bundle is the substitution point. The shipped
`adminOperations` is a thin adapter over the generated `@speed/api-sdk`
operations: the reads run the generated query options through `useQuery`
with a `select` that flattens the page envelope to the view rows (or
keeps the envelope when the page offers server-side pagination), and the
writes call the generated mutation functions and invalidate the list
through the generated key factories. A read whose route carries a
parameter (a tenant, a role, an account) takes that parameter, so its
generated query key namespaces by it; a read whose parameter is undefined
declares itself disabled rather than firing a malformed request. The audit
export is the bundle's one action rather than a hook: a page calls
`exportAuditRecords` once on a click, because mounting it as a query would
fire an export on render. The member is optional, so a host bundle that
predates the export keeps compiling, and a page renders no export control
when the member is absent.

What the pages reach through the bundle, grouped by surface:

| Members | Routes |
|---|---|
| `useTenants`, `useTenant`, `useCreateTenant`, `useSetTenantStatus`, `useSetTenantProfile`, `useTransferTenantOwner`, `useDeleteTenant` | `/api/v1/system/tenants[/:id]` (list / get / create), `/api/v1/system/tenants/:id/status`, `/api/v1/system/tenants/:id/profile`, `/api/v1/system/tenants/:id/owner`, `DELETE /api/v1/system/tenants/:id` |
| `usePermissions`, `useRoles`, `useWriteRole`, `useRemoveRole`, `useGrants`, `useGrantPermission`, `useRevokePermission`, `useBindings`, `useAccountBindings`, `useBindRole`, `useUnbindRole` | `/api/v1/system/permissions`, `/api/v1/system/tenants/:tenant/roles[/:role[/grants[/:permission]]]`, `/api/v1/system/tenants/:tenant/bindings[/:account[/:role]]` |
| `useAccounts`, `useAccount`, `useAccountFactors`, `useAccountSecrets`, `useAccountSessions`, `useCreateAccount`, `usePatchAccount`, `useSetAccountStatus`, `useSetMFARequired`, `useResetPassword`, `useAddIdentifier`, `useCreateSecret`, `useDeleteAccount`, `useRemoveIdentifier`, `useResetMFA`, `useRevokeAccountSessions`, `useRevokeSecret`, `useRevokeSession` | `/api/v1/system/auth/accounts[/:id[/factors|/secrets[/:secret]|/sessions|/identifiers/:identifier|/mfa-reset|/revoke-sessions|/status|/mfa-required|/password|/identifiers]]`, `/api/v1/system/auth/sessions/:id` |
| `useImpersonations`, `useRevokeImpersonation` | `/api/v1/system/tenants/{tenant}/impersonations[/:id]` |
| `useInvitations`, `useIssueInvitation`, `useRevokeInvitation`, `useMembers`, `useWriteMember`, `useSetMemberSsoExempt`, `useRemoveMember` | `/api/v1/tenants/:tenant/invitations[/:id]`, `/api/v1/tenants/:tenant/members[/:account[/sso-exempt]]` |
| `useSsoProviders`, `useSsoProvider`, `useCreateSsoProvider`, `useUpdateSsoProvider`, `useClaimSsoDomain`, `useDeleteSsoProvider`, `useRemoveSsoDomain`, `useVerifySsoDomain`, `useRefreshSsoMetadata`, `useReverifySsoDomains` | `/api/v1/tenants/:tenant/sso/providers`, `/api/v1/tenants/{tenant}/sso/providers/:id[/domains[/:domain[/verify]]|/metadata/refresh|/reverify-domains]` |
| `useSettings`, `useSetSetting`, `useRemoveSetting` | `/api/v1/system/settings[/:namespace[/:key]]` |
| `useSmsTemplates`, `useRegisterSmsTemplate`, `useOfferSmsTemplate`, `useSetSmsTemplateEnabled`, `useRemoveSmsTemplate` | `/api/v1/system/sms/templates[/submissions|/:name[/enabled]]` |
| `useAuditRecords`, `useAuditRecord`, `exportAuditRecords` | `/api/v1/system/audit/records[/:id]`, `/api/v1/system/audit/export` |

The organization surface reads through `OrganizationAdminOperations`:
`/api/v1/tenants/{tenant}/invitations` and `/members`, the tenant's SSO
providers, the tenant profile members (`useTenantProfile`,
`useTenantProfileWrite`), the tenant settings members
(`useTenantSettingsList`, `useTenantSettingsWrite`,
`useTenantSettingsRemove`) and the tenant
authorization members (`useTenantRoles`, `useTenantPermissions`,
`useTenantGrants`, `useTenantRoleBindings`, `useTenantBindings`,
`useTenantAccountBindings`, `useTenantWriteRole`, `useTenantRemoveRole`,
`useTenantGrantPermission`, `useTenantRevokePermission`, `useTenantBindRole`,
`useTenantUnbindRole`). The roles page reads the roles, the governed catalog,
the selected role's grants and its usage page, and writes roles, descriptions
and grants; the member panel reads and writes one account's bindings. The
tenant-wide `useTenantBindings` stays in the bundle for a host composing its
own page; no organization page renders it now.
The invitation, membership and SSO members are picked from the console
bundle's generated bindings; the settings and authorization members are
the tenant management modules' own generated operations, which the
console bundle does not carry. It holds no platform member and never
addresses `/api/v1/system`; the tenant is an argument of every read and
write. Its read keys take the tenant id as the outermost segment
(`tenantQueryKey` over the generated bare key), and its writes invalidate
through the same factory. `organizationAdminOperations` is the shipped
binding, `OrganizationAdminProvider`'s `operations` prop is the substitution
point, and the provider publishes the bundle through
`OrganizationAdminOperationsProvider`; a page reads it with
`useOrganizationAdminOperations`.

The view types are the generated response rows of each module's admin
surface, named for the admin's own pages; only the write inputs stay
hand-written, because they carry route parameters beside the body.

The platform settings route addresses an item by its namespace and **the
rest of the key under it** (`:key`), the full key being `<namespace>.<key>`
— and a module name may itself contain a dot (`authn.session`), so the
rest cannot be derived by splitting on the first dot.
`settingKeyRest(fullKey, namespace)` computes the rest. The tenant security
page receives only the full key and splits it at its last dot, the item
segment carrying no dot of its own.

`useTenantSettingsList` returns the surface's envelope
(`settings`, `writable`, `read_only_reason`) rather than a bare row array:
the wrapper page renders the two policies, and a protected tenant's answer
is read-only as a whole.

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

- **Two surfaces, two tenant rules.** The platform surface has no
  current-tenant switcher, no current-tenant-scoped query key, no
  `useCurrentTenant`, and no import of `@speed/tenancy-ui` or
  `@speed/product-shell`. The organization surface is tenant-scoped and takes
  the current tenant as the **required `tenant` prop of
  `OrganizationAdminProvider`**; it writes that tenant into every request
  path, never derives it from the session or URL, and never switches it.
  Neither surface holds a tenant switcher, and neither imports
  `@speed/tenancy-ui` or `@speed/product-shell`. The tenant selector on
  the platform surface's pages whose routes carry the tenant as a **path
  segment** names an argument of the request and is fed by the roster
  read; it is not the current tenant. A change that wants the package to
  derive, own or switch the current tenant is scope drift; a change that
  wants the platform surface to read it is scope drift too.
  Both surfaces depend on `@speed/auth-core` for the session, the
  step-up gate and the WebAuthn ceremony (the `@speed/auth-core/webauthn`
  subpath). The session is the `session` prop of `AdminShell` (and of
  `OrganizationAdminProvider`), published to the pages through
  `AdminSessionProvider`, with `session.isStepUpShortfall` as the gate's
  discriminator and `session.logout` for the challenge's re-login when
  this deployment cannot raise the session; the gate itself is auth-core's
  `useStepUpGate`, and the flow-step renderer runs the ceremony through
  the subpath. That is session and ceremony, not a tenant read;
  `useCurrentTenant` stays unused.
- **Shared units live in the lower packages.** The refresh control and
  mechanism (`RefreshButton`, `usePageRefresh`, `pageRefreshRead`),
  `httpStatusOf`, `CopyButton`, `OneTimeValuePanel` and
  `TotpEnrollmentPanel` come from `@speed/ui-kit`; the WebAuthn ceremony
  from `@speed/auth-core/webauthn` and the `otpauth://` parser from
  `@speed/auth-core/otpauth`; the step-up gate from `@speed/auth-core`
  (called with `session.isStepUpShortfall` as the discriminator); instant
  formatting from `@speed/i18n`'s `formatInstant`, fixed to UTC. This
  package keeps no copy of any of them.
- **The step renderer has two siblings.** `src/internal/flow-step.tsx` is
  one of three copies (`auth-ui`, `account-ui`, `admin-ui`) kept in
  lockstep by hand: a change to how a step renders lands in all three in
  the same change. This package hosts the workspace's source-level parity
  suite (`src/internal/flow-step-parity.test.ts`): it reads the three
  files, compares the rendered step kinds, the answer properties, the
  render primitives and the WebAuthn seam, and names the copy that drifts.
  It also holds every copy to the shared TOTP pieces -- `TotpEnrollmentPanel`
  from `@speed/ui-kit` and `parseOtpauth` from `@speed/auth-core/otpauth` --
  naming the copy and the missing piece, and scans `packages/` so the
  otpauth parser and the QR renderer keep one home: a local
  `parseOtpauth`/`formatSecretForDisplay` implementation, a second direct
  import of `qrcode-generator`, or `qrcode-generator` declared outside
  `ui-kit`'s manifest fails with the file and the reason named.
  A `webauthn` step renders the factor its options name: TOTP's
  `otpauth_uri` is parsed by `@speed/auth-core/otpauth` and rendered
  through ui-kit's `TotpEnrollmentPanel` as a scannable QR symbol with a
  manual-entry disclosure for the base32 secret and its parameters, beside
  a six-digit code field (the URI itself is never shown as text), while a
  passkey's public-key options drive the browser's WebAuthn ceremony
  (`@speed/auth-core/webauthn`) and the credential it returns is reported
  upward. All three start an assertion passkey ceremony on the step's
  arrival -- the entry was the click -- keying the arrival on the step
  number and the challenge so a re-sent challenge starts a fresh ceremony
  while an ordinary re-render does not, and start a registration ceremony
  on a click, because a creation ceremony needs the user activation some
  browsers require. Each ceremony failure keeps its own wording:
  `unsupported`, `cancelled` and `already-registered` (the browser's
  `InvalidStateError`, meaning the authenticator already holds a
  credential the server's `excludeCredentials` excludes) have their own
  sentences, and the generic failure sentence names the ceremony, so a
  failed registration does not read as a failed sign-in.
- **No routing in package code.** No path matching, no `react-router`. The
  shell links each labelled entry to its declaration's path; a page builds
  a row's detail link from its own matched path plus a row id, and a
  sibling link by resolving the published route table by declaration id.
  Matching a URL to a view is the application's route layer's.
  `onNavigate` is a host callback the frame invokes on a plain click — and
  publishes to its routed pages through an internal context, so a page's
  detail link routes in place too — not a routing mechanism this package
  owns.
- **The pages read and write only through the operations bundle.** No
  page constructs a `QueryClient`, and none writes a query key. A query
  key is whatever a generated hook carries; a read's parameter is the
  argument the generated key factory takes.
- **No second masking and no client-side authorization.** A sensitive
  setting's value arrives masked from the server and renders as-is; the
  permission a declaration carries is never a gate this package applies.
- **A read's failure branches on the answer.** A refused read — HTTP 403,
  or a bare 403 surfaced as the `client.http.403` code — renders the
  permission placeholder with no retry and replaces whatever content is on
  screen: a refusal makes the older data meaningless, so it never becomes a
  stale marker (`AdminAsyncSection` in `src/internal/async-section.tsx`).
  Any other failure over an already settled payload keeps the content and
  renders the stale marker above it — the admin's own `stale.description`,
  the section's retry label and callback; the page passes its read's
  fetching state as `refreshing`, so the marker withdraws while the retry
  runs and mounts anew when the retry fails again. Any other failure
  without a settled payload renders the retryable error placeholder. The
  branch reads the answer the server wrote, never a route-level permission
  read.
- **A control group's writes report through one feedback scope.** A page
  holds one `useWriteFeedback` per control group and renders the admin's
  `WriteFeedbackRegion` (`src/internal/write-feedback.tsx`) directly under
  it; the wrapper binds the admin-ui error-text resolver, so a failure
  code never renders as a raw key. A multi-request action — the settings
  batch save — is one attempt: every request is issued through
  `mutateAsync`, awaited together with `Promise.allSettled`, and reported
  once. A partial landing is a failure: the first failure in submission
  order is the reported code, landed items clear their drafts, failed items
  keep theirs, and the settings table moves to the page holding the first
  failed item. A destructive action confirmed through the shared
  pending-confirmation dialog closes the dialog on the confirming click and
  reports the outcome in the page's feedback area afterwards.
- **Every write goes through a generated operation.** No page hand-writes
  a `fetch` or an endpoint path; a request body the generated client does
  not send is not hand-written here — the fragment declares it or the
  control is not rendered.
- **Framework peers stay peers.** `react`, `react-dom`, `@mui/material`,
  `@emotion/*` and `@tanstack/react-query` are peer (required)
  dependencies; `i18n`, `ui-kit`, `layout-kit` and `api-sdk` are regular
  dependencies. The manifest is pinned by `src/package.json.test.ts`.
- **Every built-in string is a admin-ui namespace key.** The
  `speed/no-literal-text` rule (workspace config) enforces it over
  `src/`; the two bundles under `src/locales/` carry identical leaf key
  sets, pinned by `src/resources.test.ts` and
  `src/internal/error-text.test.ts`.
- **The error-text whitelist covers every module code a write can be
  answered with.** A admin write the server refuses surfaces as the
  module code the api-client derives from the refusal's RFC 9457 problem
  `type` (`<module>.<code>`, each dash of the code part normalized to an
  underscore); the whitelist carries one leaf per language for each
  reachable code, so a refusal renders its own text rather than the
  generic fallback. A new write surface extends the whitelist with the
  codes it can meet. The tenant settings write can be answered with 403
  `settings.protected`, 403 `settings.read_only`, 409
  `settings.tenant_admission_enforced` or 422 `settings.masked_value`; the
  whitelist carries those codes and both bundles carry their leaves, so
  each refusal renders its own text. An address an
  account create or identifier add collides with is the 409
  `authn.email_taken`, whose text names the address as already registered
  and asks for another -- an actionable refusal, not a restated status.
- **Each page opens with `AdminPageHeader`.** The page title is the
  level-one heading and a breadcrumb trail runs from the admin to the
  page. The component binds ui-kit's `PageHeader`, preferring the trail
  `OrganizationAdminBreadcrumbProvider` publishes on the organization
  surface and rendering the page's own platform trail otherwise. The
  organization trail starts at the current organization's name as plain
  text, not a link: the surface has no root page, so a link there could
  only go to the tenant home under the organization's name. A page
  states only its platform trail and never reads the context itself. The
  refresh action that re-reads the page's data (its `refetch`)
  sits in the header, except on a page whose body is addressed by a
  selector (the sessions page's account, and the tenant-addressed pages'
  tenant): there it sits in the selector's own row, immediately to the
  selector's right. The sections and `AsyncSection` placeholders continue
  from that `h1` (`headingLevel="h2"`), so the heading order never skips.
- **The refresh control is the same everywhere.** It is disabled while the
  scope it re-reads is empty and while a write in the page's scopes is in
  flight. The empty-scope state points `aria-describedby` at the page's
  existing "choose a scope first" text (and shows that text as its
  tooltip); the write-in-flight state carries neither, because the scope is
  chosen and a re-read would race the write. While its own read is in
  flight it stays enabled and focusable and reports `aria-busy` with a
  spinner, and a second activation issues no second request -- so an
  operator who pressed Enter keeps keyboard focus on it through the read.

## Testing

Unit tests are vitest + jsdom, one file per source under `src/`, shared
helpers only in `test-utils/`. `renderWithProviders` mounts the unit
under the tree a real host builds — `I18nextProvider` around `ui-kit`'s
`AppThemeProvider` around `QueryClientProvider` around
`AdminOperationsProvider` — with a fresh i18n instance (the admin-ui,
ui-kit and layout-kit namespaces registered, the last because the frame
renders `AppShell`'s built-in strings), a fresh no-retry query client, and
a caller-supplied operations bundle. `test-utils/operations.tsx` provides
that bundle: `createAdminOperations(backend)` builds real react-query
hooks over a scripted in-memory backend (`makeAdminBackend`), so the
pages exercise genuine react-query states — the first-load pending
branch, a settled empty answer, a failed read, and a write whose success
invalidates the list — with no HTTP transport. `test-utils/request-rig.ts`
binds a scripted request function into the api-sdk runtime seam, so the
shipped `adminOperations` can be driven endpoint by endpoint.

Suites: `AdminShell` (the landmarks, the `shellLayout` pass-through,
the labelled-only navigation, the
`onNavigate` seam, the skip link, the required `hasFeature` answer
reaching the pages' feature context, the page-tier axe scan); each page's
own suite (its reads, writes and failure branches, with an axe scan); the
`operations` suite (the shipped bundle's generated bindings for every
surface, driven over the request rig, plus the provider fallback and the
key-splitting helper); `routes` (the declarations' paths, permissions,
`requires` names, icons and translated labels); `resources`,
`internal/error-text`, `internal/datetime`,
`internal/async-section`, `internal/export-file` and
`internal/write-feedback` (bundle key parity, whitelist pairing, the
zero-instant sentinel, the export name and download handoff, the
403/retry/stale split, and the region's resolver binding);
`package.json` (the manifest contract); and `usage-example` (the README's
composition, with the operations bundle substituted from test-utils).

The organization-surface suite (`organization-surface.test.tsx`) covers
`organizationAdminRoutes` (paths, permission codes, `requires`),
`OrganizationAdminProvider` (the required tenant, the contexts it installs,
the no-DOM contract and the breadcrumb context), `organizationAdminOperations`
(the tenant-scoped members, including the tenant settings and
authorization ones), the wrapper exports, and the request record
of a full render of the invitations and SSO wrappers over the shipped
generated bundle: every URL those pages issue is recorded, none may target
`/api/v1/system`, every tenant-addressed read carries the current tenant,
and the platform roster is never read. The invitations
suite additionally covers tenant mode: the member write form absent, the
inviter identity resolved from the members read, the member account
column showing the recorded identifier, and an inviter that read cannot
resolve left marked. The security page's suite covers the two declared
policies only, the platform-enforced read-only switch, the two-step
confirmation before turning a policy off, optimistic save and rollback on
refusal, the protected tenant's switch-less read-only page, both
languages, and axe. The profile suite (`TenantProfilePage.test.tsx`)
covers the read-only identity card, the profile card's write, the
clearable display name's fallback to the alias, the host's
`onTenantUpdated` hand-over, and axe; `TenantProfileFields.test.ts`
covers the field bounds. The platform settings suite covers the platform
key/value table and its editable numeric inputs. The
roles suite (`OrganizationRolesPage.test.tsx`) covers the role table's
description and counts, creation, description editing, deletion, the
builtin/protected/no-manage read-only states, granting and revoking, the
server permission code driving revoke, grantable filtering, the usage
section's states and paging, the collapsed catalog, `can_manage=false`, the
members-page entry, and an axe scan. The platform `PermissionsPage.test.tsx`
covers the platform surface's bindings and account lookup, the grouped
catalog, the not-grantable mark and the grant picker's grantable filter, the
dangerous grant's two-step confirmation, and the grant's server-reported
permission code.

## Deferrals (recorded, do not re-open silently)

- **Batched account-identity resolution.** The account identity unit (an
  audit record's actor, an impersonation record's actor and target, an
  invitation's inviter, and the platform permissions page's binding account)
  resolves each distinct account id through
  `GET /api/v1/system/auth/accounts/:id`
  (react-query caches by id, so rows sharing an account share one read and a
  re-render reuses it), but a page of distinct accounts fires one read per
  account: no batch account-read surface exists. Resolving a page in one
  request needs a server surface this package does not have. The account
  read is gated on `authn.account.read`, which the page's own permission
  does not imply, so a viewer without it gets recorded ids marked
  unresolved rather than readable identities. The organization surface does
  not use this unit for its role data: the roles page's usage rows carry the
  display name and activity the server resolved through the host's
  `AccountIdentityReader`, and the member panel's binding accounts resolve
  from the members read.
- **Storybook.** No preview harness exists; the components are covered by
  jsdom tests + axe, and color-contrast is verified nowhere.
- **Generated-client freshness gate.** The generated client is committed:
  `web/packages/api-sdk/src/index.ts` for the platform surface and the
  demo's `src/app-api/index.ts` for the app's own surface. No check
  regenerates either from the OpenAPI contract and diffs the result, so a
  contract change that lands without a matching `api:gen` leaves this
  package bound to a stale shape and only a test that happens to touch the
  changed endpoint notices. A follow-up should add the
  regenerate-and-diff step to the frontend gate; it is not implemented in
  this batch.
