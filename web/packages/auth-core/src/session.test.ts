/**
 * session.test.ts -- the memory-only session state machine over the
 * generated authn flow surface, tested through the same seams a host
 * uses: a fake request function bound via bindRequestFn (the
 * @speed/api-sdk runtime seam) for the state-machine scenarios, and one
 * composition test running the real @speed/api-client against a
 * scripted fetch to prove the store bridge: a refused request retries
 * with the freshly refreshed token.
 *
 * Behaviour is asserted through the observable surface only: the
 * store's token, getSnapshot, subscriber notifications, the request
 * script's bodies and the raw ApiErrors a failed operation rejects. The
 * scripted harness itself lives in @speed/test-utils/session-harness,
 * shared with the other auth-core suites.
 */

import { describe, expect, it } from 'vitest'
import {
  createClient,
  createMemoryAccessTokenStore,
  ERROR_CODE_PROTOCOL,
  isApiError,
} from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { authnProfileGet } from '@speed/api-sdk'
import {
  apiError,
  authenticatedCompletion,
  captureRejection,
  completeFlow,
  elevatedCompletion,
  enrolledCompletion,
  linkedCompletion,
  LOGIN_ABANDON,
  LOGIN_ANSWER,
  LOGIN_CURRENT,
  LOGIN_METHODS,
  LOGIN_START,
  LOGIN_START_AUTHENTICATED,
  LOGOUT,
  makeCookieHarness,
  makeHarness,
  methodList,
  openFlow,
  principal,
  PROFILE_GET,
  SESSION_GET,
  sessionCompletion,
  sessionView,
  snapshotLog,
  SSO_DISCOVER,
  ssoProviders,
  TOKEN_ISSUE,
  tokenResponse,
} from '@speed/test-utils/session-harness'
import type { Harness } from '@speed/test-utils/session-harness'
import {
  createAuthSession,
  isOperationSuperseded,
  OperationSupersededError,
} from './session'
import type { AuthSnapshot } from './session'

async function expectProtocolViolation(
  promise: Promise<unknown>,
  harness: Harness,
): Promise<void> {
  const accessTokenBefore = harness.store.get()
  const snapshotBefore = harness.session.getSnapshot()
  const error = await captureRejection(promise)
  expect(isApiError(error)).toBe(true)
  if (!isApiError(error)) {
    return
  }
  expect(error.status).toBe(200)
  expect(error.code).toBe(ERROR_CODE_PROTOCOL)
  expect(harness.store.get()).toBe(accessTokenBefore)
  expect(harness.session.getSnapshot()).toEqual(snapshotBefore)
}

const ANONYMOUS = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

describe('initial state', () => {
  it('is anonymous with a null principal', () => {
    const { session, store } = makeHarness()
    expect(session.getSnapshot()).toEqual(ANONYMOUS)
    expect(store.get()).toBeNull()
  })

  it('notifies subscribers on every transition and stops after unsubscribe', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [LOGOUT]: () => undefined,
    })
    const seen = snapshotLog(harness.session)
    const gone: AuthSnapshot[] = []
    const unsubscribe = harness.session.subscribe((snapshot) => {
      gone.push(snapshot)
    })
    await harness.session.startFlow({})
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    unsubscribe()
    await harness.session.logout()
    expect(seen.map((snapshot) => snapshot.state)).toEqual([
      'authenticated',
      'anonymous',
    ])
    expect(gone.map((snapshot) => snapshot.state)).toEqual(['authenticated'])
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })
})

describe('list methods', () => {
  it('returns the deployment list the server answered, not a hard-coded set', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList(
          [
            'password',
            {
              name: 'wechat.web',
              kind: 'redirect',
              protocol: 'oauth2',
              binding_providers: ['wechat', 'wechat-openid:'],
            },
          ],
          { signup: true, signupPassword: true, mfaFactors: ['totp'] },
        ),
    })
    const methods = await harness.session.listMethods()
    expect(methods).toEqual({
      methods: [
        { name: 'password', kind: 'form' },
        {
          name: 'wechat.web',
          kind: 'redirect',
          protocol: 'oauth2',
          binding_providers: ['wechat', 'wechat-openid:'],
        },
      ],
      signup: { enabled: true, password: { enabled: true } },
      mfa: { factors: ['totp'] },
      human: { required: false, provider: 'off' },
      password_reset: { enabled: false, methods: [] },
    })
    expect(harness.calls).toHaveLength(1)
    expect(harness.calls[0]?.path).toBe('/api/v1/auth/methods')
  })

  it('refuses a methods 2xx whose binding providers are not strings', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [
          {
            name: 'wechat.web',
            kind: 'redirect',
            binding_providers: ['wechat', 1],
          },
        ],
        signup: { enabled: true, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
      }),
    })
    await expectProtocolViolation(harness.session.listMethods(), harness)
  })

  it('refuses a methods 2xx whose body is not the documented shape', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({ notMethods: true }),
    })
    await expectProtocolViolation(harness.session.listMethods(), harness)
  })

  it('refuses a methods 2xx that drops a required descriptor field', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [{ name: 'password' }],
        signup: { enabled: true, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
      }),
    })
    await expectProtocolViolation(harness.session.listMethods(), harness)
  })

  it('refuses a methods 2xx that drops the signup or mfa summary', async () => {
    const missingSignup = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
      }),
    })
    await expectProtocolViolation(missingSignup.session.listMethods(), missingSignup)
    const missingMFA = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        human: { required: false, provider: 'off' },
      }),
    })
    await expectProtocolViolation(missingMFA.session.listMethods(), missingMFA)
  })

  it('reads a methods 2xx that drops the human gate as off (an older server)', async () => {
    // A client newer than its server must not refuse every method: the
    // absent gate means the deployment predates it, which is the off
    // mechanism.
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
      }),
    })
    const methods = await harness.session.listMethods()
    expect(methods.human).toEqual({ required: false, provider: 'off' })
  })

  it('parses a required proof of work gate and a Turnstile gate', async () => {
    const pow = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: true, provider: 'pow' },
      }),
    })
    expect((await pow.session.listMethods()).human).toEqual({
      required: true,
      provider: 'pow',
    })
    const turnstile = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: true, provider: 'turnstile', site_key: 'site-1' },
      }),
    })
    expect((await turnstile.session.listMethods()).human).toEqual({
      required: true,
      provider: 'turnstile',
      site_key: 'site-1',
    })
  })

  it('refuses a methods 2xx whose human gate is not a document', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 7 },
      }),
    })
    await expectProtocolViolation(harness.session.listMethods(), harness)
  })

  it('refuses a methods 2xx whose signup password switch is not a boolean', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: true, password: { enabled: 'yes' } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
      }),
    })
    await expectProtocolViolation(harness.session.listMethods(), harness)
  })

  it('reads the password reset capability the deployment publishes', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          passwordReset: true,
          passwordResetMethods: ['email', 'sms'],
        }),
    })
    const methods = await harness.session.listMethods()
    expect(methods.password_reset).toEqual({
      enabled: true,
      methods: ['email', 'sms'],
    })
  })

  it('reads a methods 2xx that drops the password reset section as closed (an older server)', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
      }),
    })
    const methods = await harness.session.listMethods()
    expect(methods.password_reset).toEqual({ enabled: false, methods: [] })
  })

  it('refuses a password reset section that is malformed', async () => {
    const notADocument = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
        password_reset: 'email',
      }),
    })
    await expectProtocolViolation(notADocument.session.listMethods(), notADocument)

    const notABoolean = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
        password_reset: { enabled: 'yes', methods: [] },
      }),
    })
    await expectProtocolViolation(notABoolean.session.listMethods(), notABoolean)

    const noMethods = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
        password_reset: { enabled: true },
      }),
    })
    await expectProtocolViolation(noMethods.session.listMethods(), noMethods)

    const unknownChannel = makeHarness({
      [LOGIN_METHODS]: () => ({
        methods: [],
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
        password_reset: { enabled: true, methods: ['email', 'post'] },
      }),
    })
    await expectProtocolViolation(
      unknownChannel.session.listMethods(),
      unknownChannel,
    )
  })
})

