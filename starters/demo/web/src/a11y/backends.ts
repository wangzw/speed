/**
 * The route-level a11y sweep's scripted backends.
 *
 * Every page the sweep renders reads its data through the generated
 * @speed/api-sdk operations, which call the host's request function bound into
 * the runtime seam (bindRequestFn) -- the one seam both shells share. A sweep
 * that rendered with no bound function would meet the seam's fail-fast error
 * on every page and scan three identical error shells, so each combination is
 * rendered under a scripted function that answers the reads the routes fire:
 *
 *   - 'data'   -- the reads answer a small canned dataset, so each page renders
 *                 its populated state (the tables, the rows, the record and
 *                 account sections), not an empty shell.
 *   - 'empty'  -- the reads answer their empty envelopes, so each page renders
 *                 its own empty state.
 *   - 'error'  -- every read rejects, so each page renders its error state.
 *
 * The three are the empty / error / data states the sweep must reach; a page
 * that only ever scanned one of them would let a violation that lives in
 * another slip through. The canned rows are typed against the generated
 * response interfaces (@speed/api-sdk), so a drifting shape fails typecheck
 * rather than rendering a broken page the sweep would scan as a false sample.
 *
 * The endpoint set is the reads the routes fire on mount: the admin pages'
 * administration reads (the roster, the audit query, the accounts list, the
 * permissions catalog, the settings and SMS surfaces) and the tenant area's
 * (the profile, sessions and factors the account page mounts, the object list,
 * the notification centre) and the invitation landing's public token read. The
 * impersonations page also fires a caller-session
 * read on mount -- GET /api/v1/auth/impersonation, the operator's own impersonation
 * status -- which is scripted here too, in data mode as a grant in force and in
 * empty mode as the bare 404 the page reads as "not impersonating". Tenant-scoped
 * pages whose read waits on a tenant selector the sweep does not drive
 * (impersonations, invitations, SSO) still answer their selectors' roster read
 * and render their selector-plus-empty state; that is a real state, not a shell.
 */

import type { RequestFn } from '@speed/api-client'
import type {
  AuditAdminRecord,
  AuditAdminRecordPage,
  AuthnAdminAccount,
  AuthnAdminAccountList,
  AuthnAdminAccountEnvelope,
  AuthnAdminImpersonation,
  AuthnAdminImpersonationList,
  AuthnAdminMemberList,
  AuthnAdminProviderList,
  AuthnImpersonationGrantEnvelope,
  AuthnInvitePage,
  AuthnInviteView,
  AuthnMfaFactorList,
  AuthnProfileProfile,
  AuthnSessionList,
  AuthzAdminBindingView,
  AuthzAdminGrantView,
  AuthzAdminPermissionView,
  AuthzAdminRoleView,
  NotificationInboxGetUnreadCount200,
  NotificationInboxListMessages200,
  NotificationMessage,
  NotificationType,
  SettingsAdminSetting,
  SmsAdminTemplatesList200,
  TenantAdminTenant,
  TenantAdminTenantsGet200,
  TenantAdminTenantsList200,
  TenantSelfTenantsList200,
} from '@speed/api-sdk'

/** The three states a route is swept in. */
export type BackendMode = 'data' | 'empty' | 'error'

/**
 * A scripted refusal: the endpoint answers a bare 404 rather than a body. Some
 * reads' real "empty" state IS a refusal, not an empty envelope -- the
 * impersonation status read (GET /api/v1/auth/impersonation) answers 404 when the
 * caller has no impersonation in force, which the page reads as "not
 * impersonating". Scripted as a resolved body it would instead surface a
 * query error, so the endpoint's empty slot names this sentinel and the
 * scripted request function rejects with a 404-shaped error the page handles.
 */
const NOT_FOUND = Symbol('scripted-not-found')

/** An RFC 3339 instant, reused across the canned rows. */
const INSTANT = '2026-01-01T00:00:00Z'

/**
 * The zero instant (0001-01-01T00:00:00Z): the sentinel the administration
 * views read as "never" -- an impersonation still in force, an invitation not
 * yet accepted.
 */
const ZERO_INSTANT = '0001-01-01T00:00:00Z'

// --- The canned rows (data mode), typed against the generated shapes. -------

const TENANT: TenantAdminTenant = {
  id: 'acme',
  alias: 'Acme Inc.',
  status: 'active',
}

const ACCOUNT: AuthnAdminAccount = {
  id: 'account-1',
  kind: 'user',
  status: 'active',
  display_name: 'Test User',
  mfa_required: false,
  protected: false,
  password_set: true,
  attributes: {},
  identifiers: [],
  created_at: INSTANT,
}

const AUDIT_RECORD: AuditAdminRecord = {
  id: 'rec-1',
  tenant: 'acme',
  occurred_at: INSTANT,
  actor: 'account-1',
  source: 'test',
  action: 'login.create',
  object: 'session',
  outcome: 'success',
  purpose: '',
  detail: {},
}

