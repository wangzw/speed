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
 * The endpoint set is the reads the routes fire on mount: the console pages'
 * administration reads (the roster, the audit query, the accounts list, the
 * permissions catalog, the settings and SMS surfaces) and the tenant area's
 * (the profile, sessions and factors the account page mounts, the object list,
 * the notification centre). The impersonations page also fires a caller-session
 * read on mount -- GET /auth/impersonation, the operator's own impersonation
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
  AuthnMfaFactors200,
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
  ObjectstoreMetadataListObjects200,
  ObjectstoreObject,
  SettingsAdminSetting,
  SmsAdminTemplatesList200,
  TenantAdminTenant,
  TenantAdminTenantsGet200,
  TenantAdminTenantsList200,
} from '@speed/api-sdk'

/** The three states a route is swept in. */
export type BackendMode = 'data' | 'empty' | 'error'

/**
 * A scripted refusal: the endpoint answers a bare 404 rather than a body. Some
 * reads' real "empty" state IS a refusal, not an empty envelope -- the
 * impersonation status read (GET /auth/impersonation) answers 404 when the
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
  source: 'runtime',
  params: ['code'],
  locales: ['en-US'],
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
  params: null,
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

const OBJECT: ObjectstoreObject = {
  id: 'obj-1',
  name: 'example.txt',
  state: 'uploaded',
  size: 1024,
  mime: 'text/plain',
  checksum_sha256: 'a'.repeat(64),
  created_at: INSTANT,
  updated_at: INSTANT,
}

const PROFILE: AuthnProfileProfile = {
  account_id: 'account-1',
  kind: 'user',
  display_name: 'Test User',
  mfa_required: false,
  password_set: true,
  created_at: INSTANT,
  attributes: {},
  locale: 'en-US',
  time_zone: 'UTC',
  identifiers: [
    { id: 'id-1', kind: 'email', value: 'user@example.com', verified_at: INSTANT, verified_via: 'admin' },
  ],
  links: [],
}

// --- The data-mode bodies, keyed by the endpoint they answer. ---------------

/**
 * The caller's own impersonation grant, in force: the body the status read
 * (GET /auth/impersonation) answers when one is active. The page renders it as
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

/** The path-only endpoint answers (no path parameter). */
const EXACT: Readonly<Record<string, RouteAnswer>> = {
  '/tenant/admin/tenants': answer(
    { tenants: [TENANT] } satisfies TenantAdminTenantsList200,
    { tenants: [] } satisfies TenantAdminTenantsList200,
  ),
  '/settings/admin/settings': answer(
    [SETTING] satisfies SettingsAdminSetting[],
    [] satisfies SettingsAdminSetting[],
  ),
  '/audit/admin/records': answer(
    { entries: [AUDIT_RECORD], next: '' } satisfies AuditAdminRecordPage,
    { entries: [], next: '' } satisfies AuditAdminRecordPage,
  ),
  '/authz/admin/permissions': answer(
    { permissions: [{ permission: 'example.read' }] } satisfies {
      permissions: AuthzAdminPermissionView[]
    },
    { permissions: [] } satisfies { permissions: AuthzAdminPermissionView[] },
  ),
  '/auth/admin/accounts': answer(
    { accounts: [ACCOUNT], next_cursor: '' } satisfies AuthnAdminAccountList,
    { accounts: [], next_cursor: '' } satisfies AuthnAdminAccountList,
  ),
  '/auth/admin/impersonations': answer(
    { impersonations: [IMPERSONATION_RECORD], next_cursor: '' } satisfies AuthnAdminImpersonationList,
    { impersonations: [], next_cursor: '' } satisfies AuthnAdminImpersonationList,
  ),
  '/auth/impersonation': answer(IMPERSONATION_GRANT, NOT_FOUND),
  '/sms/admin/templates': answer(
    { templates: [SMS_TEMPLATE], next_cursor: '' } satisfies SmsAdminTemplatesList200,
    { templates: [], next_cursor: '' } satisfies SmsAdminTemplatesList200,
  ),
  '/auth/me': answer(PROFILE, {
    ...PROFILE,
    identifiers: [],
    links: [],
  } satisfies AuthnProfileProfile),
  '/auth/sessions': answer(
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
  '/auth/factors': answer(
    {
      factors: [
        { id: 'factor-1', kind: 'totp', label: 'Authenticator', status: 'active', created_at: INSTANT },
      ],
    } satisfies AuthnMfaFactors200,
    { factors: [] } satisfies AuthnMfaFactors200,
  ),
  '/api/v1/objects': answer(
    { objects: [OBJECT], next_cursor: '' } satisfies ObjectstoreMetadataListObjects200,
    { objects: [], next_cursor: '' } satisfies ObjectstoreMetadataListObjects200,
  ),
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

/** Pattern answers, for endpoints whose path carries a segment. */
const PATTERNS: readonly (readonly [RegExp, RouteAnswer])[] = [
  [
    /^\/tenant\/admin\/tenants\/[^/]+$/,
    answer({ tenant: TENANT } satisfies TenantAdminTenantsGet200, {
      tenant: TENANT,
    } satisfies TenantAdminTenantsGet200),
  ],
  [
    /^\/audit\/admin\/records\/[^/]+$/,
    answer(AUDIT_RECORD satisfies AuditAdminRecord, AUDIT_RECORD satisfies AuditAdminRecord),
  ],
  [
    /^\/auth\/admin\/accounts\/[^/]+\/factors$/,
    answer({ factors: [] }, { factors: [] }),
  ],
  [
    /^\/auth\/admin\/accounts\/[^/]+\/secrets$/,
    answer({ secrets: [] }, { secrets: [] }),
  ],
  [
    /^\/auth\/admin\/accounts\/[^/]+\/sessions$/,
    answer({ sessions: [] }, { sessions: [] }),
  ],
  [
    /^\/auth\/admin\/accounts\/[^/]+$/,
    answer({ account: ACCOUNT } satisfies AuthnAdminAccountEnvelope, {
      account: ACCOUNT,
    } satisfies AuthnAdminAccountEnvelope),
  ],
  [
    /^\/auth\/admin\/tenants\/[^/]+\/invitations$/,
    answer(
      { invitations: [], next_cursor: '' } satisfies AuthnInvitePage,
      { invitations: [], next_cursor: '' } satisfies AuthnInvitePage,
    ),
  ],
  [
    /^\/auth\/admin\/tenants\/[^/]+\/members$/,
    answer({ members: [] } satisfies AuthnAdminMemberList, {
      members: [],
    } satisfies AuthnAdminMemberList),
  ],
  [
    /^\/auth\/admin\/tenants\/[^/]+\/sso\/providers$/,
    answer({ providers: [] } satisfies AuthnAdminProviderList, {
      providers: [],
    } satisfies AuthnAdminProviderList),
  ],
  [
    /^\/auth\/admin\/sso\/providers$/,
    answer({ providers: [] } satisfies AuthnAdminProviderList, {
      providers: [],
    } satisfies AuthnAdminProviderList),
  ],
  [
    /^\/authz\/admin\/tenants\/[^/]+\/roles\/[^/]+\/grants$/,
    answer({ grants: [] } as { grants: AuthzAdminGrantView[] }, {
      grants: [],
    } as { grants: AuthzAdminGrantView[] }),
  ],
  [
    /^\/authz\/admin\/tenants\/[^/]+\/roles$/,
    answer({ roles: [] } as { roles: AuthzAdminRoleView[] }, {
      roles: [],
    } as { roles: AuthzAdminRoleView[] }),
  ],
  [
    /^\/authz\/admin\/tenants\/[^/]+\/bindings$/,
    answer({ bindings: [] } as { bindings: AuthzAdminBindingView[] }, {
      bindings: [],
    } as { bindings: AuthzAdminBindingView[] }),
  ],
]

/**
 * The body one endpoint answers in the given mode, or undefined if unscripted,
 * or NOT_FOUND when the endpoint names a bare-404 empty state.
 */
function resolveBody(mode: 'data' | 'empty', url: string): unknown {
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
 */
export function createBackend(mode: BackendMode): RequestFn {
  const requestFn = <T,>(url: string): Promise<T> => {
    if (mode === 'error') {
      return Promise.reject(new Error(`scripted backend: ${url} refused`))
    }
    const body = resolveBody(mode, url)
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
