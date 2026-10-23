/**
 * The personal-tenant trigger in the tenant frame.
 *
 * The server decides the answer; the host's job is to ask only when every
 * eligibility fact the server checks holds -- authenticated, not impersonating,
 * the workspace.personal feature in force, a settled roster read that left no
 * current tenant -- and to render what each answer means: the personal tenant
 * as the current one, the admission refusal for the 409 already-member, and a
 * retryable failure for everything else. The generated ensure operation runs
 * through the api-sdk runtime seam, so each case binds a scripted request
 * function and observes the calls it makes.
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { AuthnProfileProfile } from './app-api/index.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  DEFAULT_TENANT_ID,
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { ACCOUNT_PATH, ACCOUNT_SETUP_PATH } from './paths.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const PERSONAL_ID = 'personal-account-1'
const ROSTER_PATH = '/api/v1/system/tenants'
const ENSURE_PATH = '/api/v1/workspace/personal'

const PERSONAL_FEATURES = ['tenant.admin', 'workspace.personal'] as const

/** Binds a scripted request function: the case-specific answers are plain
 * values, and the seam's generic result type is the caller's contract. */
function bindScript(
  fn: (path: string, call: RequestOptions) => Promise<unknown>,
): void {
  bindRequestFn(fn as unknown as RequestFn)
}

/** The roster answer a case scripts: a member's refusal, a successful list,
 * an HTTP failure of another status, or a network failure carrying none. */
type RosterAnswer =
  | 'refused'
  | 'network'
  | { readonly tenants: readonly unknown[] }
  | { readonly status: number }

/** A profile the session's GET /api/v1/auth/me answers: no login name and no
 * password -- the setup-incomplete state the notice is about. */
const INCOMPLETE_PROFILE: AuthnProfileProfile = {
  account_id: 'account-1',
  kind: 'user',
  display_name: 'Test User',
  mfa_required: false,
  password_set: false,
  created_at: '2026-01-01T00:00:00Z',
  attributes: {},
  locale: 'en-US',
  time_zone: 'UTC',
  identifiers: [
    {
      id: 'identifier-phone',
      kind: 'phone',
      value: '+14155550123',
      proven: true,
      verified_at: '2026-01-01T00:00:00Z',
      verified_via: 'sms-code',
    },
  ],
  links: [],
  verification: { state: 'none', email: '', since: '' },
}

/** A self-registration whose email address is not proved yet. */
const PENDING_PROFILE: AuthnProfileProfile = {
  ...INCOMPLETE_PROFILE,
  password_set: true,
  verification: { state: 'pending', email: 'ada@x.test', since: '' },
}

/** The profile of an account that completed setup: a username and a
 * password. */
const COMPLETE_PROFILE: AuthnProfileProfile = {
  ...INCOMPLETE_PROFILE,
  password_set: true,
  identifiers: [
    ...INCOMPLETE_PROFILE.identifiers,
    {
      id: 'identifier-username',
      kind: 'username',
      value: 'test-user',
      proven: false,
      verified_at: '',
      verified_via: 'self',
    },
  ],
}

/** A scripted request function: the roster read answers as the case asks,
 * the ensure does too, and GET /api/v1/auth/me answers the profile when the case
 * scripts one (the home page reads it once the notice is eligible). Every
 * other call fails loudly. */