describe('discover sso', () => {
  it('returns the entries the server answered for an email domain', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: (call) => {
        expect(call.options?.body).toEqual({ email: 'ada@acme.example' })
        return ssoProviders([
          { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
        ])
      },
    })
    const providers = await harness.session.discoverSso('ada@acme.example')
    expect(providers).toEqual([
      { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
    ])
    expect(harness.calls).toHaveLength(1)
    expect(harness.calls[0]?.path).toBe('/api/v1/auth/sso/discover')
  })

  it('resolves an empty list when nothing matches, never an error', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () => ssoProviders(),
    })
    await expect(harness.session.discoverSso('ada@nowhere.example')).resolves.toEqual(
      [],
    )
  })

  it('refuses a discover 2xx that is not the documented shape', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () => ({ notProviders: true }),
    })
    await expectProtocolViolation(
      harness.session.discoverSso('ada@acme.example'),
      harness,
    )
  })

  it('refuses a discover 2xx whose provider entry drops a field', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () => ({ providers: [{ id: 'idp-1' }] }),
    })
    await expectProtocolViolation(
      harness.session.discoverSso('ada@acme.example'),
      harness,
    )
  })
})

describe('register', () => {
  const REGISTER = 'POST /api/v1/auth/register'
  const registration = {
    state: 'registered',
    account_id: 'account-1',
    verification: { email: 'ada@example.com', state: 'pending' },
  } as const

  it('opens an account in one request and changes nothing in the session', async () => {
    const harness = makeHarness({
      [REGISTER]: (call) => {
        expect(call.options?.body).toEqual({
          username: 'ada',
          email: 'ada@example.com',
          password: 's3cret-pass',
        })
        return registration
      },
    })
    const log = snapshotLog(harness.session)
    const result = await harness.session.register({
      username: 'ada',
      email: 'ada@example.com',
      password: 's3cret-pass',
    })
    expect(result).toEqual(registration)
    expect(harness.calls).toHaveLength(1)
    expect(harness.calls[0]?.path).toBe('/api/v1/auth/register')
    // Registration is not a login: no snapshot transition, no token.
    expect(log).toEqual([])
    expect(harness.session.getSnapshot()).toEqual({
      state: 'anonymous',
      principal: null,
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'none' },
    })
    expect(harness.store.get()).toBeNull()
  })

  it('leaves an authenticated session and its credential untouched', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [REGISTER]: () => registration,
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    const before = harness.session.getSnapshot()
    const tokenBefore = harness.store.get()
    const log = snapshotLog(harness.session)

    await harness.session.register({
      username: 'ada',
      email: 'ada@example.com',
      password: 's3cret-pass',
    })

    // Same snapshot object, same token, no notification: nothing about the
    // session moved.
    expect(harness.session.getSnapshot()).toBe(before)
    expect(harness.store.get()).toBe(tokenBefore)
    expect(log).toEqual([])
  })

  it.each([
    ['an empty 2xx', undefined],
    ['a non-object body', 'registered'],
    ['a state other than registered', { ...registration, state: 'done' }],
    ['no account id', { state: 'registered', verification: registration.verification }],
    ['no verification document', { state: 'registered', account_id: 'account-1' }],
    [
      'a verification that is not pending',
      {
        state: 'registered',
        account_id: 'account-1',
        verification: { email: 'ada@example.com', state: 'verified' },
      },
    ],
    [
      'no verification email',
      {
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending' },
      },
    ],
  ])('refuses %s as client.protocol', async (_label, body) => {
    const harness = makeHarness({ [REGISTER]: () => body })
    await expectProtocolViolation(
      harness.session.register({
        username: 'ada',
        email: 'ada@example.com',
        password: 's3cret-pass',
      }),
      harness,
    )
  })
})

describe('start flow', () => {
  it('returns the parsed first step and changes nothing', async () => {
    const harness = makeHarness({
      [LOGIN_START]: (call) => {
        expect(call.options?.body).toEqual({
          method: 'password',
          purpose: 'login',
          delivery: 'session',
        })
        return completeFlow(1)
      },
    })
    const seen = snapshotLog(harness.session)
    const state = await harness.session.startFlow({
      method: 'password',
      purpose: 'login',
    })
    expect(state).toEqual({
      seq: 1,
      state: 'ready',
      next: { kind: 'complete' },
    })
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    expect(seen).toHaveLength(0)
  })

  it('parses a challenge step field by field', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'text', required: true }],
            expires_in: 300,
            resend_after: 60,
          },
        }),
    })
    const state = await harness.session.startFlow({ method: 'sms' })
    expect(state).toMatchObject({
      seq: 1,
      state: 'first',
      next: {
        kind: 'challenge',
        fields: [{ name: 'code', kind: 'text', required: true }],
        expires_in: 300,
        resend_after: 60,
      },
    })
  })

  it('refuses an empty 2xx as client.protocol before any state change', async () => {
    const harness = makeHarness({ [LOGIN_START]: () => undefined })
    await expectProtocolViolation(
      harness.session.startFlow({ method: 'password' }),
      harness,
    )
  })

  it('refuses a step whose kind is outside the closed set', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => ({ seq: 1, state: 'first', next: { kind: 'mystery' } }),
    })
    await expectProtocolViolation(
      harness.session.startFlow({ method: 'password' }),
      harness,
    )
  })
})

describe('start authenticated flow', () => {
  it('returns the step for a link / step-up / enroll purpose and changes nothing', async () => {
    const harness = makeHarness({
      [LOGIN_START_AUTHENTICATED]: (call) => {
        expect(call.options?.body).toEqual({ purpose: 'enroll' })
        return openFlow({
          state: 'enroll',
          next: { kind: 'enroll', kinds: ['totp', 'webauthn'] },
        })
      },
    })
    const state = await harness.session.startAuthenticatedFlow({
      purpose: 'enroll',
    })
    expect(state).toEqual({
      seq: 1,
      state: 'enroll',
      next: { kind: 'enroll', kinds: ['totp', 'webauthn'] },
    })
    expect(harness.store.get()).toBeNull()
  })
})