const SETTING: SettingsAdminSetting = {
  key: 'example.key',
  namespace: 'example',
  type: 'string',
  description: 'An example setting',
  domain: '',
  sensitive: false,
  default: 'value',
  written: 'value',
  has_row: true,
  value: 'value',
}

const SMS_TEMPLATE: SmsAdminTemplatesList200['templates'][number] = {
  name: 'example',
  carrier: 'review',
  kind: 'notification',
  source: 'runtime',
  params: ['code'],
  locales: ['en-US'],
  bodies: {},
  attributes: {},
  approvals: {},
  enabled: true,
  version: 1,
  created_at: INSTANT,
  updated_at: INSTANT,
}

const NOTIFICATION_MESSAGE: NotificationMessage = {
  id: 'msg-1',
  type_key: 'example.created',
  group: 'example',
  title: 'An example notification',
  body: 'Something happened.',
  params: {},
  link: '/',
  created_at: INSTANT,
}

const NOTIFICATION_TYPE: NotificationType = {
  key: 'example.created',
  group: 'example',
  default_channels: ['in_app'],
  unsubscribable: false,
  recipient_visible_params: [],
}

const PROFILE: AuthnProfileProfile = {
  account_id: 'account-1',
  kind: 'user',
  display_name: 'Test User',
  mfa_required: false,
  password_set: true,
  created_at: INSTANT,
  attributes: {},
  color_mode: '',
  locale: 'en-US',
  time_zone: 'UTC',
  identifiers: [
    { id: 'id-1', kind: 'email', value: 'user@example.com', proven: true, contact: true, default_contact: true, sign_in: true, verified_at: INSTANT, verified_via: 'email-code' },
  ],
  links: [],
  verification: { state: 'pending', email: 'user@example.com', since: '' },
}

// --- The data-mode bodies, keyed by the endpoint they answer. ---------------

/**
 * The caller's own impersonation grant, in force: the body the status read
 * (GET /api/v1/auth/impersonation) answers when one is active. The page renders it as
 * the "current impersonation" surface (target, tenant, end instant, end
 * action) and hides the start form while it holds.
 */
const IMPERSONATION_GRANT: AuthnImpersonationGrantEnvelope = {
  grant: {
    id: 'grant-1',
    target: 'account-2',
    tenant: 'acme',
    started_at: INSTANT,
    ends_at: INSTANT,
  },
}

/**
 * The invitation a landing token points to, pending and unaccepted: the body
 * the accept screen's read (GET /api/v1/auth/invitations/:token) answers in
 * data mode. `accepted_at` is the zero sentinel, the value the screen reads as
 * "still redeemable".
 */
const INVITATION: AuthnInviteView = {
  invitation: {
    id: 'invite-1',
    tenant: 'acme',
    email: 'invitee@example.com',
    invited_by: 'account-1',
    status: 'pending',
    created_at: INSTANT,
    expires_at: INSTANT,
    accepted_at: ZERO_INSTANT,
  },
}

/**
 * One impersonation record, in force: `ended_at` is the zero sentinel the
 * views read as "still active", so the record row renders its state column as
 * in force and offers the terminate action.
 */
const IMPERSONATION_RECORD: AuthnAdminImpersonation = {
  id: 'imp-1',
  actor: 'account-1',
  target: 'account-2',
  tenant: 'acme',
  session_id: 'session-1',
  started_at: INSTANT,
  ends_at: INSTANT,
  ended_at: ZERO_INSTANT,
}

interface RouteAnswer {
  /** The body the endpoint answers in data mode. */
  readonly data: unknown
  /**
   * The body the endpoint answers in empty mode -- or NOT_FOUND when the
   * endpoint's real empty state is a bare 404 rather than an empty envelope.
   */
  readonly empty: unknown
}

function answer(data: unknown, empty: unknown): RouteAnswer {
  return { data, empty }
}

/**
 * The path-only endpoint answers (no path parameter).
 *
 * Every key is a full request path exactly as a generated operation issues it,
 * `/api/v1` prefix included. The prefix is part of the generated URL (see
 * `web/packages/api-sdk/src/index.ts`), so an entry written without it never
 * matches and the read falls through to `undefined` -- which react-query logs
 * as "Query data cannot be undefined" while the page renders an empty state
 * the sweep would scan as if it were real. The table is exported so
 * ./backends.test.ts can pin the prefix invariant on every entry.
 */