function script(options: {
  readonly roster?: RosterAnswer
  readonly ensure?: 'success' | 'conflict' | 'unavailable' | 'verification'
  /** Whether the answer reports a creation; a function answers per call. */
  readonly ensureCreated?: boolean | (() => boolean)
  readonly ensureCalls?: string[]
  readonly calls?: string[]
  readonly profile?: () => AuthnProfileProfile
}): void {
  const ensure = options.ensure ?? 'success'
  bindScript(async (path, call) => {
    options.calls?.push(`${call.method ?? 'GET'} ${path}`)
    if ((call.method ?? 'GET') === 'GET' && path === ROSTER_PATH) {
      const roster = options.roster ?? { tenants: [] }
      if (roster === 'refused') {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if (roster === 'network') {
        throw new TypeError('failed to fetch')
      }
      if ('status' in roster) {
        throw Object.assign(new Error('the roster read failed'), {
          status: roster.status,
        })
      }
      return roster
    }
    if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/me') {
      // The frame reads the profile to decide the verification state even
      // when a case cares only about the ensure, so an unscripted profile is
      // the ordinary incomplete one rather than a failure.
      return options.profile === undefined
        ? INCOMPLETE_PROFILE
        : options.profile()
    }
    if (call.method === 'POST' && path === ENSURE_PATH) {
      options.ensureCalls?.push(ENSURE_PATH)
      if (ensure === 'conflict') {
        throw Object.assign(new Error('already a member'), { status: 409 })
      }
      if (ensure === 'verification') {
        throw Object.assign(new Error('verify the email first'), {
          status: 409,
          code: 'workspace.verification_required',
        })
      }
      if (ensure === 'unavailable') {
        throw Object.assign(new Error('the store is unavailable'), {
          status: 503,
        })
      }
      const created =
        typeof options.ensureCreated === 'function'
          ? options.ensureCreated()
          : (options.ensureCreated ?? true)
      return {
        tenant: { id: PERSONAL_ID, alias: PERSONAL_ID, kind: 'personal' },
        created,
      }
    }
    throw new Error(`unexpected ${call.method ?? 'GET'} ${path}`)
  })
}

/** Moves the app to a path through the browser history, the way a link
 * outside React (or a typed URL) does: the router listens for popstate. */