describe('answer flow', () => {
  it('returns an open step without touching the session', async () => {
    const harness = makeHarness({
      [LOGIN_ANSWER]: () =>
        openFlow({
          seq: 2,
          next: { kind: 'factor', factors: [{ id: 'f-1', kind: 'totp' }] },
        }),
    })
    const seen = snapshotLog(harness.session)
    const state = await harness.session.answerFlow({
      seq: 1,
      step: 'answer',
      input: { identifier: 'ada@example.com', password: 'pw' },
    })
    expect(state).toMatchObject({
      seq: 2,
      next: { kind: 'factor', factors: [{ id: 'f-1', kind: 'totp' }] },
    })
    expect(harness.store.get()).toBeNull()
    expect(seen).toHaveLength(0)
  })

  it('commits a login completion: writes the token, reads /me with it, and authenticates', async () => {
    const harness: Harness = makeHarness({
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      // The /me read must ride the freshly minted access token, so the
      // store holds it at the moment the profile is fetched.
      [PROFILE_GET]: () => {
        expect(harness.store.get()).toBe('access-1')
        return principal({ account_id: 'acct-9', display_name: 'Ada L.' })
      },
    })
    const seen = snapshotLog(harness.session)
    const state = await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(state.state).toBe('authenticated')
    expect(harness.store.get()).toBe('access-1')
    expect(harness.session.getSnapshot()).toEqual({
      state: 'authenticated',
      principal: principal({ account_id: 'acct-9', display_name: 'Ada L.' }),
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'ready', names: new Set() },
    })
    expect(seen.map((snapshot) => snapshot.state)).toEqual(['authenticated'])
    expect(harness.calls.map((call) => call.path)).toEqual([
      '/api/v1/auth/flows/answer',
      '/api/v1/auth/me',
      '/api/v1/features',
    ])
  })

  it('clears the store and rejects when the login completion cannot read /me', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => {
        throw refused
      },
    })
    const error = await captureRejection(
      harness.session.answerFlow({ seq: 2, step: 'complete' }),
    )
    expect(error).toBe(refused)
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('a step-up completion replaces the access token and keeps the principal, with no /me read', async () => {
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : elevatedCompletion()
      },
      [PROFILE_GET]: () => principal({ account_id: 'acct-9' }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const before = harness.session.getSnapshot()
    expect(before.principal?.account_id).toBe('acct-9')
    const callsBefore = harness.calls.length
    // A step-up flow: start on the authenticated identity, then complete.
    const elevated = await harness.session.answerFlow({
      seq: 3,
      step: 'complete',
    })
    expect(elevated.state).toBe('elevated')
    expect(harness.store.get()).toBe('access-elevated')
    // The principal stands and no /me read followed the elevation.
    expect(harness.session.getSnapshot().principal).toBe(before.principal)
    expect(harness.calls).toHaveLength(callsBefore + 1)
    expect(harness.calls.at(-1)?.path).toBe('/api/v1/auth/flows/answer')
  })

  it('a linked completion changes no login state', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => linkedCompletion(),
    })
    const seen = snapshotLog(harness.session)
    const state = await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(state).toEqual({ state: 'linked' })
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    expect(seen).toHaveLength(0)
  })

  it('an enrolled completion changes no login state', async () => {
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => enrolledCompletion(),
    })
    const state = await harness.session.answerFlow({ seq: 2, step: 'enroll' })
    expect(state).toEqual({ state: 'enrolled' })
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('refuses a login completion missing the refresh token before any state change', async () => {
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => ({
        state: 'authenticated',
        token_type: 'Bearer',
        access_token: 'access-1',
        expires_in: 3600,
      }),
    })
    await expectProtocolViolation(
      harness.session.answerFlow({ seq: 2, step: 'complete' }),
      harness,
    )
    // Nothing was written: no /me read either.
    expect(harness.calls.map((call) => call.path)).toEqual([
      '/api/v1/auth/flows/answer',
    ])
  })

  it('refuses an empty 2xx completion as client.protocol', async () => {
    const harness = makeHarness({ [LOGIN_ANSWER]: () => undefined })
    await expectProtocolViolation(
      harness.session.answerFlow({ seq: 2, step: 'complete' }),
      harness,
    )
  })
})

describe('read flow', () => {
  it('returns the current step so a page refresh can resume', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => openFlow({ seq: 3 }),
    })
    const state = await harness.session.readFlow()
    expect(state).toMatchObject({ seq: 3, state: 'first' })
  })

  it('resolves null when the flow binding is missing (a 401), never rejecting', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    await expect(harness.session.readFlow()).resolves.toBeNull()
  })

  it('rethrows a non-401 failure raw', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw refused
      },
    })
    await expect(harness.session.readFlow()).rejects.toBe(refused)
  })
})

describe('abandon flow', () => {
  it('resolves on the 204 of a removed flow', async () => {
    const harness = makeHarness({
      [LOGIN_ABANDON]: (call) => {
        expect(call.options?.method).toBe('DELETE')
      },
    })
    await expect(harness.session.abandonFlow()).resolves.toBeUndefined()
  })

  it('is idempotent: a 401 (the flow is already gone) resolves', async () => {
    const harness = makeHarness({
      [LOGIN_ABANDON]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    await expect(harness.session.abandonFlow()).resolves.toBeUndefined()
  })

  it('rethrows a non-401 failure raw', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeHarness({
      [LOGIN_ABANDON]: () => {
        throw refused
      },
    })
    await expect(harness.session.abandonFlow()).rejects.toBe(refused)
  })
})

async function signIn(harness: Harness): Promise<void> {
  await harness.session.startFlow({ method: 'password' })
  await harness.session.answerFlow({ seq: 2, step: 'complete' })
}

describe('logout', () => {
  it('ends the session: the store empties, the snapshot goes anonymous', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [LOGOUT]: (call) => {
        expect(call.options?.method).toBe('POST')
      },
    })
    await signIn(harness)
    await harness.session.logout()
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    await expect(harness.session.refresh()).resolves.toBe(false)
  })

  it('still clears locally and rejects raw when the revocation is refused', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [LOGOUT]: () => {
        throw refused
      },
    })
    await signIn(harness)
    const error = await captureRejection(harness.session.logout())
    expect(error).toBe(refused)
    // A session the server could not revoke is no longer trustworthy
    // here either: it is cleared regardless.
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })
})

describe('operation supersession', () => {
  it('rejects OperationSupersededError for a login completion that loses the race, never resolving with the winner', async () => {
    let releaseFirst!: (reply: unknown) => void
    let releaseSecond!: (reply: unknown) => void
    const firstGate = new Promise((resolve) => {
      releaseFirst = resolve
    })
    const secondGate = new Promise((resolve) => {
      releaseSecond = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1 ? firstGate : secondGate
      },
      [PROFILE_GET]: () => principal({ account_id: 'acct-1' }),
    })
    const seen = snapshotLog(harness.session)
    // Two concurrent completions, both capturing the same generation
    // synchronously before either answer resolves.
    const first = harness.session.answerFlow({ seq: 2, step: 'complete' })
    const second = harness.session.answerFlow({ seq: 2, step: 'complete' })

    releaseFirst(authenticatedCompletion({ access_token: 'access-first' }))
    await expect(first).resolves.toMatchObject({ state: 'authenticated' })
    expect(harness.store.get()).toBe('access-first')

    releaseSecond(authenticatedCompletion({ access_token: 'access-second' }))
    const error = await captureRejection(second)
    expect(isOperationSuperseded(error)).toBe(true)
    expect(isApiError(error)).toBe(false)
    if (isOperationSuperseded(error)) {
      expect(error.snapshot.principal?.account_id).toBe('acct-1')
    }
    // The loser's tokens were never applied, and it notified no one.
    expect(harness.store.get()).toBe('access-first')
    expect(seen).toHaveLength(1)
  })

  it('recognises a structurally-shaped superseded error from outside the class', () => {
    const winnerSnapshot: AuthSnapshot = {
      state: 'authenticated',
      principal: principal(),
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'ready', names: new Set() },
    }
    const duck = {
      name: 'OperationSupersededError',
      message:
        'a concurrent operation committed to the session before this one settled',
      snapshot: winnerSnapshot,
    }
    expect(isOperationSuperseded(duck)).toBe(true)
    expect(
      isOperationSuperseded(new OperationSupersededError(duck.snapshot)),
    ).toBe(true)
    expect(
      isOperationSuperseded({ name: 'OperationSupersededError', snapshot: {} }),
    ).toBe(false)
    expect(
      isOperationSuperseded({ name: 'OperationSupersededError', message: 'x' }),
    ).toBe(false)
    expect(isOperationSuperseded('OperationSupersededError')).toBe(false)
    expect(isOperationSuperseded(null)).toBe(false)
    expect(isOperationSuperseded(new Error('a concurrent operation'))).toBe(false)
  })
})