export const EXACT: Readonly<Record<string, RouteAnswer>> = {
  '/api/v1/auth/me/tenants': answer(
    {
      tenants: [
        { id: TENANT.id, alias: TENANT.alias },
      ],
    } satisfies TenantSelfTenantsList200,
    { tenants: [] } satisfies TenantSelfTenantsList200,
  ),
  '/api/v1/system/tenants': answer(
    { tenants: [TENANT] } satisfies TenantAdminTenantsList200,
    { tenants: [] } satisfies TenantAdminTenantsList200,
  ),
  '/api/v1/system/settings': answer(
    [SETTING] satisfies SettingsAdminSetting[],
    [] satisfies SettingsAdminSetting[],
  ),
  '/api/v1/system/audit/records': answer(
    { entries: [AUDIT_RECORD], next: '' } satisfies AuditAdminRecordPage,
    { entries: [], next: '' } satisfies AuditAdminRecordPage,
  ),
  '/api/v1/system/permissions': answer(
    { permissions: [{ permission: 'example.read', description: '' }] } satisfies {
      permissions: AuthzAdminPermissionView[]
    },
    { permissions: [] } satisfies { permissions: AuthzAdminPermissionView[] },
  ),
  '/api/v1/system/auth/accounts': answer(
    { accounts: [ACCOUNT], next_cursor: '' } satisfies AuthnAdminAccountList,
    { accounts: [], next_cursor: '' } satisfies AuthnAdminAccountList,
  ),
  '/api/v1/tenants/acme/impersonations': answer(
    { impersonations: [IMPERSONATION_RECORD], next_cursor: '' } satisfies AuthnAdminImpersonationList,
    { impersonations: [], next_cursor: '' } satisfies AuthnAdminImpersonationList,
  ),
  '/api/v1/auth/impersonation': answer(IMPERSONATION_GRANT, NOT_FOUND),
  '/api/v1/system/sms/templates': answer(
    { templates: [SMS_TEMPLATE], next_cursor: '' } satisfies SmsAdminTemplatesList200,
    { templates: [], next_cursor: '' } satisfies SmsAdminTemplatesList200,
  ),
  // The verification page's automatic code send. The scripted request
  // function keys on the URL alone, so the write is answered here too; no
  // read shares the path.
  '/api/v1/auth/identifiers': answer(
    { state: 'pending', expires_in: 600, resend_after: 60 },
    { state: 'pending', expires_in: 600, resend_after: 60 },
  ),
  '/api/v1/auth/me': answer(PROFILE, {
    ...PROFILE,
    identifiers: [],
    links: [],
    // The empty state has no self-registration contact: `none` renders no
    // verification section and no banner.
    verification: { state: 'none', email: '', since: '' },
  } satisfies AuthnProfileProfile),
  '/api/v1/auth/sessions': answer(
    {
      sessions: [
        {
          id: 'session-1',
          kind: 'browser',
          created_at: INSTANT,
          last_seen_at: INSTANT,
          expires_at: INSTANT,
          client_ip: '127.0.0.1',
          user_agent: 'test',
          current: true,
        },
      ],
    } satisfies AuthnSessionList,
    { sessions: [] } satisfies AuthnSessionList,
  ),
  '/api/v1/auth/factors': answer(
    {
      factors: [
        { id: 'factor-1', kind: 'totp', label: 'Authenticator', status: 'active', created_at: INSTANT },
      ],
    } satisfies AuthnMfaFactorList,
    { factors: [] } satisfies AuthnMfaFactorList,
  ),
  // The anonymous reset page's resume read. A visitor with no reset cookie is
  // answered a bare 404 flow-invalid, which the surface reads as "start
  // fresh"; this is that empty state, not a body.
  '/api/v1/auth/password-reset/current': answer(NOT_FOUND, NOT_FOUND),
  '/api/v1/notifications/messages': answer(
    { messages: [NOTIFICATION_MESSAGE], total: 1 } satisfies NotificationInboxListMessages200,
    { messages: [], total: 0 } satisfies NotificationInboxListMessages200,
  ),
  '/api/v1/notifications/messages/unread-count': answer(
    { unread: 1 } satisfies NotificationInboxGetUnreadCount200,
    { unread: 0 } satisfies NotificationInboxGetUnreadCount200,
  ),
  '/api/v1/notifications/types': answer(
    { types: [NOTIFICATION_TYPE] },
    { types: [] },
  ),
  '/api/v1/notifications/preferences': answer(
    { preferences: [] },
    { preferences: [] },
  ),
}

/**
 * Pattern answers, for endpoints whose path carries a segment.
 *
 * Like EXACT, every pattern anchors on the generated operation's full path,
 * `/api/v1` prefix included (see `web/packages/api-sdk/src/index.ts`); a
 * pattern written without the prefix never matches and the read falls through
 * to `undefined`. Every pattern is end-anchored, so a shorter path cannot
 * shadow a longer one that extends it; ./backends.test.ts also pins that no
 * entry answers another entry's sample URL.
 */
