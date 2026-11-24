import type { RequestFn } from '@speed/api-client'
import type {
  AuditAdminRecord,
  AuditAdminRecordPage,
  AuthnAdminAccount,
  AuthnAdminAccountList,
  AuthnAdminAccountEnvelope,
  AuthnAdminImpersonation,
  AuthnAdminImpersonationList,
  AuthnAdminMember,
  AuthnAdminMemberList,
  AuthnAdminProvider,
  AuthnAdminProviderList,
  AuthnImpersonationGrantEnvelope,
  AuthnInviteInvitation,
  AuthnInvitePage,
  AuthnInviteView,
  AuthnMfaFactorList,
  AuthnProfileProfile,
  AuthnSessionList,
  AuthzAdminBindingView,
  AuthzAdminGrantView,
  AuthzAdminPermissionView,
  AuthzAdminRoleView,
  AuthzSelfPermissions200,
  AuthzTenantAdminBindingView,
  AuthzTenantAdminGrantView,
  AuthzTenantAdminPermissionView,
  AuthzTenantAdminRoleView,
  AuthzTenantadminBindingsList200,
  AuthzTenantadminBindingsOf200,
  AuthzTenantadminGrantsList200,
  AuthzTenantadminPermissionsList200,
  AuthzTenantadminRolesList200,
  NotificationInboxGetUnreadCount200,
  NotificationInboxListMessages200,
  NotificationMessage,
  NotificationType,
  MyTransferView,
  SettingsAdminSetting,
  SettingsTenantAdminSettings,
  SmsAdminTemplatesList200,
  TenantAdminTenant,
  TenantAdminTenantsGet200,
  TenantAdminTenantsList200,
  TenantProfileView,
  TenantSelfTenantsList200,
} from '@speed/api-sdk'

export type BackendMode = 'data' | 'empty' | 'error'

const NOT_FOUND = Symbol('scripted-not-found')

const INSTANT = '2026-01-01T00:00:00Z'


const TENANT: TenantAdminTenant = {
  name: 'acme',
  kind: 'business',
  status: 'active',
  contact_email: 'ops@acme.example',
  description: 'The canned organization the fixture answers with.',
  display_name: 'Acme Inc.',
  website: 'https://acme.example',
  owner_account: 'account-1',
  created_at: INSTANT,
}

