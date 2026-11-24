import { ApiError, ERROR_CODE_PROTOCOL, isApiError } from '@speed/api-client'
import type { AccessTokenStore, RequestFn } from '@speed/api-client'
import {
  authnLoginAbandon,
  authnLoginAnswer,
  authnLoginCurrent,
  authnLoginMethods,
  authnLoginRegister,
  authnLoginStart,
  authnLoginStartAuthenticated,
  authnProfileGet,
  authnSessionGet,
  authnSessionLogout,
  authnSsoDiscover,
  authnTokenIssue,
  featureBoardList,
} from '@speed/api-sdk'
import type {
  AuthnLoginActionResult,
  AuthnLoginAnswerBody,
  AuthnLoginFactor,
  AuthnLoginField,
  AuthnLoginFlow,
  AuthnLoginHuman,
  AuthnLoginHumanGate,
  AuthnLoginMethods,
  AuthnLoginPasswordReset,
  AuthnLoginRegisterBody,
  AuthnLoginRegistration,
  AuthnLoginSession,
  AuthnLoginStartAuthenticatedBody,
  AuthnLoginStartBody,
  AuthnLoginStep,
  AuthnLoginToken,
  AuthnMethodDescriptor as AuthnMethodDescriptorModel,
  AuthnProfileIdentifier,
  AuthnProfileLink,
  AuthnProfileProfile,
  AuthnProfileVerification,
  AuthnSessionView as AuthnSessionViewModel,
  AuthnSsoProvider as AuthnSsoProviderModel,
  FeatureBoardDocument,
} from '@speed/api-sdk'

export type { AuthnLoginRegisterBody, AuthnLoginRegistration } from '@speed/api-sdk'

/**
 * The permission domain a host-attached permission set belongs to: `'tenant'`
 * holds permissions inside the tenant the request resolves in; `'system'`
 * holds platform-staff permissions independent of any tenant.
 */
export type AuthDomain = 'tenant' | 'system'

/**
 * The host-attached permission lists per domain; a null list means nothing is
 * known for that domain (checks fail closed). The session never evaluates a
 * list.
 */
export interface AuthPermissionSets {
  tenant: readonly string[] | null
  system: readonly string[] | null
}

/** The authenticated principal: the generated response model of GET /api/v1/auth/me. */
export type AuthnPrincipal = AuthnProfileProfile

/** One verified identifier of an account. */
export type AuthnIdentifier = AuthnProfileIdentifier

/** One external binding of an account. */
export type AuthnLink = AuthnProfileLink

/** One input a challenge step asks the client for. */
export type AuthnField = AuthnLoginField

/** One second-factor option a factor step offers. */
export type AuthnFactorOption = AuthnLoginFactor

/**
 * The next step of an open flow: the generated flat step narrowed by `kind`
 * into a discriminated union, so a view reads only the fields its step
 * carries.
 */
export type AuthnStep =
  | (AuthnLoginStep & { kind: 'challenge'; fields: AuthnLoginField[] })
  | (AuthnLoginStep & { kind: 'redirect'; url: string; method: string })
  | (AuthnLoginStep & { kind: 'factor'; factors: AuthnLoginFactor[] })
  | (AuthnLoginStep & { kind: 'enroll'; kinds: string[] })
  | (AuthnLoginStep & { kind: 'complete' })
  | (AuthnLoginStep & { kind: 'webauthn'; options: unknown })

type SessionCompletion = AuthnLoginSession & { state: 'authenticated' }

type TokenCompletion = AuthnLoginToken & { state: 'authenticated' }

type ElevatedCompletion = Omit<AuthnLoginToken, 'refresh_token'> & {
  state: 'elevated'
}

/**
 * The state of a flow as the wire carries it: an open flow (a sequence number
 * and the next step), a session or token completion, or a link/enroll action
 * result.
 */
export type AuthnFlowState =
  | (AuthnLoginFlow & { state: OpenFlowState; next: AuthnStep })
  | SessionCompletion
  | TokenCompletion
  | ElevatedCompletion
  | AuthnLoginActionResult

/** The login methods this deployment offers (GET /api/v1/auth/methods). */
export type AuthnMethodList = AuthnLoginMethods

/** The channel an anonymous password reset can use. */
export type PasswordResetChannel = 'email' | 'sms'

/**
 * The human-verification gate the discovery document reports once per surface;
 * an omitted gate reads as the off mechanism on both surfaces.
 */
export type AuthnHumanGate = AuthnLoginHuman

/** The human-verification mechanism one surface runs. */
export type AuthnHumanSurfaceGate = AuthnLoginHumanGate

/** One login method as the discovery endpoint reports it. */
export type AuthnMethodDescriptor = AuthnMethodDescriptorModel

/** One enterprise sign-in entry the email-domain discovery answers. */
export type AuthnSsoProvider = AuthnSsoProviderModel

/** Who is signed in on the session the request's cookie carries. */
export type AuthnSessionView = AuthnSessionViewModel

/** The observable session state; hosts read it, they never mutate it. */
export interface AuthSnapshot {
  state: 'anonymous' | 'authenticated'
  /** The authenticated principal, or null while anonymous. */
  principal: AuthnPrincipal | null
  /** Whether the current session carries an impersonation; only restore() writes it. */
  impersonating: boolean
  /** The host-attached permission lists per domain (see setPermissionSet). */
  permissionSets: AuthPermissionSets
  /** The feature document this process serves, as read for this session. */
  features: FeatureState
}