describe('refresh', () => {
  const authenticated = {
    [LOGIN_START]: () => completeFlow(),
    [LOGIN_ANSWER]: () => authenticatedCompletion(),
    [PROFILE_GET]: () => principal(),
  }

  it('resolves false without a request when no refresh token is held', async () => {
    const harness = makeHarness()
    const seen = snapshotLog(harness.session)
    await expect(harness.session.refresh()).resolves.toBe(false)
    expect(harness.calls).toHaveLength(0)
    expect(harness.store.get()).toBeNull()
    expect(seen).toHaveLength(0)
  })

  it('refreshes silently, rotates the held token and notifies', async () => {
    let refreshCount = 0
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: (call) => {
        refreshCount += 1
        const body = String(call.options?.body ?? '')
        expect(body).toContain('grant_type=refresh_token')
        expect(body).toContain(
          `refresh_token=${refreshCount === 1 ? 'refresh-1' : 'refresh-2'}`,
        )
        return tokenResponse({
          access_token: `access-${refreshCount + 1}`,
          refresh_token: `refresh-${refreshCount + 1}`,
        })
      },
    })
    await signIn(harness)
    await expect(harness.session.refresh()).resolves.toBe(true)
    expect(harness.store.get()).toBe('access-2')
    await expect(harness.session.refresh()).resolves.toBe(true)
    expect(harness.store.get()).toBe('access-3')
  })

  it('signs the session out when the server refuses the held token (400 invalid_grant)', async () => {
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () => {
        throw apiError(400, 'client.http.400')
      },
    })
    const seen = snapshotLog(harness.session)
    await signIn(harness)
    await expect(harness.session.refresh()).resolves.toBe(false)
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    expect(seen.map((snapshot) => snapshot.state)).toEqual([
      'authenticated',
      'anonymous',
    ])
  })

  it('signs the session out on a contract-violating 2xx', async () => {
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () => ({ access_token: 'access-2' }),
    })
    await signIn(harness)
    await expect(harness.session.refresh()).resolves.toBe(false)
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot().state).toBe('anonymous')
  })

  it('keeps the session tokens and rethrows raw on a server-side failure', async () => {
    const refused = apiError(503, 'client.http.503')
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () => {
        throw refused
      },
    })
    await signIn(harness)
    const error = await captureRejection(harness.session.refresh())
    expect(error).toBe(refused)
    expect(harness.store.get()).toBe('access-1')
    expect(harness.session.getSnapshot().state).toBe('authenticated')
  })

  it('keeps the session tokens and rethrows raw on a transport failure', async () => {
    const refused = apiError(0, 'client.network')
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () => {
        throw refused
      },
    })
    await signIn(harness)
    await expect(harness.session.refresh()).rejects.toBe(refused)
    expect(harness.store.get()).toBe('access-1')
  })

  it('shares one in-flight request between concurrent callers', async () => {
    let releaseRefresh!: () => void
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () =>
        refreshGate.then(() => tokenResponse({ access_token: 'access-2' })),
    })
    await signIn(harness)
    const first = harness.session.refresh()
    const second = harness.session.refresh()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/token'),
    ).toHaveLength(1)
    releaseRefresh()
    await expect(first).resolves.toBe(true)
    await expect(second).resolves.toBe(true)
    expect(harness.store.get()).toBe('access-2')
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/token'),
    ).toHaveLength(1)
  })

  it('cannot resurrect a session a logout ended while it was in flight', async () => {
    let releaseRefresh!: () => void
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    const harness = makeHarness({
      ...authenticated,
      [TOKEN_ISSUE]: () =>
        refreshGate.then(() => tokenResponse({ access_token: 'access-2' })),
      [LOGOUT]: () => undefined,
    })
    await signIn(harness)
    const refreshing = harness.session.refresh()
    await harness.session.logout()
    expect(harness.store.get()).toBeNull()
    releaseRefresh()
    await expect(refreshing).resolves.toBe(false)
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('cannot overwrite a session a newer login committed while it was in flight', async () => {
    let releaseRefresh!: () => void
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : authenticatedCompletion({
              access_token: 'access-new-login',
              refresh_token: 'refresh-new-login',
            })
      },
      [PROFILE_GET]: () => principal({ account_id: 'acct-new' }),
      [TOKEN_ISSUE]: () =>
        refreshGate.then(() => tokenResponse({ access_token: 'access-2' })),
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const refreshing = harness.session.refresh()
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.store.get()).toBe('access-new-login')
    releaseRefresh()
    await expect(refreshing).resolves.toBe(false)
    // The stale refresh lost the race: the newer login owns the store.
    expect(harness.store.get()).toBe('access-new-login')
    expect(harness.session.getSnapshot().principal?.account_id).toBe('acct-new')
  })

  it('adopts the rotated token when a step-up won the race, resolving true', async () => {
    let releaseRefresh!: () => void
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : elevatedCompletion()
      },
      [PROFILE_GET]: () => principal({ account_id: 'acct-1' }),
      [TOKEN_ISSUE]: (call) => {
        const presented = String(call.options?.body ?? '')
        if (!presented.includes('refresh-1') && !presented.includes('refresh-2')) {
          throw apiError(400, 'client.http.400')
        }
        return refreshGate.then(() =>
          tokenResponse({
            access_token: 'access-rotated',
            refresh_token: 'refresh-2',
          }),
        )
      },
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const refreshing = harness.session.refresh()
    // A step-up commits while the refresh is in flight: it keeps the
    // held token (it mints no new refresh token) and the principal.
    await harness.session.answerFlow({ seq: 3, step: 'complete' })
    expect(harness.store.get()).toBe('access-elevated')
    releaseRefresh()
    // True: the winner kept the principal this refresh spoke for, so a
    // silent-401 retry stays safe.
    await expect(refreshing).resolves.toBe(true)
    // The elevation stands; only the rotated refresh token was adopted,
    // so the session still refreshes.
    expect(harness.store.get()).toBe('access-elevated')
    await expect(harness.session.refresh()).resolves.toBe(true)
    expect(harness.store.get()).toBe('access-rotated')
  })

  it('leaves an in-flight refresh intact when a completion fails', async () => {
    let releaseRefresh!: () => void
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        if (answerCall === 2) {
          throw apiError(401, 'authn.login_failed')
        }
        return authenticatedCompletion()
      },
      [PROFILE_GET]: () => principal(),
      [TOKEN_ISSUE]: () =>
        refreshGate.then(() => tokenResponse({ access_token: 'access-2' })),
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const refreshing = harness.session.refresh()
    await expect(
      harness.session.answerFlow({ seq: 2, step: 'complete' }),
    ).rejects.toBeInstanceOf(Error)
    releaseRefresh()
    // The failed completion changed nothing, so the refresh still lands.
    await expect(refreshing).resolves.toBe(true)
    expect(harness.store.get()).toBe('access-2')
  })
})

describe('subscriber notification isolation', () => {
  it('contains a throwing subscriber: the others still hear the commit and the operation still resolves', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const seen: AuthSnapshot[] = []
    harness.session.subscribe(() => {
      throw new Error('host listener bug')
    })
    harness.session.subscribe((snapshot) => {
      seen.push(snapshot)
    })
    const state = await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(state.state).toBe('authenticated')
    expect(harness.store.get()).toBe('access-1')
    expect(seen).toHaveLength(1)
    expect(seen[0]?.state).toBe('authenticated')
  })
})