const TENANT_PROFILE: TenantProfileView = {
  name: TENANT.name,
  kind: TENANT.kind,
  status: TENANT.status,
  contact_email: TENANT.contact_email,
  description: TENANT.description,
  display_name: TENANT.display_name,
  website: TENANT.website,
  owner_account: TENANT.owner_account,
  owner_display_name: 'Test User',
  owner_username: '',
  created_at: TENANT.created_at,
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

const MEMBER: AuthnAdminMember = {
  account: ACCOUNT.id,
  tenant: TENANT.name,
  active: true,
  display_name: 'Test User',
  username: 'test-user',
  sso_exempt: false,
  source: 'admin',
  roles: [],
  joined_at: '2026-10-15T12:00:00Z',
}

const INVITATION_ROW: AuthnInviteInvitation = {
  id: 'invite-1',
  tenant: TENANT.name,
  email: 'invitee@example.com',
  invited_by: ACCOUNT.id,
  status: 'pending',
  created_at: INSTANT,
  expires_at: '2099-01-01T00:00:00Z',
  accepted_at: null,
  revoked_at: null,
  last_sent_at: INSTANT,
  send_count: 1,
  state: 'pending',
  invited_by_name: ACCOUNT.display_name,
  invited_by_username: 'test-user',
}

const EXPIRED_INVITATION_ROW: AuthnInviteInvitation = {
  ...INVITATION_ROW,
  id: 'invite-expired',
  email: 'expired-invitee@example.com',
  state: 'expired',
  expires_at: '2020-01-01T00:00:00Z',
}

const ACCEPTED_INVITATION_ROW: AuthnInviteInvitation = {
  ...INVITATION_ROW,
  id: 'invite-accepted',
  email: 'accepted-invitee@example.com',
  status: 'accepted',
  state: 'accepted',
  accepted_at: '2026-06-01T12:00:00Z',
}

const REVOKED_INVITATION_ROW: AuthnInviteInvitation = {
  ...INVITATION_ROW,
  id: 'invite-revoked',
  email: 'revoked-invitee@example.com',
  status: 'revoked',
  state: 'revoked',
  revoked_at: '2026-06-01T12:00:00Z',
}

const INVITATION_ROWS: readonly AuthnInviteInvitation[] = [
  INVITATION_ROW,
  EXPIRED_INVITATION_ROW,
  ACCEPTED_INVITATION_ROW,
  REVOKED_INVITATION_ROW,
]

const SSO_PROVIDER: AuthnAdminProvider = {
  id: 'provider-1',
  tenant: TENANT.name,
  name: 'Example IdP',
  protocol: 'oidc',
  enabled: true,
  provisioning: 'jit',
  username_strategy: 'user-confirm',
  username_claim: '',
  allow_idp_initiated: false,
  allow_sha1: false,
  merge_existing: false,
  trust_idp_mfa: false,
  version: 1,
  domains: [],
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

const TENANT_ROLE: AuthzTenantAdminRoleView = {
  builtin: false,
  can_manage: true,
  created_at: INSTANT,
  description: '',
  permission_count: 0,
  member_count: null,
  role: 'support',
  tenant: TENANT.name,
}

const TENANT_PERMISSION: AuthzTenantAdminPermissionView = {
  dangerous: false,
  description: 'Read the tenant settings',
  grantable: true,
  group: '',
  permission: 'settings.tenant.read',
}

const TENANT_GRANT: AuthzTenantAdminGrantView = {
  action: 'read',
  created_at: INSTANT,
  permission: TENANT_PERMISSION.permission,
  resource: 'tenant.settings',
  role: TENANT_ROLE.role,
  tenant: TENANT.name,
}

const TENANT_BINDING: AuthzTenantAdminBindingView = {
  account: ACCOUNT.id,
  granted_at: INSTANT,
  role: TENANT_ROLE.role,
  tenant: TENANT.name,
}

const TENANT_SETTINGS: SettingsTenantAdminSettings = {
  settings: [
    {
      key: 'tenant.admission.require-mfa',
      title: 'Require multi-factor authentication',
      description: 'Members must sign in with a second factor.',
      type: 'bool',
      control: 'switch',
      danger: 'critical',
      effect: 'immediate',
      tenant_access: 'read-write',
      effective_value: 'true',
      platform_value: 'false',
      tenant_value: 'true',
      locked_by_platform: false,
      has_row: true,
      source: 'tenant',
    },
    {
      key: 'tenant.admission.require-sso',
      title: 'Require enterprise single sign-on',
      description:
        "Members must sign in through the organization's identity provider.",
      type: 'bool',
      control: 'switch',
      danger: 'critical',
      effect: 'immediate',
      tenant_access: 'read-write',
      effective_value: 'false',
      platform_value: 'false',
      tenant_value: 'false',
      locked_by_platform: false,
      has_row: false,
      source: 'default',
    },
  ],
  writable: true,
  read_only_reason: '',
}

const EMPTY_TENANT_SETTINGS: SettingsTenantAdminSettings = {
  settings: [],
  writable: true,
  read_only_reason: '',
}

const SMS_TEMPLATE: SmsAdminTemplatesList200['templates'][number] = {
  name: 'example',
  carrier: 'log',
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
  read_at: null,
  created_at: INSTANT,
}

const NOTIFICATION_TYPE: NotificationType = {
  key: 'example.created',
  group: 'example',
  default_channels: ['in_app'],
  unsubscribable: false,
  recipient_visible_params: [],
}

const MY_TRANSFER: MyTransferView = {
  id: 'transfer-1',
  tenant: TENANT.name,
  requested_at: INSTANT,
  expires_at: '2026-01-08T00:00:00Z',
  status: 'pending',
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
  verification: { state: 'pending', email: 'user@example.com', since: null },
}


const IMPERSONATION_GRANT: AuthnImpersonationGrantEnvelope = {
  grant: {
    id: 'grant-1',
    target: 'account-2',
    tenant: 'acme',
    started_at: INSTANT,
    ends_at: INSTANT,
  },
}

const INVITATION: AuthnInviteView = {
  state: 'pending',
  invitation: {
    tenant_display_name: TENANT.display_name,
    invited_by_name: 'Test User',
    email: 'i***@example.com',
    expires_at: INSTANT,
  },
}

const IMPERSONATION_RECORD: AuthnAdminImpersonation = {
  id: 'imp-1',
  actor: 'account-1',
  target: 'account-2',
  tenant: 'acme',
  session_id: 'session-1',
  started_at: INSTANT,
  ends_at: INSTANT,
  ended_at: null,
}

interface RouteAnswer {
  readonly data: unknown
  readonly empty: unknown
}

function answer(data: unknown, empty: unknown): RouteAnswer {
  return { data, empty }
}

export const EXACT: Readonly<Record<string, RouteAnswer>> = {
  '/api/v1/auth/me/tenants': answer(
    {
      tenants: [
        {
          name: TENANT.name,
          kind: 'business',
          display_name: TENANT.display_name,
        },
      ],
    } satisfies TenantSelfTenantsList200,
    {
      tenants: [
        {
          name: TENANT.name,
          kind: 'business',
          display_name: TENANT.display_name,
        },
      ],
    } satisfies TenantSelfTenantsList200,
  ),
  '/api/v1/authz/me/permissions': answer(
    {
      platform: [],
      tenant: [
        'authn.membership.manage',
        'authn.idp.manage',
        'authz.tenant.role.read',
        'settings.tenant.read',
        'tenant.profile.read',
      ],
    } satisfies AuthzSelfPermissions200,
    {
      platform: [],
      tenant: [
        'authn.membership.manage',
        'authn.idp.manage',
        'authz.tenant.role.read',
        'settings.tenant.read',
        'tenant.profile.read',
      ],
    } satisfies AuthzSelfPermissions200,
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
    { permissions: [{ permission: 'example.read', description: '', group: '', dangerous: false, grantable: true }] } satisfies {
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
  '/api/v1/tenants/acme/profile': answer(
    { profile: TENANT_PROFILE },
    { profile: TENANT_PROFILE },
  ),
  '/api/v1/auth/impersonation': answer(IMPERSONATION_GRANT, NOT_FOUND),
  '/api/v1/system/sms/templates': answer(
    { templates: [SMS_TEMPLATE], next_cursor: '' } satisfies SmsAdminTemplatesList200,
    { templates: [], next_cursor: '' } satisfies SmsAdminTemplatesList200,
  ),
  '/api/v1/auth/identifiers': answer(
    { state: 'pending', expires_in: 600, resend_after: 60 },
    { state: 'pending', expires_in: 600, resend_after: 60 },
  ),
  '/api/v1/auth/me': answer(PROFILE, {
    ...PROFILE,
    identifiers: [],
    links: [],
    verification: { state: 'none', email: '', since: null },
  } satisfies AuthnProfileProfile),
  '/api/v1/auth/me/owner-transfer-requests': answer(
    { requests: [MY_TRANSFER] },
    { requests: [] },
  ),
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
    /^\/api\/v1\/auth\/me\/owner-transfer-requests\/[^/]+$/,
    answer(
      { request: MY_TRANSFER },
      { request: { ...MY_TRANSFER, status: 'expired' } },
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/owner\/requests\/pending$/,
    answer({ request: null }, { request: null }),
  ],
  [
    /^\/api\/v1\/auth\/invitations\/[^/]+$/,
    answer(INVITATION, NOT_FOUND),
  ],
  [
    /^\/api\/v1\/auth\/invitations\/[^/]+\/eligibility$/,
    answer(
      { state: 'pending', email_proved: true },
      { state: 'pending', email_proved: true },
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/invitations$/,
    answer(
      {
        invitations: [...INVITATION_ROWS],
        next_cursor: '',
      } satisfies AuthnInvitePage,
      { invitations: [], next_cursor: '' } satisfies AuthnInvitePage,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/members$/,
    answer(
      { members: [MEMBER], next_cursor: '' } satisfies AuthnAdminMemberList,
      { members: [], next_cursor: '' } satisfies AuthnAdminMemberList,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/sso\/providers$/,
    answer({ providers: [SSO_PROVIDER] } satisfies AuthnAdminProviderList, {
      providers: [],
    } satisfies AuthnAdminProviderList),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/roles$/,
    answer({ roles: [TENANT_ROLE] } satisfies AuthzTenantadminRolesList200, {
      roles: [],
    } satisfies AuthzTenantadminRolesList200),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/permissions$/,
    answer(
      { permissions: [TENANT_PERMISSION] } satisfies AuthzTenantadminPermissionsList200,
      { permissions: [] } satisfies AuthzTenantadminPermissionsList200,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/roles\/[^/]+\/grants$/,
    answer({ grants: [TENANT_GRANT] } satisfies AuthzTenantadminGrantsList200, {
      grants: [],
    } satisfies AuthzTenantadminGrantsList200),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/bindings$/,
    answer(
      { bindings: [TENANT_BINDING] } satisfies AuthzTenantadminBindingsList200,
      { bindings: [] } satisfies AuthzTenantadminBindingsList200,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/bindings\/[^/]+$/,
    answer(
      { bindings: [TENANT_BINDING] } satisfies AuthzTenantadminBindingsOf200,
      { bindings: [] } satisfies AuthzTenantadminBindingsOf200,
    ),
  ],
  [
    /^\/api\/v1\/tenants\/[^/]+\/settings$/,
    answer(TENANT_SETTINGS, EMPTY_TENANT_SETTINGS),
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