/**
 * The closed set of feature-state values: `none` (anonymous), `pending` (a
 * retry is in flight), `failed` (carries the error), and `ready` (carries the
 * served names).
 */
export type FeatureState =
  | { readonly state: 'none' }
  | { readonly state: 'pending' }
  | { readonly state: 'failed'; readonly error: unknown }
  | { readonly state: 'ready'; readonly names: ReadonlySet<string> }

/** Receives the new snapshot on every state change. */
export type AuthSessionListener = (snapshot: AuthSnapshot) => void

/** The session handle createAuthSession returns. */
export interface AuthSession {
  /** The current snapshot. */
  getSnapshot(): AuthSnapshot
  /** Subscribe to state changes; returns the unsubscribe function. */
  subscribe(listener: AuthSessionListener): () => void
  /** The login methods this deployment offers; rejects the raw ApiError on failure. */
  listMethods(): Promise<AuthnMethodList>
  /** The enterprise sign-in entries an email's domain maps to; an unmatched domain answers an empty list. */
  discoverSso(email: string): Promise<AuthnSsoProvider[]>
  /**
   * Opens an account (POST /api/v1/auth/register). Registration is not a
   * login: it establishes no session and leaves the snapshot untouched.
   */
  register(
    request: AuthnLoginRegisterBody,
  ): Promise<AuthnLoginRegistration>
  /**
   * Starts a `purpose: 'login'` flow and returns its first step. The flow is
   * always cookie-delivered, whatever `delivery` the request carries.
   */
  startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState>
  /** Starts a flow on the identity already signed in, for link, step-up or enroll. */
  startAuthenticatedFlow(
    request: AuthnLoginStartAuthenticatedBody,
  ): Promise<AuthnFlowState>
  /**
   * Answers the current step and returns the resulting flow state. This is the
   * one operation that commits the session.
   */
  answerFlow(request: AuthnLoginAnswerBody): Promise<AuthnFlowState>
  /** Reads the current step of the bound flow; null when there is no flow. */
  readFlow(): Promise<AuthnFlowState | null>
  /** Abandons the bound flow; a 401 (no binding or already gone) resolves. */
  abandonFlow(): Promise<void>
  /** Ends the session; a refused logout still clears locally and rejects the raw ApiError. */
  logout(): Promise<void>
  /**
   * Restores the session the request's cookie names: true when signed in, false
   * when nobody is, and a transport/server failure rethrows. An anonymous answer
   * is discarded while an identity transition is open.
   */
  restore(issue?: RequestFn): Promise<boolean>
  /**
   * Opens an identity transition around a credential replacement; the returned
   * release is idempotent, and concurrent windows are counted.
   */
  beginIdentityTransition(): () => void
  /**
   * Re-reads the feature document for the current session, landing as `ready`
   * or `failed` if the session is still the one the read started under.
   */
  retryFeatures(): Promise<void>
  /**
   * Silently refreshes the access token with the held refresh token. Resolves
   * false when there is nothing to refresh or the held token was refused; a
   * transport or server failure rethrows.
   */
  refresh(): Promise<boolean>
  /** Whether a failure is a step-up shortfall: a 401 while the session is still established. */
  isStepUpShortfall(error: unknown): boolean
  /** Replaces one domain's host-attached permission list with a defensive copy; null clears it. */
  setPermissionSet(domain: AuthDomain, perms: readonly string[] | null): void
  /** Replaces the principal's display name locally, without a request; a no-op while anonymous. */
  setDisplayName(name: string): void
}

/**
 * Rejects a session-establishing commit superseded by a concurrent commit that
 * landed first. A superseded token step-up has consumed its challenge, so only
 * a fresh flow can retry.
 */
export class OperationSupersededError extends Error {
  readonly snapshot: AuthSnapshot

  constructor(snapshot: AuthSnapshot) {
    super(
      'a concurrent operation committed to the session before this one settled',
    )
    this.name = 'OperationSupersededError'
    this.snapshot = snapshot
  }
}

/** Type guard for OperationSupersededError; also accepts a structurally shaped error from another realm. */
export function isOperationSuperseded(
  value: unknown,
): value is OperationSupersededError {
  if (value instanceof OperationSupersededError) {
    return true
  }
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Partial<OperationSupersededError>
  return (
    candidate.name === 'OperationSupersededError' &&
    typeof candidate.message === 'string' &&
    typeof candidate.snapshot === 'object' &&
    candidate.snapshot !== null
  )
}

interface IssuedTokens {
  accessToken: string
  refreshToken: string | null
}

function protocolViolation(detail: string): ApiError {
  return new ApiError({
    status: 200,
    code: ERROR_CODE_PROTOCOL,
    attempts: 1,
    cause: new Error(detail),
  })
}

function asObject(body: unknown, detail: string): Record<string, unknown> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    throw protocolViolation(detail)
  }
  return body as Record<string, unknown>
}

function requireString(value: unknown, detail: string): string {
  if (typeof value !== 'string' || value === '') {
    throw protocolViolation(detail)
  }
  return value
}