describe('host-attached permission sets', () => {
  const scripts = {
    [LOGIN_START]: () => completeFlow(),
    [LOGIN_ANSWER]: () => authenticatedCompletion(),
    [PROFILE_GET]: () => principal(),
  }

  it('replaces one domain, keeps the other, and notifies', async () => {
    const harness = makeHarness(scripts)
    await signIn(harness)
    const seen = snapshotLog(harness.session)
    harness.session.setPermissionSet('tenant', ['notes:read'])
    harness.session.setPermissionSet('system', ['users:manage'])
    harness.session.setPermissionSet('tenant', ['notes:read', 'notes:write'])
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: ['notes:read', 'notes:write'],
      system: ['users:manage'],
    })
    expect(seen).toHaveLength(3)
  })

  it('stores a defensive copy and clears a domain on null', async () => {
    const harness = makeHarness(scripts)
    await signIn(harness)
    const tenantPerms = ['notes:read']
    harness.session.setPermissionSet('tenant', tenantPerms)
    expect(harness.session.getSnapshot().permissionSets.tenant).not.toBe(
      tenantPerms,
    )
    tenantPerms.push('notes:write')
    expect(harness.session.getSnapshot().permissionSets.tenant).toEqual([
      'notes:read',
    ])
    harness.session.setPermissionSet('tenant', null)
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: null,
      system: null,
    })
  })

  it('a login clears both domains, even for the same account', async () => {
    const harness = makeHarness(scripts)
    await signIn(harness)
    harness.session.setPermissionSet('tenant', ['notes:read'])
    harness.session.setPermissionSet('system', ['users:manage'])
    await signIn(harness)
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: null,
      system: null,
    })
  })

  it('a silent refresh keeps both domains', async () => {
    const harness = makeHarness({
      ...scripts,
      [TOKEN_ISSUE]: () => tokenResponse({ access_token: 'access-2' }),
    })
    await signIn(harness)
    harness.session.setPermissionSet('tenant', ['notes:read'])
    harness.session.setPermissionSet('system', ['users:manage'])
    await expect(harness.session.refresh()).resolves.toBe(true)
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: ['notes:read'],
      system: ['users:manage'],
    })
  })

  it('a step-up keeps both domains', async () => {
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : elevatedCompletion()
      },
      [PROFILE_GET]: () => principal(),
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    harness.session.setPermissionSet('tenant', ['notes:read'])
    harness.session.setPermissionSet('system', ['users:manage'])
    await harness.session.answerFlow({ seq: 3, step: 'complete' })
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: ['notes:read'],
      system: ['users:manage'],
    })
  })
})

describe('response guards reject a contract-violating 2xx', () => {
  it.each([
    ['a non-object body', 'nope'],
    ['a null body', null],
    ['an array body', []],
    ['a body with no state', { seq: 1, next: { kind: 'complete' } }],
    [
      'an authenticated completion with no access token',
      {
        state: 'authenticated',
        token_type: 'Bearer',
        expires_in: 1,
        refresh_token: 'r',
      },
    ],
    [
      'an authenticated completion with a non-Bearer token type',
      {
        state: 'authenticated',
        token_type: 'MAC',
        access_token: 'a',
        expires_in: 1,
        refresh_token: 'r',
      },
    ],
    [
      'an authenticated completion with no expires_in',
      {
        state: 'authenticated',
        token_type: 'Bearer',
        access_token: 'a',
        refresh_token: 'r',
      },
    ],
    [
      'an elevated completion with no access token',
      { state: 'elevated', token_type: 'Bearer', expires_in: 1 },
    ],
    ['an open flow with no seq', { state: 'first', next: { kind: 'complete' } }],
    [
      'an open flow with a terminal state',
      { state: 'done', seq: 1, next: { kind: 'complete' } },
    ],
    ['a step with no kind', { seq: 1, state: 'first', next: {} }],
    [
      'a step with an unknown kind',
      { seq: 1, state: 'first', next: { kind: 'zap' } },
    ],
    [
      'a challenge field with no name',
      { seq: 1, state: 'first', next: { kind: 'challenge', fields: [{}] } },
    ],
    [
      'a factor option with no id',
      { seq: 1, state: 'second', next: { kind: 'factor', factors: [{}] } },
    ],
    [
      'an enroll kind that is not a string',
      { seq: 1, state: 'enroll', next: { kind: 'enroll', kinds: [7] } },
    ],
    [
      'a redirect step with no url',
      { seq: 1, state: 'first', next: { kind: 'redirect', method: 'GET' } },
    ],
    [
      'a redirect step with no method',
      { seq: 1, state: 'first', next: { kind: 'redirect', url: 'https://x' } },
    ],
  ])('refuses %s as client.protocol', async (_label, body) => {
    const harness = makeHarness({ [LOGIN_ANSWER]: () => body })
    await expectProtocolViolation(
      harness.session.answerFlow({ seq: 1, step: 'answer', input: {} }),
      harness,
    )
  })

  it.each([
    ['a non-object profile', 'x'],
    ['a profile with no account_id', { kind: 'person' }],
    ['a profile identifier with no id', { account_id: 'a', identifiers: [{}] }],
    ['a profile with a non-object identifier', { account_id: 'a', identifiers: [3] }],
    ['a profile link with no id', { account_id: 'a', links: [{}] }],
  ])('refuses %s on the /me read as client.protocol', async (_label, body) => {
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => body,
    })
    await expectProtocolViolation(
      harness.session.answerFlow({ seq: 2, step: 'complete' }),
      harness,
    )
  })

  it('parses a redirect step and a webauthn step', async () => {
    const redirect = makeHarness({
      [LOGIN_START]: () => ({
        seq: 1,
        state: 'first',
        next: { kind: 'redirect', url: 'https://idp.example/auth', method: 'GET' },
      }),
    })
    await expect(
      redirect.session.startFlow({ method: 'oidc' }),
    ).resolves.toEqual({
      seq: 1,
      state: 'first',
      next: { kind: 'redirect', url: 'https://idp.example/auth', method: 'GET' },
    })

    const webauthn = makeHarness({
      [LOGIN_START]: () => ({
        seq: 1,
        state: 'first',
        next: { kind: 'webauthn', options: { challenge: 'abc' } },
      }),
    })
    await expect(
      webauthn.session.startFlow({ method: 'passkey' }),
    ).resolves.toMatchObject({
      next: { kind: 'webauthn', options: { challenge: 'abc' } },
    })
  })

  it('carries a challenge step with no fields, and a ready state, without refusing them', async () => {
    const bare = makeHarness({
      [LOGIN_START]: () => ({ seq: 1, state: 'processing', next: { kind: 'challenge' } }),
    })
    await expect(bare.session.startFlow({ method: 'password' })).resolves.toEqual({
      seq: 1,
      state: 'processing',
      next: { kind: 'challenge', fields: [] },
    })
  })

  it('parses the profile identifiers, links and attribute set field by field', async () => {
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => ({
        account_id: 'acct-1',
        kind: 'person',
        display_name: 'Ada',
        mfa_required: true,
        password_set: false,
        created_at: '2026-01-01T00:00:00Z',
        // A non-string attribute is dropped, not carried.
        attributes: { team: 'core', size: 3 },
        locale: 'zh-CN',
        time_zone: 'Asia/Shanghai',
        identifiers: [
          {
            id: 'i-1',
            kind: 'email',
            value: 'ada@example.com',
            verified_at: '2026-01-02T00:00:00Z',
            verified_via: 'email',
            sign_in: true,
            contact: true,
            default_contact: false,
          },
          { id: 'i-2' },
        ],
        links: [
          {
            id: 'l-1',
            provider: 'google',
            email: 'ada@example.com',
            created_at: '2026-01-03T00:00:00Z',
            last_login_at: '2026-01-04T00:00:00Z',
          },
          { id: 'l-2' },
        ],
      }),
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const principalState = harness.session.getSnapshot().principal
    expect(principalState).toMatchObject({
      account_id: 'acct-1',
      mfa_required: true,
      password_set: false,
      attributes: { team: 'core' },
      identifiers: [
        {
          id: 'i-1',
          kind: 'email',
          value: 'ada@example.com',
          verified_at: '2026-01-02T00:00:00Z',
          verified_via: 'email',
          sign_in: true,
          contact: true,
          default_contact: false,
        },
        {
          id: 'i-2',
          kind: '',
          value: '',
          verified_at: '',
          verified_via: '',
          sign_in: false,
          contact: false,
          default_contact: false,
        },
      ],
      links: [
        {
          id: 'l-1',
          provider: 'google',
          email: 'ada@example.com',
          created_at: '2026-01-03T00:00:00Z',
          last_login_at: '2026-01-04T00:00:00Z',
        },
        { id: 'l-2', provider: '', created_at: '' },
      ],
    })
  })
})