export const PATTERNS: readonly (readonly [RegExp, RouteAnswer])[] = [
  [
    /^\/api\/v1\/system\/tenants\/[^/]+$/,
    answer({ tenant: TENANT } satisfies TenantAdminTenantsGet200, {
      tenant: TENANT,
    } satisfies TenantAdminTenantsGet200),
  ],
  [
    /^\/api\/v1\/system\/audit\/records\/[^/]+$/,
    answer(AUDIT_RECORD satisfies AuditAdminRecord, AUDIT_RECORD satisfies AuditAdminRecord),
  ],
  [
    /^\/api\/v1\/system\/auth\/accounts\/[^/]+\/factors$/,
    answer({ factors: [] }, { factors: [] }),
  ],
  [
    /^\/api\/v1\/system\/auth\/accounts\/[^/]+\/secrets$/,
    answer({ secrets: [] }, { secrets: [] }),
  ],
  [
    /^\/api\/v1\/system\/auth\/accounts\/[^/]+\/sessions$/,
    answer({ sessions: [] }, { sessions: [] }),
  ],
  [
    /^\/api\/v1\/system\/auth\/accounts\/[^/]+$/,
    answer({ account: ACCOUNT } satisfies AuthnAdminAccountEnvelope, {
      account: ACCOUNT,
    } satisfies AuthnAdminAccountEnvelope),
  ],
  [
    /^\/api\/v1\/auth\/invitations\/[^/]+$/,
    // An unknown token is the screen's "no such invitation" state: a bare
    // 404, like the impersonation status read's empty slot.
    answer(INVITATION, NOT_FOUND),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/invitations$/,
    answer(
      { invitations: [], next_cursor: '' } satisfies AuthnInvitePage,
      { invitations: [], next_cursor: '' } satisfies AuthnInvitePage,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/members$/,
    answer({ members: [] } satisfies AuthnAdminMemberList, {
      members: [],
    } satisfies AuthnAdminMemberList),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/sso\/providers$/,
    answer({ providers: [] } satisfies AuthnAdminProviderList, {
      providers: [],
    } satisfies AuthnAdminProviderList),
  ],
  [
    /^\/api\/v1\/system\/tenants\/[^/]+\/roles\/[^/]+\/grants$/,
    answer({ grants: [] } as { grants: AuthzAdminGrantView[] }, {
      grants: [],
    } as { grants: AuthzAdminGrantView[] }),
  ],
  [
    /^\/api\/v1\/system\/tenants\/[^/]+\/roles$/,
    answer({ roles: [] } as { roles: AuthzAdminRoleView[] }, {
      roles: [],
    } as { roles: AuthzAdminRoleView[] }),
  ],
  [
    /^\/api\/v1\/system\/tenants\/[^/]+\/bindings$/,
    answer({ bindings: [] } as { bindings: AuthzAdminBindingView[] }, {
      bindings: [],
    } as { bindings: AuthzAdminBindingView[] }),
  ],
]

/**
 * The body one endpoint answers in the given mode, or undefined if unscripted,
 * or NOT_FOUND when the endpoint names a bare-404 empty state. Exported so
 * ./backends.test.ts can assert every table entry resolves.
 */
export function resolveBody(mode: 'data' | 'empty', url: string): unknown {
  const exact = EXACT[url]
  if (exact !== undefined) {
    return mode === 'data' ? exact.data : exact.empty
  }
  for (const [pattern, body] of PATTERNS) {
    if (pattern.test(url)) {
      return mode === 'data' ? body.data : body.empty
    }
  }
  return undefined
}

/**
 * The scripted request function for one mode. It is bound into the api-sdk
 * runtime seam (bindRequestFn) before a route renders, so every generated read
 * the page fires is answered from the table above. An endpoint whose answer is
 * NOT_FOUND rejects with a 404-shaped error, the refusal the page reads as
 * "no such resource".
 *
 * `onUnscripted` names a read the tables do not cover. The sweep passes one so
 * an unanswerable read fails the route instead of resolving `undefined` --
 * react-query would otherwise log "Query data cannot be undefined" and the
 * page would render, and be scanned in, a state its real backend never
 * produces. Error mode rejects every read before lookup and reports nothing.
 */
export function createBackend(
  mode: BackendMode,
  onUnscripted?: (url: string) => void,
): RequestFn {
  const requestFn = <T,>(url: string): Promise<T> => {
    if (mode === 'error') {
      return Promise.reject(new Error(`scripted backend: ${url} refused`))
    }
    const body = resolveBody(mode, url)
    if (body === undefined) {
      onUnscripted?.(url)
    }
    if (body === NOT_FOUND) {
      return Promise.reject(
        Object.assign(new Error(`scripted backend: ${url} not found`), {
          status: 404,
        }),
      )
    }
    return Promise.resolve(body as T)
  }
  return requestFn as RequestFn
}