function requireNumber(value: unknown, detail: string): number {
  if (typeof value !== 'number' || Number.isNaN(value)) {
    throw protocolViolation(detail)
  }
  return value
}

const BEARER = 'Bearer' as const

function requireBearer(value: unknown, detail: string): typeof BEARER {
  if (value !== BEARER) {
    throw protocolViolation(detail)
  }
  return BEARER
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' ? value : undefined
}

function parseIdentifier(value: unknown): AuthnIdentifier {
  const rec = asObject(value, 'profile identifier is not an object')
  return {
    id: requireString(rec.id, 'profile identifier carries no id'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
    value: typeof rec.value === 'string' ? rec.value : '',
    proven: rec.proven === true,
    verified_at: typeof rec.verified_at === 'string' ? rec.verified_at : '',
    verified_via: typeof rec.verified_via === 'string' ? rec.verified_via : '',
    sign_in: rec.sign_in === true,
    contact: rec.contact === true,
    default_contact: rec.default_contact === true,
  }
}

function parseVerification(value: unknown): AuthnProfileVerification {
  const rec = typeof value === 'object' && value !== null ? value : {}
  const typed = rec as Record<string, unknown>
  return {
    state: typeof typed.state === 'string' ? typed.state : 'none',
    email: typeof typed.email === 'string' ? typed.email : '',
    since: typeof typed.since === 'string' ? typed.since : null,
  }
}

function parseLink(value: unknown): AuthnLink {
  const rec = asObject(value, 'profile link is not an object')
  const link: AuthnLink = {
    id: requireString(rec.id, 'profile link carries no id'),
    provider: typeof rec.provider === 'string' ? rec.provider : '',
    created_at: typeof rec.created_at === 'string' ? rec.created_at : null,
  }
  if (typeof rec.email === 'string') {
    link.email = rec.email
  }
  if (typeof rec.last_login_at === 'string') {
    link.last_login_at = rec.last_login_at
  }
  return link
}

function parseStringMap(value: unknown): Readonly<Record<string, string>> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {}
  }
  const out: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry === 'string') {
      out[key] = entry
    }
  }
  return out
}

function parsePrincipal(body: unknown): AuthnPrincipal {
  const rec = asObject(body, 'profile 2xx carries no body document')
  const identifiers = rec.identifiers
  const links = rec.links
  return {
    account_id: requireString(
      rec.account_id,
      'profile carries no account_id string',
    ),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
    display_name: typeof rec.display_name === 'string' ? rec.display_name : '',
    mfa_required: rec.mfa_required === true,
    password_set: rec.password_set === true,
    created_at: typeof rec.created_at === 'string' ? rec.created_at : null,
    attributes: parseStringMap(rec.attributes),
    color_mode: typeof rec.color_mode === 'string' ? rec.color_mode : '',
    locale: typeof rec.locale === 'string' ? rec.locale : '',
    time_zone: typeof rec.time_zone === 'string' ? rec.time_zone : '',
    identifiers: Array.isArray(identifiers)
      ? identifiers.map((entry) => parseIdentifier(entry))
      : [],
    links: Array.isArray(links) ? links.map((entry) => parseLink(entry)) : [],
    verification: parseVerification(rec.verification),
  }
}

function parseSessionView(body: unknown): AuthnSessionView {
  const rec = asObject(body, 'session 2xx carries no body document')
  if (typeof rec.authenticated !== 'boolean') {
    throw protocolViolation('session 2xx carries no authenticated boolean')
  }
  const view: AuthnSessionView = { authenticated: rec.authenticated }
  if (typeof rec.session_id === 'string') {
    view.session_id = rec.session_id
  }
  if (typeof rec.account_id === 'string') {
    view.account_id = rec.account_id
  }
  if (typeof rec.actor_id === 'string') {
    view.actor_id = rec.actor_id
  }
  if (typeof rec.impersonating === 'boolean') {
    view.impersonating = rec.impersonating
  }
  if (Array.isArray(rec.methods)) {
    view.methods = rec.methods.filter(
      (m): m is NonNullable<AuthnSessionView['methods']>[number] =>
        typeof m === 'string',
    )
  }
  if (
    rec.assurance === 'none' ||
    rec.assurance === 'single' ||
    rec.assurance === 'multi'
  ) {
    view.assurance = rec.assurance
  }
  if (typeof rec.auth_time === 'string') {
    view.auth_time = rec.auth_time
  }
  if (typeof rec.provider === 'string') {
    view.provider = rec.provider
  }
  return view
}