describe('logout pre-emption', () => {
  it('does not clear a session a newer login committed while the logout was in flight', async () => {
    let releaseLogout!: () => void
    const logoutGate = new Promise<void>((resolve) => {
      releaseLogout = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : authenticatedCompletion({
              access_token: 'access-2',
              refresh_token: 'refresh-2',
            })
      },
      [PROFILE_GET]: () => principal({ account_id: 'acct-x' }),
      [LOGOUT]: () => logoutGate,
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const logoutRequest = harness.session.logout()
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.store.get()).toBe('access-2')
    releaseLogout()
    await logoutRequest
    // The logout revocation concerned the superseded session only: the
    // newer login owns the store and the snapshot.
    expect(harness.store.get()).toBe('access-2')
    expect(harness.session.getSnapshot().state).toBe('authenticated')
  })

  it('converges to signed-out when a same-session step-up pre-empts the logout', async () => {
    let releaseLogout!: () => void
    const logoutGate = new Promise<void>((resolve) => {
      releaseLogout = resolve
    })
    let answerCall = 0
    const harness = makeHarness({
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1
          ? authenticatedCompletion()
          : elevatedCompletion()
      },
      [PROFILE_GET]: () => principal(),
      [LOGOUT]: () => logoutGate,
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const seen = snapshotLog(harness.session)
    // The logout revokes the session whose held token is refresh-1; the
    // step-up that lands first keeps that very token, so it continues
    // the session the revocation is ending.
    const logoutRequest = harness.session.logout()
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.store.get()).toBe('access-elevated')
    releaseLogout()
    await logoutRequest
    // The user who clicked sign-out is signed out: the state the
    // pre-empting step-up committed spoke for the revoked session.
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    expect(seen.at(-1)?.state).toBe('anonymous')
  })
})