function navigateTo(pathname: string): void {
  window.history.pushState({}, '', pathname)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

/** The authenticated snapshot with a principal whose setup state the case
 * chooses. */
function snapshotWithProfile(
  complete: boolean,
  features = readyFeatures(...PERSONAL_FEATURES),
) {
  const snapshot = authenticatedSnapshot({ features })
  if (snapshot.principal === null) {
    throw new Error('the authenticated snapshot carries no principal')
  }
  return {
    ...snapshot,
    principal: {
      ...snapshot.principal,
      password_set: complete,
      identifiers: complete
        ? [
            {
              id: 'identifier-username',
              kind: 'username',
              value: 'test-user',
              proven: false,
              verified_at: '',
              verified_via: 'self',
            },
          ]
        : [
            {
              id: 'identifier-phone',
              kind: 'phone',
              value: '+14155550123',
              proven: true,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
    },
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(null)
  setTenantAliases(new Map())
})

describe('the tenant frame and the personal tenant', () => {
  it('ensures a personal tenant when no current tenant is left, and labels it', async () => {
    const ensureCalls: string[] = []
    script({ roster: 'refused', ensure: 'success', ensureCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    expect(
      await screen.findByRole('button', {
        name: demoEnUS.workspace.personal,
      }),
    ).toBeInTheDocument()
    expect(ensureCalls).toEqual([ENSURE_PATH])
    expect(getCurrentTenantId()).toBe(PERSONAL_ID)
    expect(getCurrentTenantAlias()).toBe(PERSONAL_ID)
  })

  it('does not offer the setup notice when the personal tenant already existed', async () => {
    // The account already held its personal tenant, so the ensure answer is
    // created: false. The profile is deliberately left incomplete: the
    // notice's other half is true, which makes created the deciding fact.
    script({ roster: 'refused', ensureCreated: false })
    const snapshot = authenticatedSnapshot({
      features: readyFeatures(...PERSONAL_FEATURES),
    })
    if (snapshot.principal === null) {
      throw new Error('the authenticated snapshot carries no principal')
    }
    renderDemoApp({
      session: createTestSession({
        ...snapshot,
        principal: {
          ...snapshot.principal,
          password_set: false,
          identifiers: [
            {
              id: 'identifier-phone',
              kind: 'phone',
              value: '+14155550123',
              proven: true,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
        },
      }),
    })

    // The ensure settled (the switcher labels the personal tenant), and the
    // home page shows no notice because this call created nothing.
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()
  })

  it('renders the personal label in the interface language', async () => {
    script({ roster: 'refused' })
    renderDemoApp({
      language: 'zh-CN',
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    expect(
      await screen.findByRole('button', { name: demoZhCN.workspace.personal }),
    ).toBeInTheDocument()
  })

  it('does not call the ensure for an anonymous viewer', async () => {
    const calls: string[] = []
    script({ roster: 'refused', calls })
    renderDemoApp()

    // The anonymous viewer meets the host's sign-in surface; the ensure needs
    // an authenticated caller and is never issued.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('does not call the ensure when the feature is not in force', async () => {
    const calls: string[] = []
    script({ roster: 'refused', calls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures('tenant.admin') }),
      ),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe(DEFAULT_TENANT_ID))
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('does not call the ensure when a current tenant is settled', async () => {
    const calls: string[] = []
    script({
      roster: {
        tenants: [{ id: 'acme', alias: 'acme', status: 'active' }],
      },
      calls,
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('does not call the ensure while impersonating', async () => {
    const calls: string[] = []
    bindScript(async (path, call) => {
      calls.push(`${call.method ?? 'GET'} ${path}`)
      if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/impersonation') {
        return {
          grant: {
            id: 'grant-1',
            target: 'account-1',
            tenant: 'acme',
            ends_at: '2999-01-01T00:00:00Z',
          },
        }
      }
      if ((call.method ?? 'GET') === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/me') {
        return INCOMPLETE_PROFILE
      }
      throw new Error(`unexpected ${call.method ?? 'GET'} ${path}`)
    })
    const snapshot = authenticatedSnapshot({
      features: readyFeatures(...PERSONAL_FEATURES),
    })
    renderDemoApp({
      session: createTestSession({ ...snapshot, impersonating: true }),
    })

    await waitFor(() =>
      expect(calls).toContain('GET /api/v1/auth/impersonation'),
    )
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('does not call the personal ensure while registration verification is pending', async () => {
    // The server refuses a pending account, so the frame answers with the
    // verification state alone: no ensure request is issued until the address
    // is proved.
    const ensureCalls: string[] = []
    script({ roster: 'refused', ensureCalls, profile: () => PENDING_PROFILE })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    expect(
      await screen.findByRole('region', {
        name: new RegExp(demoEnUS.verificationBanner.region),
      }),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(ensureCalls).toEqual([])
    expect(getCurrentTenantId()).toBeNull()
  })

  it('navigates to the verification page on the 409 verification-required, not a failure', async () => {
    // The server's refusal names the reason: the account must verify first.
    // That is a routing fact, not a retryable failure, so the frame moves to
    // the verification page and renders no failure placeholder.
    const ensureCalls: string[] = []
    script({
      roster: 'refused',
      ensure: 'verification',
      ensureCalls,
      profile: () => INCOMPLETE_PROFILE,
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => {
      expect(window.location.pathname).toBe(ACCOUNT_PATH)
      expect(window.location.search).toBe('?verify=1')
    })
    expect(screen.queryByText(demoEnUS.workspace.failure.title)).toBeNull()
    expect(getCurrentTenantId()).toBeNull()
  })

  it('resumes the ensure once verification moves the profile to verified', async () => {
    // The 409 is not a terminal answer: the account proves its address on the
    // verification page, the verify write invalidates the profile, and the
    // frame's ensure runs again without a reload.
    const ensureCalls: string[] = []
    let verificationState = 'none'
    bindScript(async (path, call) => {
      if ((call.method ?? 'GET') === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/me') {
        return {
          ...INCOMPLETE_PROFILE,
          verification: {
            state: verificationState,
            email: 'ada@x.test',
            since: '',
          },
        }
      }
      if (call.method === 'POST' && path === ENSURE_PATH) {
        ensureCalls.push(ENSURE_PATH)
        if (ensureCalls.length === 1) {
          throw Object.assign(new Error('verify the email first'), {
            status: 409,
            code: 'workspace.verification_required',
          })
        }
        return {
          tenant: { id: PERSONAL_ID, alias: PERSONAL_ID, kind: 'personal' },
          created: true,
        }
      }
      throw new Error(`unexpected ${call.method ?? 'GET'} ${path}`)
    })
    const { queryClient } = renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => {
      expect(window.location.pathname).toBe(ACCOUNT_PATH)
      expect(window.location.search).toBe('?verify=1')
    })
    expect(getCurrentTenantId()).toBeNull()

    verificationState = 'verified'
    await act(async () => {
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    })

    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(ensureCalls).toHaveLength(2)
    expect(getCurrentTenantId()).toBe(PERSONAL_ID)
  })

  it('renders a retryable failure on a 503, and the retry asks again', async () => {
    const ensureCalls: string[] = []
    let behavior: 'unavailable' | 'success' = 'unavailable'
    bindScript(async (path, call) => {
      if ((call.method ?? 'GET') === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/me') {
        return INCOMPLETE_PROFILE
      }
      if (call.method === 'POST' && path === ENSURE_PATH) {
        ensureCalls.push(ENSURE_PATH)
        if (behavior === 'unavailable') {
          throw Object.assign(new Error('the store is unavailable'), {
            status: 503,
          })
        }
        return {
          tenant: { id: PERSONAL_ID, alias: PERSONAL_ID, kind: 'personal' },
          created: true,
        }
      }
      throw new Error(`unexpected ${call.method ?? 'GET'} ${path}`)
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    const failure = await screen.findByRole('alert')
    expect(failure).toHaveTextContent(demoEnUS.workspace.failure.title)
    expect(ensureCalls).toHaveLength(1)
    expect(getCurrentTenantId()).toBeNull()

    behavior = 'success'
    fireEvent.click(
      screen.getByRole('button', { name: demoEnUS.workspace.failure.retry }),
    )
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(ensureCalls).toHaveLength(2)
    expect(getCurrentTenantId()).toBe(PERSONAL_ID)
  })

  it('keeps no personal tenant on the 409, falling back to the placeholder', async () => {
    const ensureCalls: string[] = []
    script({ roster: 'refused', ensure: 'conflict', ensureCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe(DEFAULT_TENANT_ID))
    expect(ensureCalls).toHaveLength(1)
    // No personal tenant was set, and no retryable failure is shown: the
    // tenant-scoped reads fire under the placeholder and render the admission
    // layer's access-refused state, which is the tenant area's answer for an
    // account without a readable roster.
    expect(
      screen.queryByRole('button', { name: demoEnUS.workspace.personal }),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('does not ensure a personal tenant when the roster read fails with a 5xx', async () => {
    const calls: string[] = []
    script({ roster: { status: 500 }, calls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // Only a 403 says "an ordinary member". A 5xx says nothing about
    // membership, so it falls back to the placeholder tenant and the ensure
    // is never called.
    await waitFor(() => expect(getCurrentTenantId()).toBe(DEFAULT_TENANT_ID))
    expect(calls).toContain(`GET ${ROSTER_PATH}`)
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('does not ensure a personal tenant on a network failure', async () => {
    const calls: string[] = []
    script({ roster: 'network', calls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe(DEFAULT_TENANT_ID))
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('offers no notice when a new tenant already has a complete account', async () => {
    const calls: string[] = []
    script({
      roster: 'refused',
      ensureCreated: true,
      profile: () => COMPLETE_PROFILE,
      calls,
    })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(true)),
    })

    // The ensure created the tenant, but the account already holds the
    // username and password the notice would ask it to add: the eligibility
    // condition is false and the home page shows no notice. The frame still
    // reads the profile once -- the verification banner needs its state --
    // and the shared query dedupes that with anything else reading it.
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      calls.filter((call) => call === 'GET /api/v1/auth/me'),
    ).toHaveLength(1)
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()
  })

  it('keeps the account-level pages rendering while the ensure keeps failing', async () => {
    // The account setup page reads no tenant data, so a sustained ensure
    // failure must not replace it with the tenant failure view: that is
    // where a new account adds the username and password.
    window.history.replaceState({}, '', ACCOUNT_SETUP_PATH)
    const ensureCalls: string[] = []
    script({
      roster: 'refused',
      ensure: 'unavailable',
      ensureCalls,
      profile: () => INCOMPLETE_PROFILE,
    })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.accountSetup.title,
      }),
    ).toBeInTheDocument()
    // Let the ensure's rejection settle and React commit what it makes of
    // it before judging the page.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(screen.queryByText(demoEnUS.workspace.failure.title)).toBeNull()
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.accountSetup.title,
      }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBeNull()
  })

  it('keeps the account page rendering while the ensure keeps failing', async () => {
    window.history.replaceState({}, '', '/account')
    const ensureCalls: string[] = []
    script({
      roster: 'refused',
      ensure: 'unavailable',
      ensureCalls,
      profile: () => INCOMPLETE_PROFILE,
    })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(screen.queryByText(demoEnUS.workspace.failure.title)).toBeNull()
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
  })

  it('still replaces a tenant-reading page with the retryable failure', async () => {
    window.history.replaceState({}, '', '/')
    script({ roster: 'refused', ensure: 'unavailable' })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByText(demoEnUS.workspace.failure.title),
    ).toBeInTheDocument()
  })

  it('keeps the notice dismissed across navigation', async () => {
    window.history.replaceState({}, '', '/')
    script({
      roster: 'refused',
      ensureCreated: true,
      profile: () => INCOMPLETE_PROFILE,
    })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()
    fireEvent.click(
      screen.getByRole('button', { name: demoEnUS.home.setupNotice.dismiss }),
    )
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()

    // Leave the home page and come back: the dismissal belongs to the
    // identity's session, so the notice stays away.
    act(() => navigateTo(ACCOUNT_SETUP_PATH))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.accountSetup.title,
      }),
    ).toBeInTheDocument()
    act(() => navigateTo('/'))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()
  })

  it('resets the notice when the identity changes', async () => {
    let ensures = 0
    script({
      roster: 'refused',
      // The first identity's tenant is created; the second identity already
      // has its own, so it must not inherit the first one's notice.
      ensureCreated: () => ensures++ === 0,
      profile: () => INCOMPLETE_PROFILE,
    })
    const session = createTestSession(snapshotWithProfile(false))
    renderDemoApp({ session })

    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()

    act(() => session.set(ANONYMOUS_SNAPSHOT))
    // The home page is gone with the session; the reset runs while the shell
    // renders its ended state.
    await waitFor(() =>
      expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull(),
    )

    act(() =>
      session.set({
        ...snapshotWithProfile(false),
        principal: {
          ...(snapshotWithProfile(false).principal ?? {}),
          account_id: 'account-2',
        },
      }),
    )
    // The second identity's ensure answered created: false, and the first
    // identity's eligibility was cleared when the session turned anonymous.
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()
  })

  it('hides the notice once setup is completed on another page', async () => {
    let complete = false
    script({
      roster: 'refused',
      ensureCreated: true,
      profile: () => (complete ? COMPLETE_PROFILE : INCOMPLETE_PROFILE),
    })
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()

    // The account follows the notice to the setup page and completes it
    // there; coming back, the home page reads the current profile and the
    // notice is gone.
    complete = true
    await user.click(
      screen.getByRole('link', { name: demoEnUS.home.setupNotice.action }),
    )
    expect(
      await screen.findByText(demoEnUS.accountSetup.complete),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('link', { name: demoEnUS.accountSetup.home }),
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull()
  })

  it('clears the current tenant and the aliases when the session turns anonymous', async () => {
    // A renamed alias the roster reported earlier: after the session ends it
    // must be gone with the identity that read it.
    setTenantAliases(new Map([['acme', 'acme-renamed']]))
    script({ roster: 'refused', ensureCreated: true })
    const session = createTestSession(snapshotWithProfile(true))
    renderDemoApp({ session })

    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBe(PERSONAL_ID)
    expect(getCurrentTenantAlias()).toBe(PERSONAL_ID)

    act(() => session.set(ANONYMOUS_SNAPSHOT))

    // No identity is left for a tenant fact to belong to: both cells are
    // cleared, and a tenant the alias map still held would not resolve.
    await waitFor(() => expect(getCurrentTenantId()).toBeNull())
    expect(getCurrentTenantAlias()).toBeNull()
    // The roster alias is gone too: the placeholder's own name is what a
    // later resolution falls back to, not the ended identity's read.
    setCurrentTenantId('acme')
    expect(getCurrentTenantAlias()).toBe('acme')
  })
})