function parseField(value: unknown): AuthnField {
  const rec = asObject(value, 'challenge field is not an object')
  const field: AuthnField = {
    name: requireString(rec.name, 'challenge field carries no name'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
  }
  if (rec.required === true) {
    field.required = true
  }
  if (rec.secret === true) {
    field.secret = true
  }
  return field
}

function parseFactor(value: unknown): AuthnFactorOption {
  const rec = asObject(value, 'factor option is not an object')
  const factor: AuthnFactorOption = {
    id: requireString(rec.id, 'factor option carries no id'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
  }
  if (typeof rec.label === 'string') {
    factor.label = rec.label
  }
  return factor
}

function parseStep(body: unknown): AuthnStep {
  const rec = asObject(body, 'flow step carries no object')
  const kind = requireString(rec.kind, 'flow step carries no kind')
  switch (kind) {
    case 'challenge': {
      const fields = Array.isArray(rec.fields)
        ? rec.fields.map((entry) => parseField(entry))
        : []
      const step: AuthnStep = { kind: 'challenge', fields }
      if (
        typeof rec.provided_display_name === 'string' &&
        rec.provided_display_name !== ''
      ) {
        step.provided_display_name = rec.provided_display_name
      }
      if (
        typeof rec.provided_method === 'string' &&
        rec.provided_method !== ''
      ) {
        step.provided_method = rec.provided_method
      }
      if (typeof rec.provided_email === 'string' && rec.provided_email !== '') {
        step.provided_email = rec.provided_email
      }
      const expiresIn = optionalNumber(rec.expires_in)
      if (expiresIn !== undefined) {
        step.expires_in = expiresIn
      }
      const resendAfter = optionalNumber(rec.resend_after)
      if (resendAfter !== undefined) {
        step.resend_after = resendAfter
      }
      return step
    }
    case 'redirect':
      return {
        kind: 'redirect',
        url: requireString(rec.url, 'redirect step carries no url'),
        method: requireString(rec.method, 'redirect step carries no method'),
      }
    case 'factor':
      return {
        kind: 'factor',
        factors: Array.isArray(rec.factors)
          ? rec.factors.map((entry) => parseFactor(entry))
          : [],
      }
    case 'enroll':
      return {
        kind: 'enroll',
        kinds: Array.isArray(rec.kinds)
          ? rec.kinds.map((entry) =>
              requireString(entry, 'enroll kind is not a string'),
            )
          : [],
      }
    case 'complete':
      return { kind: 'complete' }
    case 'webauthn':
      return { kind: 'webauthn', options: rec.options }
    default:
      throw protocolViolation(`flow step carries an unknown kind ${kind}`)
  }
}

const OPEN_FLOW_STATES = [
  'first',
  'processing',
  'second',
  'enroll',
  'reprove',
  'ready',
  'signup',
] as const
type OpenFlowState = (typeof OPEN_FLOW_STATES)[number]

function isOpenFlowState(state: string): state is OpenFlowState {
  return (OPEN_FLOW_STATES as readonly string[]).includes(state)
}

function parseFlowState(body: unknown): AuthnFlowState {
  const rec = asObject(body, 'flow reply carries no body document')
  const state = requireString(rec.state, 'flow reply carries no state')
  switch (state) {
    case 'authenticated': {
      if (rec.session !== undefined) {
        const session = asObject(
          rec.session,
          'session completion carries no session document',
        )
        return {
          state: 'authenticated',
          session: {
            id: requireString(session.id, 'session completion carries no id'),
            expires_at:
              typeof session.expires_at === 'string' ? session.expires_at : '',
          },
        }
      }
      return {
        state: 'authenticated',
        token_type: requireBearer(
          rec.token_type,
          'login completion carries no Bearer token_type',
        ),
        access_token: requireString(
          rec.access_token,
          'login completion carries no access_token string',
        ),
        expires_in: requireNumber(
          rec.expires_in,
          'login completion carries no numeric expires_in',
        ),
        refresh_token: requireString(
          rec.refresh_token,
          'login completion carries no refresh_token string',
        ),
      }
    }
    case 'elevated':
      return {
        state: 'elevated',
        token_type: requireBearer(
          rec.token_type,
          'step-up completion carries no Bearer token_type',
        ),
        access_token: requireString(
          rec.access_token,
          'step-up completion carries no access_token string',
        ),
        expires_in: requireNumber(
          rec.expires_in,
          'step-up completion carries no numeric expires_in',
        ),
      }
    case 'linked':
      return { state: 'linked' }
    case 'enrolled':
      return { state: 'enrolled' }
    default: {
      if (!isOpenFlowState(state)) {
        throw protocolViolation(`flow reply carries an unknown state ${state}`)
      }
      return {
        seq: requireNumber(rec.seq, 'flow reply carries no numeric seq'),
        state,
        next: parseStep(rec.next),
      }
    }
  }
}

function isSessionCompletion(
  state: AuthnFlowState,
): state is SessionCompletion {
  return 'session' in state
}

function parseIssuedTokens(
  body: unknown,
  expectRefreshToken: boolean,
): IssuedTokens {
  const rec = asObject(body, 'token-issuing response carries no body document')
  const accessToken = requireString(
    rec.access_token,
    'token-issuing response carries no access_token string',
  )
  if (expectRefreshToken) {
    return {
      accessToken,
      refreshToken: requireString(
        rec.refresh_token,
        'token-issuing response carries no refresh_token string',
      ),
    }
  }
  const refreshToken = rec.refresh_token
  if (refreshToken === undefined) {
    return { accessToken, refreshToken: null }
  }
  return {
    accessToken,
    refreshToken: requireString(
      refreshToken,
      'token-issuing response carries a malformed refresh_token',
    ),
  }
}

function parseMethodDescriptor(value: unknown): AuthnMethodDescriptorModel {
  const rec = asObject(value, 'methods entry is not an object')
  const descriptor: AuthnMethodDescriptorModel = {
    name: requireString(rec.name, 'method descriptor carries no name'),
    kind: requireString(rec.kind, 'method descriptor carries no kind'),
  }
  if (typeof rec.protocol === 'string') {
    descriptor.protocol = rec.protocol
  }
  if (typeof rec.label === 'string') {
    descriptor.label = rec.label
  }
  if (rec.opens_account === true) {
    descriptor.opens_account = true
  }
  if (Array.isArray(rec.fields)) {
    descriptor.fields = rec.fields.map((entry) => parseField(entry))
  }
  if (Array.isArray(rec.binding_providers)) {
    descriptor.binding_providers = rec.binding_providers.map((entry) =>
      requireString(
        entry,
        'method descriptor carries a non-string binding provider',
      ),
    )
  }
  return descriptor
}

function humanGateOff(): AuthnHumanGate {
  return {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  }
}

function parseHumanSurfaceGate(
  value: unknown,
  surface: string,
): AuthnHumanSurfaceGate {
  const gate = asObject(value, `methods human ${surface} is not a document`)
  if (typeof gate.required !== 'boolean') {
    throw protocolViolation(
      `methods human ${surface} carries no required boolean`,
    )
  }
  const provider = requireString(
    gate.provider,
    `methods human ${surface} carries no provider string`,
  )
  const siteKey =
    gate.site_key === undefined
      ? undefined
      : requireString(
          gate.site_key,
          `methods human ${surface} site_key is not a string`,
        )
  const action =
    gate.action === undefined
      ? undefined
      : requireString(
          gate.action,
          `methods human ${surface} action is not a string`,
        )
  return {
    required: gate.required,
    provider,
    ...(siteKey === undefined ? {} : { site_key: siteKey }),
    ...(action === undefined ? {} : { action }),
  }
}

function parseHumanGate(value: unknown): AuthnHumanGate {
  if (value === undefined) {
    return humanGateOff()
  }
  const human = asObject(value, 'methods human is not a document')
  if (
    human.phone === undefined &&
    human.other === undefined &&
    typeof human.required === 'boolean' &&
    typeof human.provider === 'string'
  ) {
    const gate = parseHumanSurfaceGate(human, 'gate')
    return { phone: gate, other: gate }
  }
  return {
    phone: parseHumanSurfaceGate(human.phone, 'phone'),
    other: parseHumanSurfaceGate(human.other, 'other'),
  }
}

const PASSWORD_RESET_CLOSED: AuthnLoginPasswordReset = {
  enabled: false,
  methods: [],
}

function parsePasswordReset(value: unknown): AuthnLoginPasswordReset {
  if (value === undefined) {
    return { ...PASSWORD_RESET_CLOSED, methods: [] }
  }
  const reset = asObject(value, 'methods password_reset is not a document')
  if (typeof reset.enabled !== 'boolean') {
    throw protocolViolation('methods password_reset carries no enabled boolean')
  }
  if (!Array.isArray(reset.methods)) {
    throw protocolViolation('methods password_reset carries no methods array')
  }
  return {
    enabled: reset.enabled,
    methods: reset.methods.map((entry) => {
      const channel = requireString(
        entry,
        'methods password_reset carries a non-string channel',
      )
      if (channel !== 'email' && channel !== 'sms') {
        throw protocolViolation(
          `methods password_reset carries an unknown channel ${channel}`,
        )
      }
      return channel
    }),
  }
}

function parseMethodList(body: unknown): AuthnMethodList {
  const rec = asObject(body, 'methods 2xx carries no body document')
  if (!Array.isArray(rec.methods)) {
    throw protocolViolation('methods 2xx carries no methods array')
  }
  const signup = asObject(rec.signup, 'methods 2xx carries no signup document')
  if (typeof signup.enabled !== 'boolean') {
    throw protocolViolation('methods signup carries no enabled boolean')
  }
  const signupPassword = asObject(
    signup.password,
    'methods signup carries no password document',
  )
  if (typeof signupPassword.enabled !== 'boolean') {
    throw protocolViolation('methods signup password carries no enabled boolean')
  }
  const mfa = asObject(rec.mfa, 'methods 2xx carries no mfa document')
  if (!Array.isArray(mfa.factors)) {
    throw protocolViolation('methods mfa carries no factors array')
  }
  const human = parseHumanGate(rec.human)
  return {
    methods: rec.methods.map((entry) => parseMethodDescriptor(entry)),
    signup: {
      enabled: signup.enabled,
      password: { enabled: signupPassword.enabled },
    },
    mfa: {
      factors: mfa.factors.map((entry) =>
        requireString(entry, 'mfa factor is not a string'),
      ),
    },
    human,
    password_reset: parsePasswordReset(rec.password_reset),
  }
}

function parseSsoProviders(body: unknown): AuthnSsoProviderModel[] {
  const rec = asObject(body, 'sso discover 2xx carries no body document')
  if (!Array.isArray(rec.providers)) {
    throw protocolViolation('sso discover 2xx carries no providers array')
  }
  return rec.providers.map((entry) => {
    const provider = asObject(entry, 'sso provider entry is not an object')
    const protocol = requireString(
      provider.protocol,
      'sso provider carries no protocol',
    )
    if (protocol !== 'oidc' && protocol !== 'saml') {
      throw protocolViolation(
        `sso provider carries an unknown protocol ${protocol}`,
      )
    }
    return {
      id: requireString(provider.id, 'sso provider carries no id'),
      name: requireString(provider.name, 'sso provider carries no name'),
      protocol,
    }
  })
}

function parseRegistration(body: unknown): AuthnLoginRegistration {
  const rec = asObject(body, 'registration 2xx carries no body document')
  if (rec.state !== 'registered') {
    throw protocolViolation('registration 2xx carries no registered state')
  }
  const verification = asObject(
    rec.verification,
    'registration carries no verification document',
  )
  if (verification.state !== 'pending') {
    throw protocolViolation('registration verification is not pending')
  }
  return {
    state: 'registered',
    account_id: requireString(
      rec.account_id,
      'registration carries no account_id string',
    ),
    verification: {
      state: 'pending',
      email: requireString(
        verification.email,
        'registration verification carries no email string',
      ),
    },
  }
}

const NO_PERMISSION_SETS: AuthPermissionSets = {
  tenant: null,
  system: null,
}

const NO_FEATURES: FeatureState = { state: 'none' }

function parseFeatureDocument(body: unknown): ReadonlySet<string> {
  const rec = asObject(body, 'GET /api/v1/features answered no document')
  if (!Array.isArray(rec.features)) {
    throw protocolViolation('GET /api/v1/features carries no features array')
  }
  const names = new Set<string>()
  for (const entry of rec.features) {
    if (typeof entry !== 'string') {
      throw protocolViolation('GET /api/v1/features carries a non-string name')
    }
    names.add(entry)
  }
  return names
}

type FlowPurpose = 'login' | 'link' | 'step-up' | 'enroll'

function normalizePurpose(purpose: string | undefined): FlowPurpose {
  return purpose === 'link' || purpose === 'step-up' || purpose === 'enroll'
    ? purpose
    : 'login'
}

function isRefreshRefusal(error: unknown): boolean {
  if (!isApiError(error)) {
    return false
  }
  return (
    error.status === 400 ||
    error.status === 401 ||
    error.code === ERROR_CODE_PROTOCOL
  )
}

/**
 * Creates the session. The optional store is the bridge into api-client for a
 * token-delivered session; a host that leaves it out runs the cookie path.
 */
export function createAuthSession(store?: AccessTokenStore): AuthSession {
  let snapshot: AuthSnapshot = {
    state: 'anonymous',
    principal: null,
    impersonating: false,
    permissionSets: NO_PERMISSION_SETS,
    features: NO_FEATURES,
  }
  // The held refresh token: closure-only, never stored, never exposed.
  let refreshToken: string | null = null
  // The generation guard: a commit and a logout bump it; restore and refresh never do.
  let generation = 0
  // The identity-transition guard: open windows and a sequence bumped on every open.
  let openTransitions = 0
  let transitionSequence = 0
  // The purpose of the flow this session most recently advanced.
  let pendingPurpose: FlowPurpose = 'login'
  let refreshFlight: { held: string; promise: Promise<boolean> } | null = null
  // Concurrent retryFeatures() calls of the same session share one read, keyed on generation and account.
  let featuresFlight: {
    readonly generation: number
    readonly accountId: string
    readonly promise: Promise<void>
  } | null = null
  const listeners = new Set<AuthSessionListener>()

  function notify(): void {
    for (const listener of listeners) {
      try {
        listener(snapshot)
      } catch {
      }
    }
  }

  /**
   * Clears everything local. The caller decides whether the clear ends its
   * generation: logout bumps after clearing, while the restore/refresh-side
   * clears deliberately do not.
   */
  function clearLocal(): void {
    store?.set(null)
    refreshToken = null
    snapshot = {
      state: 'anonymous',
      principal: null,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features: NO_FEATURES,
    }
  }

  function commitTokens(issued: IssuedTokens): void {
    store?.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    if (snapshot.state !== 'authenticated') {
      snapshot = { ...snapshot, state: 'authenticated' }
    }
  }

  function commitLogin(
    issued: IssuedTokens,
    principal: AuthnPrincipal,
    features: FeatureState,
  ): void {
    store?.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    snapshot = {
      state: 'authenticated',
      principal,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features,
    }
  }

  function settledFeatures(
    result: PromiseSettledResult<unknown>,
  ): FeatureState {
    if (result.status === 'rejected') {
      return { state: 'failed', error: result.reason }
    }
    try {
      return { state: 'ready', names: parseFeatureDocument(result.value) }
    } catch (error) {
      return { state: 'failed', error }
    }
  }

  async function settleLogin(
    opGeneration: number,
    state: TokenCompletion,
  ): Promise<void> {
    const issued: IssuedTokens = {
      accessToken: state.access_token,
      refreshToken: state.refresh_token,
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    store?.set(issued.accessToken)
    const [profile, features] = await Promise.allSettled([
      authnProfileGet(),
      featureBoardList(),
    ])
    let principal: AuthnPrincipal
    try {
      if (profile.status === 'rejected') {
        throw profile.reason
      }
      principal = parsePrincipal(profile.value)
    } catch (error) {
      store?.set(null)
      refreshToken = null
      throw error
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    commitLogin(issued, principal, settledFeatures(features))
    generation += 1
    notify()
  }

  async function settleLoginSession(opGeneration: number): Promise<void> {
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    const [profile, features] = await Promise.allSettled([
      authnProfileGet(),
      featureBoardList(),
    ])
    if (profile.status === 'rejected') {
      throw profile.reason
    }
    const principal = parsePrincipal(profile.value)
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    snapshot = {
      state: 'authenticated',
      principal,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features: settledFeatures(features),
    }
    generation += 1
    notify()
  }

  function settleElevated(
    opGeneration: number,
    state: ElevatedCompletion,
  ): void {
    const issued: IssuedTokens = {
      accessToken: state.access_token,
      refreshToken: null,
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    commitTokens(issued)
    generation += 1
    notify()
  }

  function settleElevatedSession(opGeneration: number): void {
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    snapshot = {
      state: 'authenticated',
      principal: snapshot.principal,
      impersonating: snapshot.impersonating,
      permissionSets: snapshot.permissionSets,
      features: snapshot.features,
    }
    generation += 1
    notify()
  }

  async function runRefresh(
    held: string,
    capturedGeneration: number,
    capturedPrincipal: AuthnPrincipal | null,
  ): Promise<boolean> {
    let pair: unknown
    try {
      pair = await authnTokenIssue({
        grant_type: 'refresh_token',
        refresh_token: held,
      })
    } catch (error) {
      if (generation !== capturedGeneration) {
        return false
      }
      if (isRefreshRefusal(error)) {
        clearLocal()
        notify()
        return false
      }
      throw error
    }
    if (generation !== capturedGeneration) {
      if (refreshToken === held) {
        try {
          const rotated = parseIssuedTokens(pair, true)
          refreshToken = rotated.refreshToken
          const winner = snapshot.principal
          return (
            capturedPrincipal !== null &&
            winner !== null &&
            capturedPrincipal.account_id === winner.account_id
          )
        } catch {
        }
      }
      return false
    }
    let issued: IssuedTokens
    try {
      issued = parseIssuedTokens(pair, true)
    } catch {
      clearLocal()
      notify()
      return false
    }
    commitTokens(issued)
    notify()
    return true
  }

  return {
    getSnapshot(): AuthSnapshot {
      return snapshot
    },

    subscribe(listener: AuthSessionListener): () => void {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },

    async listMethods(): Promise<AuthnMethodList> {
      return parseMethodList(await authnLoginMethods())
    },

    async discoverSso(email: string): Promise<AuthnSsoProviderModel[]> {
      return parseSsoProviders(await authnSsoDiscover({ email }))
    },

    async register(
      request: AuthnLoginRegisterBody,
    ): Promise<AuthnLoginRegistration> {
      return parseRegistration(await authnLoginRegister(request))
    },

    async startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState> {
      pendingPurpose = normalizePurpose(request.purpose)
      return parseFlowState(
        await authnLoginStart({ ...request, delivery: 'session' }),
      )
    },

    async startAuthenticatedFlow(
      request: AuthnLoginStartAuthenticatedBody,
    ): Promise<AuthnFlowState> {
      pendingPurpose = normalizePurpose(request.purpose)
      return parseFlowState(await authnLoginStartAuthenticated(request))
    },

    async answerFlow(request: AuthnLoginAnswerBody): Promise<AuthnFlowState> {
      const opGeneration = generation
      const state = parseFlowState(await authnLoginAnswer(request))
      if (state.state === 'authenticated') {
        if (isSessionCompletion(state)) {
          if (pendingPurpose === 'step-up') {
            settleElevatedSession(opGeneration)
          } else {
            await settleLoginSession(opGeneration)
          }
        } else {
          await settleLogin(opGeneration, state)
        }
        pendingPurpose = 'login'
      } else if (state.state === 'elevated') {
        settleElevated(opGeneration, state)
        pendingPurpose = 'login'
      } else if (state.state === 'linked' || state.state === 'enrolled') {
        pendingPurpose = 'login'
      }
      return state
    },

    async readFlow(): Promise<AuthnFlowState | null> {
      try {
        return parseFlowState(await authnLoginCurrent())
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          return null
        }
        throw error
      }
    },

    async abandonFlow(): Promise<void> {
      try {
        await authnLoginAbandon()
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          return
        }
        throw error
      }
    },

    async logout(): Promise<void> {
      const opGeneration = generation
      const revokedSessionToken = refreshToken
      let failure: unknown
      let failed = false
      try {
        await authnSessionLogout()
      } catch (error) {
        failed = true
        failure = error
      }
      const replaced =
        generation !== opGeneration &&
        (revokedSessionToken === null || refreshToken !== revokedSessionToken)
      if (!replaced) {
        clearLocal()
        generation += 1
        notify()
      }
      if (failed) {
        throw failure
      }
    },

    async restore(issue?: RequestFn): Promise<boolean> {
      const opGeneration = generation
      const transitionAtEntry = openTransitions > 0
      const transitionSequenceAtEntry = transitionSequence
      const wasAuthenticated = snapshot.state === 'authenticated'
      const readSession = (): Promise<AuthnSessionViewModel> =>
        issue === undefined
          ? authnSessionGet()
          : issue<AuthnSessionViewModel>('/api/v1/auth/session')
      const readProfile = (): Promise<AuthnProfileProfile> =>
        issue === undefined
          ? authnProfileGet()
          : issue<AuthnProfileProfile>('/api/v1/auth/me')
      const readFeatures = (): Promise<FeatureBoardDocument> =>
        issue === undefined
          ? featureBoardList()
          : issue<FeatureBoardDocument>('/api/v1/features')
      let view: AuthnSessionView
      try {
        view = parseSessionView(await readSession())
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          view = { authenticated: false }
        } else {
          throw error
        }
      }
      if (generation !== opGeneration) {
        return false
      }
      if (!view.authenticated) {
        if (
          transitionAtEntry ||
          openTransitions > 0 ||
          transitionSequence !== transitionSequenceAtEntry
        ) {
          return false
        }
        if (snapshot.state !== 'anonymous') {
          clearLocal()
          notify()
        }
        return false
      }
      if (wasAuthenticated) {
        const principal = parsePrincipal(await readProfile())
        if (generation !== opGeneration) {
          return false
        }
        snapshot = {
          state: 'authenticated',
          principal,
          impersonating: view.impersonating === true,
          permissionSets: snapshot.permissionSets,
          features: snapshot.features,
        }
        notify()
        return true
      }
      const [profile, features] = await Promise.allSettled([
        readProfile(),
        readFeatures(),
      ])
      if (profile.status === 'rejected') {
        throw profile.reason
      }
      const principal = parsePrincipal(profile.value)
      if (generation !== opGeneration) {
        return false
      }
      snapshot = {
        state: 'authenticated',
        principal,
        impersonating: view.impersonating === true,
        permissionSets: snapshot.permissionSets,
        features: settledFeatures(features),
      }
      notify()
      return true
    },

    beginIdentityTransition(): () => void {
      openTransitions += 1
      transitionSequence += 1
      let released = false
      return () => {
        // Idempotent per release: a double release must not close someone else's window.
        if (released) {
          return
        }
        released = true
        openTransitions -= 1
      }
    },

    refresh(): Promise<boolean> {
      const held = refreshToken
      if (held === null) {
        return Promise.resolve(false)
      }
      const capturedGeneration = generation
      const capturedPrincipal = snapshot.principal
      const inFlight = refreshFlight
      if (inFlight !== null && inFlight.held === held) {
        // Keyed on the token, not the generation: a step-up keeps the held token, so a second presentation would read as theft.
        return inFlight.promise
      }
      const promise = runRefresh(held, capturedGeneration, capturedPrincipal)
      refreshFlight = { held, promise }
      const clearFlight = (): void => {
        if (refreshFlight?.promise === promise) {
          refreshFlight = null
        }
      }
      promise.then(clearFlight, clearFlight)
      return promise
    },

    isStepUpShortfall(error: unknown): boolean {
      return (
        isApiError(error) &&
        error.status === 401 &&
        snapshot.state === 'authenticated'
      )
    },

    retryFeatures(): Promise<void> {
      if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
        return Promise.resolve()
      }
      const capturedGeneration = generation
      const capturedAccountId = snapshot.principal.account_id
      if (
        featuresFlight !== null &&
        featuresFlight.generation === capturedGeneration &&
        featuresFlight.accountId === capturedAccountId
      ) {
        return featuresFlight.promise
      }
      const previous = snapshot.features
      const pending: FeatureState = { state: 'pending' }
      // Registered before `pending` is published, so a listener that re-enters finds this read.
      let settle!: () => void
      const promise = new Promise<void>((resolve) => {
        settle = resolve
      })
      const flight = {
        generation: capturedGeneration,
        accountId: capturedAccountId,
        promise,
      }
      featuresFlight = flight
      snapshot = { ...snapshot, features: pending }
      notify()
      const clearFlight = (): void => {
        if (featuresFlight === flight) {
          featuresFlight = null
        }
      }
      promise.then(clearFlight, clearFlight)
      void (async (): Promise<void> => {
        try {
          let next: FeatureState
          try {
            next = {
              state: 'ready',
              names: parseFeatureDocument(await featureBoardList()),
            }
          } catch (error) {
            next = { state: 'failed', error }
          }
          const applies =
            generation === capturedGeneration &&
            snapshot.state === 'authenticated' &&
            snapshot.principal !== null &&
            snapshot.principal.account_id === capturedAccountId
          if (!applies) {
            if (snapshot.features === pending) {
              snapshot = { ...snapshot, features: previous }
              notify()
            }
            return
          }
          snapshot = { ...snapshot, features: next }
          notify()
        } finally {
          settle()
        }
      })()
      return promise
    },

    setPermissionSet(
      domain: AuthDomain,
      perms: readonly string[] | null,
    ): void {
      const copy: readonly string[] | null = perms === null ? null : [...perms]
      const current = snapshot.permissionSets
      snapshot = {
        ...snapshot,
        permissionSets:
          domain === 'tenant'
            ? { tenant: copy, system: current.system }
            : { tenant: current.tenant, system: copy },
      }
      notify()
    },

    setDisplayName(name: string): void {
      if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
        return
      }
      snapshot = {
        ...snapshot,
        principal: { ...snapshot.principal, display_name: name },
      }
      notify()
    },
  }
}