describe('cookie session (store-less)', () => {
  const cookieLogin = {
    [LOGIN_START]: () => completeFlow(),
    [LOGIN_ANSWER]: () => sessionCompletion(),
    [PROFILE_GET]: () => principal(),
    [SESSION_GET]: () => sessionView({ authenticated: true }),
  }

  it('commits a cookie login completion without writing a token store', async () => {
    const harness = makeCookieHarness(cookieLogin)
    const seen = snapshotLog(harness.session)
    await harness.session.startFlow({ method: 'password' })
    const state = await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(state).toEqual({
      state: 'authenticated',
      session: { id: 'session-1', expires_at: '2026-01-01T01:00:00Z' },
    })
    expect(harness.session.getSnapshot()).toEqual({
      state: 'authenticated',
      principal: principal(),
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'ready', names: new Set() },
    })
    // The cookie carries the credential: no access token was stored.
    expect(harness.store.get()).toBeNull()
    expect(seen.map((snapshot) => snapshot.state)).toEqual(['authenticated'])
    expect(harness.calls.map((call) => call.path)).toEqual([
      '/api/v1/auth/flows',
      '/api/v1/auth/flows/answer',
      '/api/v1/auth/me',
      '/api/v1/features',
    ])
  })

  it('forces delivery to session whatever the caller asked for', async () => {
    const harness = makeCookieHarness({
      [LOGIN_START]: (call) => {
        expect(call.options?.body).toEqual({
          method: 'password',
          delivery: 'session',
        })
        return completeFlow()
      },
    })
    await harness.session.startFlow({ method: 'password', delivery: 'token' })
  })

  it('keeps a reload logged in: a fresh session restores from the cookie', async () => {
    // The bug this session model fixes: a page reload once signed the
    // user out, because the browser path held its credential in memory.
    // A fresh session (a reload) reads GET /api/v1/auth/session and, with the
    // cookie still valid, re-establishes the authenticated snapshot.
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true }),
      [PROFILE_GET]: () =>
        principal({ account_id: 'acct-reload', display_name: 'Ada Reload' }),
    })
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot()).toEqual({
      state: 'authenticated',
      principal: principal({
        account_id: 'acct-reload',
        display_name: 'Ada Reload',
      }),
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'ready', names: new Set() },
    })
    expect(harness.calls.map((call) => call.path)).toEqual([
      '/api/v1/auth/session',
      '/api/v1/auth/me',
      '/api/v1/features',
    ])
  })

  it('restore() carries the impersonating flag from the session answer', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () =>
        sessionView({ authenticated: true, impersonating: true }),
      [PROFILE_GET]: () => principal(),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(true)
    // The snapshot carries no initiator identity: the principal is the
    // impersonated account, as GET /api/v1/auth/me answered it.
    expect(harness.session.getSnapshot().principal).toEqual(principal())
  })

  it('restore() reports impersonating false when the answer omits the field', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true }),
      [PROFILE_GET]: () => principal(),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(false)
  })

  it('a login completion and an anonymous session carry impersonating false', async () => {
    const harness = makeCookieHarness(cookieLogin)
    expect(harness.session.getSnapshot().impersonating).toBe(false)
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.session.getSnapshot().impersonating).toBe(false)
  })

  it('a later restore updates impersonating as the impersonation starts and ends', async () => {
    let impersonating = false
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true, impersonating }),
      [PROFILE_GET]: () => principal(),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(false)

    // The host starts an impersonation server-side and re-reads.
    impersonating = true
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(true)

    // The host ends it and re-reads: the flag follows the server.
    impersonating = false
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(false)
  })

  it('a step-up continuing the session keeps the impersonating flag', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () =>
        sessionView({ authenticated: true, impersonating: true }),
      [PROFILE_GET]: () => principal(),
      [LOGIN_START_AUTHENTICATED]: () => completeFlow(),
      [LOGIN_ANSWER]: () => sessionCompletion(),
    })
    await harness.session.restore()
    expect(harness.session.getSnapshot().impersonating).toBe(true)
    await harness.session.startAuthenticatedFlow({ purpose: 'step-up' })
    await harness.session.answerFlow({ seq: 3, step: 'complete' })
    // A step-up continues the same session; only restore writes the flag.
    expect(harness.session.getSnapshot().impersonating).toBe(true)
  })

  it('restore() resolves false and stays anonymous when nobody is signed in', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: false }),
    })
    const seen = snapshotLog(harness.session)
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    // No /me read follows an anonymous session, and no transition fired.
    expect(harness.calls.map((call) => call.path)).toEqual(['/api/v1/auth/session'])
    expect(seen).toHaveLength(0)
  })

  it('restore() ends a session the cookie no longer names', async () => {
    const harness = makeCookieHarness({
      ...cookieLogin,
      [SESSION_GET]: () => sessionView({ authenticated: false }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    const seen = snapshotLog(harness.session)
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    expect(seen.map((snapshot) => snapshot.state)).toEqual(['anonymous'])
  })

  it('restore() treats a 401 from the read as nobody signed in', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () => {
        throw apiError(401, 'authn.session_expired')
      },
    })
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('restore() rethrows a server-side failure and keeps the snapshot', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeCookieHarness({
      [SESSION_GET]: () => {
        throw refused
      },
    })
    await expect(harness.session.restore()).rejects.toBe(refused)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('a cookie step-up keeps the principal and the permission sets', async () => {
    const harness = makeCookieHarness({
      ...cookieLogin,
      [LOGIN_START_AUTHENTICATED]: () => completeFlow(),
      [LOGIN_ANSWER]: () => sessionCompletion({ id: 'session-2' }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    harness.session.setPermissionSet('tenant', ['notes:read'])
    harness.session.setPermissionSet('system', ['users:manage'])
    const before = harness.session.getSnapshot()
    const callsBefore = harness.calls.length
    await harness.session.startAuthenticatedFlow({ purpose: 'step-up' })
    const state = await harness.session.answerFlow({ seq: 3, step: 'complete' })
    expect(state.state).toBe('authenticated')
    // The principal stands and no /me read followed the step-up.
    expect(harness.session.getSnapshot().principal).toBe(before.principal)
    expect(harness.session.getSnapshot().permissionSets).toEqual({
      tenant: ['notes:read'],
      system: ['users:manage'],
    })
    expect(harness.calls.slice(callsBefore)).toHaveLength(2)
  })

  it('logout() ends the cookie session and goes anonymous', async () => {
    const harness = makeCookieHarness({
      ...cookieLogin,
      [LOGOUT]: (call) => {
        expect(call.options?.method).toBe('POST')
      },
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    await harness.session.logout()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
    // A cookie session holds no refresh token: refresh() is a no-op.
    await expect(harness.session.refresh()).resolves.toBe(false)
  })

  it('a restore that resolves after a logout does not resurrect the session', async () => {
    let releaseRestore!: () => void
    const restoreGate = new Promise<void>((resolve) => {
      releaseRestore = resolve
    })
    const harness = makeCookieHarness({
      ...cookieLogin,
      [LOGOUT]: () => undefined,
      [SESSION_GET]: () => restoreGate.then(() => sessionView({ authenticated: true })),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    const restoring = harness.session.restore()
    await harness.session.logout()
    releaseRestore()
    await expect(restoring).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })
})

describe('impersonating write rules', () => {
  // Every rule below starts from an authenticated snapshot the server
  // answered with `impersonating: true`: an anonymous pre-state cannot
  // tell a write of false from leaving the old value alone.
  const impersonatingRestore = {
    [SESSION_GET]: () =>
      sessionView({ authenticated: true, impersonating: true }),
    [PROFILE_GET]: () => principal(),
  }

  async function restoredWhileImpersonating(
    harness: Harness,
  ): Promise<void> {
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(true)
  }

  it('a token login completion clears impersonating', async () => {
    const harness = makeHarness({
      ...impersonatingRestore,
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
    })
    await restoredWhileImpersonating(harness)
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.session.getSnapshot().impersonating).toBe(false)
  })

  it('a cookie login completion clears impersonating', async () => {
    const harness = makeCookieHarness({
      ...impersonatingRestore,
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => sessionCompletion(),
    })
    await restoredWhileImpersonating(harness)
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    expect(harness.session.getSnapshot().impersonating).toBe(false)
  })

  it('logout clears impersonating', async () => {
    const harness = makeCookieHarness({
      ...impersonatingRestore,
      [LOGOUT]: () => undefined,
    })
    await restoredWhileImpersonating(harness)
    await harness.session.logout()
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('a restore that reads nobody signed in clears impersonating', async () => {
    let signedIn = true
    const harness = makeCookieHarness({
      [SESSION_GET]: () =>
        signedIn
          ? sessionView({ authenticated: true, impersonating: true })
          : sessionView({ authenticated: false }),
      [PROFILE_GET]: () => principal(),
    })
    await restoredWhileImpersonating(harness)
    signedIn = false
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('a refused refresh clears impersonating', async () => {
    const harness = makeHarness({
      ...impersonatingRestore,
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [TOKEN_ISSUE]: () => {
        throw apiError(400, 'authn.invalid_grant')
      },
    })
    // The token login is what puts a refresh token in the session's hand.
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    await restoredWhileImpersonating(harness)
    await expect(harness.session.refresh()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('reads impersonating only as a boolean', async () => {
    for (const value of ['false', 1]) {
      const harness = makeCookieHarness({
        [SESSION_GET]: () => ({
          ...sessionView({ authenticated: true }),
          impersonating: value,
        }),
        [PROFILE_GET]: () => principal(),
      })
      await expect(harness.session.restore()).resolves.toBe(true)
      expect(harness.session.getSnapshot().impersonating).toBe(false)
    }
  })

  it('attaching a permission set keeps impersonating', async () => {
    const harness = makeCookieHarness(impersonatingRestore)
    await restoredWhileImpersonating(harness)
    harness.session.setPermissionSet('system', ['tenant.read'])
    expect(harness.session.getSnapshot().impersonating).toBe(true)
    expect(harness.session.getSnapshot().permissionSets.system).toEqual([
      'tenant.read',
    ])
  })

  it('a token refresh keeps impersonating', async () => {
    const harness = makeHarness({
      ...impersonatingRestore,
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [TOKEN_ISSUE]: () => tokenResponse(),
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    await restoredWhileImpersonating(harness)
    await expect(harness.session.refresh()).resolves.toBe(true)
    expect(harness.session.getSnapshot().impersonating).toBe(true)
  })

  it('a token step-up keeps impersonating', async () => {
    let answerCall = 0
    const harness = makeHarness({
      ...impersonatingRestore,
      [LOGIN_START_AUTHENTICATED]: () => completeFlow(),
      [LOGIN_ANSWER]: () => {
        answerCall += 1
        return answerCall === 1 ? authenticatedCompletion() : elevatedCompletion()
      },
    })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
    await restoredWhileImpersonating(harness)
    await harness.session.startAuthenticatedFlow({ purpose: 'step-up' })
    await harness.session.answerFlow({ seq: 3, step: 'complete' })
    expect(harness.session.getSnapshot().impersonating).toBe(true)
  })

  it('a settled feature retry keeps impersonating', async () => {
    const harness = makeCookieHarness(impersonatingRestore)
    await restoredWhileImpersonating(harness)
    await harness.session.retryFeatures()
    expect(harness.session.getSnapshot().features.state).toBe('ready')
    expect(harness.session.getSnapshot().impersonating).toBe(true)
  })
})

describe('identity transitions', () => {
  // The window an impersonation start opens: the server rotates the
  // session cookie, and a read issued under the outgoing cookie answers
  // anonymous while the transition runs. The guard keeps that answer from
  // ending the local session.
  const cookieLogin = {
    [LOGIN_START]: () => completeFlow(),
    [LOGIN_ANSWER]: () => sessionCompletion(),
    [PROFILE_GET]: () => principal(),
  }

  async function signInCookie(harness: Harness): Promise<void> {
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })
  }

  it('discards a 401 answered during an open transition: the snapshot stays authenticated', async () => {
    let rotating = false
    const harness = makeCookieHarness({
      ...cookieLogin,
      [SESSION_GET]: () => {
        if (rotating) {
          throw apiError(401, 'authn.session_expired')
        }
        return sessionView({ authenticated: true })
      },
    })
    await signInCookie(harness)
    const before = harness.session.getSnapshot()
    const seen = snapshotLog(harness.session)
    const release = harness.session.beginIdentityTransition()
    rotating = true
    // Without the guard this answer clears the snapshot: the silent-401
    // leg would report a spurious sign-out mid-transition.
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toBe(before)
    expect(seen).toHaveLength(0)
    release()
  })

  it('settles the real state on the next read after the release', async () => {
    let signedIn = true
    const harness = makeCookieHarness({
      [SESSION_GET]: () =>
        signedIn
          ? sessionView({ authenticated: true })
          : sessionView({ authenticated: false }),
      [PROFILE_GET]: () => principal(),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    const release = harness.session.beginIdentityTransition()
    // The session genuinely ended during the window: the read is
    // discarded while the transition runs...
    signedIn = false
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    // ...and the next read after the release settles it.
    release()
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toEqual(ANONYMOUS)
  })

  it('discards the answer of a restore already in flight when the transition opened', async () => {
    let releaseRead!: () => void
    const readGate = new Promise<void>((resolve) => {
      releaseRead = resolve
    })
    let reading = false
    const harness = makeCookieHarness({
      [SESSION_GET]: () =>
        reading
          ? readGate.then(() => {
              throw apiError(401, 'authn.session_expired')
            })
          : sessionView({ authenticated: true }),
      [PROFILE_GET]: () => principal(),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    reading = true
    const inFlight = harness.session.restore()
    const release = harness.session.beginIdentityTransition()
    releaseRead()
    await expect(inFlight).resolves.toBe(false)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    release()
  })

  it('discards a restore issued during the window when it settles after the release', async () => {
    let releaseRead!: () => void
    const readGate = new Promise<void>((resolve) => {
      releaseRead = resolve
    })
    let reading = false
    const harness = makeCookieHarness({
      ...cookieLogin,
      [SESSION_GET]: () =>
        reading
          ? readGate.then(() => {
              throw apiError(401, 'authn.session_expired')
            })
          : sessionView({ authenticated: true }),
    })
    await signInCookie(harness)
    const before = harness.session.getSnapshot()
    const seen = snapshotLog(harness.session)
    const release = harness.session.beginIdentityTransition()
    reading = true
    // Issued while the window is open, so it carries the outgoing
    // credential. It outlives the release: the answer still speaks for the
    // credential the window replaced and must not end the session.
    const inFlight = harness.session.restore()
    release()
    releaseRead()
    await expect(inFlight).resolves.toBe(false)
    expect(harness.session.getSnapshot()).toBe(before)
    expect(seen).toHaveLength(0)
  })

  it('counts open transitions: a release called twice does not close another window', async () => {
    let rotating = false
    const harness = makeCookieHarness({
      ...cookieLogin,
      [SESSION_GET]: () => {
        if (rotating) {
          throw apiError(401, 'authn.session_expired')
        }
        return sessionView({ authenticated: true })
      },
    })
    await signInCookie(harness)
    const first = harness.session.beginIdentityTransition()
    const second = harness.session.beginIdentityTransition()
    first()
    first()
    rotating = true
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    second()
  })

  it('rethrows a transport failure untouched while a transition is open', async () => {
    const refused = apiError(503, 'authn.unavailable')
    const harness = makeCookieHarness({
      ...cookieLogin,
      [SESSION_GET]: () => {
        throw refused
      },
    })
    await signInCookie(harness)
    const release = harness.session.beginIdentityTransition()
    await expect(harness.session.restore()).rejects.toBe(refused)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    release()
  })
})

describe('with the real api-client', () => {
  it('retries a refused request with the freshly refreshed token', async () => {
    const store = createMemoryAccessTokenStore()
    const session = createAuthSession(store)
    const fetchCalls: Array<{
      path: string
      method: string
      authorization: string | null
    }> = []
    let meAttempts = 0
    const fetcher: typeof fetch = async (input, init) => {
      const url = new URL(String(input))
      const method = init?.method ?? 'GET'
      const authorization = new Headers(init?.headers).get('authorization')
      fetchCalls.push({ path: url.pathname, method, authorization })
      if (url.pathname === '/api/v1/auth/flows') {
        return jsonResponse(200, completeFlow())
      }
      if (url.pathname === '/api/v1/auth/flows/answer') {
        return jsonResponse(200, authenticatedCompletion())
      }
      if (url.pathname === '/api/v1/auth/me') {
        meAttempts += 1
        // The first /api/v1/auth/me is the login completion's own principal
        // read; the second is the request under test, which carries the
        // stale token and is refused; the third is its retry with the
        // fresh one.
        if (meAttempts === 2) {
          return jsonResponse(401, {
            code: 'authn.session_expired',
            params: {},
          })
        }
        return jsonResponse(200, principal())
      }
      if (url.pathname === '/api/v1/auth/token') {
        return jsonResponse(
          200,
          tokenResponse({ access_token: 'access-2', refresh_token: 'refresh-2' }),
        )
      }
      throw new Error(`unexpected fetch: ${url.pathname}`)
    }
    const client = createClient({
      baseUrl: 'https://api.test',
      fetch: fetcher,
      accessTokenStore: store,
      refreshAccessToken: () => session.refresh(),
    })
    bindRequestFn(client)

    await session.startFlow({ method: 'password' })
    await session.answerFlow({ seq: 2, step: 'complete' })
    expect(store.get()).toBe('access-1')

    // A real generated operation through the real client: the first
    // attempt presents the stale token and is refused; the silent-401
    // path runs one session refresh and retries with the fresh token.
    await authnProfileGet()
    expect(store.get()).toBe('access-2')
    expect(session.getSnapshot().state).toBe('authenticated')

    const meCalls = fetchCalls.filter((call) => call.path === '/api/v1/auth/me')
    expect(meCalls).toHaveLength(3)
    expect(meCalls[0]?.authorization).toBe('Bearer access-1')
    expect(meCalls[1]?.authorization).toBe('Bearer access-1')
    expect(meCalls[2]?.authorization).toBe('Bearer access-2')
    // The session's own refresh request went out exactly once. It OUGHT
    // to travel credential-less (the generated operation is declared to
    // carry omitAccessToken) so a refused refresh can never re-enter the
    // client's bearer-only refresh path -- but the checked-in generated
    // surface still routes the token operation through the bearer
    // mutator: web/orval.config.ts's per-operation override key named
    // the pre-flow operation id and no longer matched, so the override
    // (now corrected to 'authn.token.issue') did not apply when the
    // artifact was last regenerated. This suite therefore pins the call
    // and the retry, not the omission, until the artifact is regenerated
    // from the corrected config; see the notes in AGENTS.md.
    expect(fetchCalls.filter((call) => call.path === '/api/v1/auth/token')).toHaveLength(
      1,
    )
  })

  it("keeps the refused request's 401 and the snapshot when a transition is open", async () => {
    // A cookie session (no store): the silent-401 leg is restore(), and
    // the window is the one an impersonation start opens by rotating the
    // cookie.
    const session = createAuthSession()
    let phase: 'operator' | 'rotating' | 'target' = 'operator'
    let sessionReads = 0
    const warnings: string[] = []
    const fetcher: typeof fetch = async (input) => {
      const url = new URL(String(input))
      if (url.pathname === '/api/v1/auth/session') {
        sessionReads += 1
        if (phase === 'rotating') {
          return jsonResponse(401, {
            code: 'authn.session_expired',
            params: {},
          })
        }
        return jsonResponse(
          200,
          sessionView({
            authenticated: true,
            impersonating: phase === 'target',
          }),
        )
      }
      if (url.pathname === '/api/v1/auth/me') {
        if (phase === 'rotating') {
          return jsonResponse(401, {
            code: 'authn.session_expired',
            params: {},
          })
        }
        return jsonResponse(
          200,
          principal({
            account_id: phase === 'target' ? 'acct-target' : 'acct-operator',
          }),
        )
      }
      if (url.pathname === '/api/v1/features') {
        return jsonResponse(200, { features: [] })
      }
      throw new Error(`unexpected fetch: ${url.pathname}`)
    }
    const client = createClient({
      baseUrl: 'https://api.test',
      fetch: fetcher,
      credentials: 'same-origin',
      refreshAccessToken: (issue) => session.restore(issue),
      // The silent leg fails on purpose here; capture the transport's
      // diagnostic instead of writing it to the test console.
      reporter: {
        error: () => undefined,
        warn: (message) => {
          warnings.push(message)
        },
      },
    })
    bindRequestFn(client)

    await expect(session.restore()).resolves.toBe(true)
    expect(session.getSnapshot().principal?.account_id).toBe('acct-operator')

    const release = session.beginIdentityTransition()
    phase = 'rotating'
    const readsBefore = sessionReads
    // The refused request runs the session's silent leg, whose read
    // answers anonymous under the outgoing cookie. The guard discards it,
    // so the caller keeps its 401 instead of being answered from an
    // anonymous session the host would render as a sign-out.
    const error = await captureRejection(authnProfileGet())
    expect(isApiError(error)).toBe(true)
    expect(isApiError(error) ? error.status : null).toBe(401)
    expect(sessionReads).toBeGreaterThan(readsBefore)
    // The transport reports the silent leg's failure -- the discard is a
    // session-level answer, not a transport failure.
    expect(warnings).toContain('access token refresh failed')
    expect(session.getSnapshot().state).toBe('authenticated')
    expect(session.getSnapshot().principal?.account_id).toBe('acct-operator')

    // After the release the next read settles the identity the new
    // credential names.
    release()
    phase = 'target'
    await expect(session.restore()).resolves.toBe(true)
    expect(session.getSnapshot().impersonating).toBe(true)
    expect(session.getSnapshot().principal?.account_id).toBe('acct-target')
  })
})

/** Builds a Response with a JSON body for the fetch stand-in. */
function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}
