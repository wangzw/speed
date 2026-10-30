/**
 * The personal-tenant backfill in the tenant frame.
 *
 * The server decides the answer; the host's job is to ask only when the
 * caller's own personal tenant is missing from a settled membership list --
 * authenticated, not impersonating, the workspace.personal feature in force,
 * the list settled -- and to render what each answer means: the personal tenant
 * as the current one when none is settled, and a retryable failure for every
 * failure. The generated ensure operation runs through the api-sdk runtime
 * seam, so each case binds a scripted request function and observes the calls
 * it makes.
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { AuthnProfileProfile } from './app-api/index.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { ACCOUNT_SETUP_PATH } from './paths.js'
import { clearSelfTenants } from './tenant-self.js'
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
const SELF_PATH = '/api/v1/auth/me/tenants'

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

/** The caller tenant read's answer: a list of memberships, or a failure. A
 * function answers per call, so a case can change the list after the ensure. */
type SelfAnswer =
  | 'failure'
  | { readonly tenants: readonly { readonly id: string; readonly alias: string }[] }

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
      contact: false,
      default_contact: false,
      sign_in: true,
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
      contact: false,
      default_contact: false,
      sign_in: true,
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
  readonly ensure?: 'success' | 'conflict' | 'unavailable'
  /** Whether the answer reports a creation; a function answers per call. */
  readonly ensureCreated?: boolean | (() => boolean)
  /** The personal tenant identifier the ensure answers; defaults to the
   * first identity's. A function answers per call, so a case that swaps the
   * acting account returns the identifier that account's server would derive. */
  readonly ensureTenantId?: () => string
  readonly ensureCalls?: string[]
  /** Each ensure answer's outcome, recorded when the answer is decided -- an
   * acceptance or a rejection -- rather than when the request is issued: a
   * case that judges what the rejection leaves out waits for this. */
  readonly ensureOutcomes?: ('settled' | 'rejected')[]
  /** The caller tenant read's answer; defaults to an empty, settled list. A
   * function answers per call, so a case can reveal a membership the ensure
   * or the roster settled after the first read. */
  readonly self?: SelfAnswer | (() => SelfAnswer)
  readonly selfCalls?: string[]
  readonly calls?: string[]
  readonly profile?: () => AuthnProfileProfile
}): void {
  const ensure = options.ensure ?? 'success'
  bindScript(async (path, call) => {
    options.calls?.push(`${call.method ?? 'GET'} ${path}`)
    if ((call.method ?? 'GET') === 'GET' && path === SELF_PATH) {
      options.selfCalls?.push(SELF_PATH)
      const self =
        typeof options.self === 'function'
          ? options.self()
          : (options.self ?? { tenants: [] })
      if (self === 'failure') {
        throw Object.assign(new Error('the self read failed'), { status: 503 })
      }
      return self
    }
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
      // The verification banner reads the profile; an unscripted profile is the
      // ordinary incomplete one rather than a failure.
      return options.profile === undefined
        ? INCOMPLETE_PROFILE
        : options.profile()
    }
    if (call.method === 'POST' && path === ENSURE_PATH) {
      options.ensureCalls?.push(ENSURE_PATH)
      let outcome: 'settled' | 'rejected' = 'settled'
      try {
        if (ensure === 'conflict') {
          throw Object.assign(new Error('already a member'), { status: 409 })
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
        const tenantId = options.ensureTenantId?.() ?? PERSONAL_ID
        return {
          tenant: { id: tenantId, alias: tenantId, kind: 'personal' },
          created,
        }
      } catch (error) {
        outcome = 'rejected'
        throw error
      } finally {
        options.ensureOutcomes?.push(outcome)
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
              contact: false,
              default_contact: false,
              sign_in: true,
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
              contact: false,
              default_contact: false,
              sign_in: true,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
    },
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  clearSelfTenants()
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

  it('does not ensure from a failed self read: a failure is not an empty list', async () => {
    const ensureCalls: string[] = []
    const selfCalls: string[] = []
    script({ roster: 'refused', ensureCalls, selfCalls, self: 'failure' })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The read failed, so the list never settled and the trigger is not met:
    // the host does not mistake a failed read for "no memberships" and create
    // a space the account may not need.
    await waitFor(() => expect(selfCalls.length).toBeGreaterThanOrEqual(1))
    expect(ensureCalls).toHaveLength(0)
    expect(getCurrentTenantId()).toBeNull()
  })

  it('backfills beside other memberships and leaves the current tenant', async () => {
    const ensureCalls: string[] = []
    script({
      roster: 'refused',
      ensureCalls,
      self: { tenants: [{ id: 'acme', alias: 'acme' }] },
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The list holds another tenant, but not the caller's own personal one, so
    // the backfill asks for it. The settled current tenant is not replaced.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
  })

  it('settles the first membership that is not the account’s personal tenant', async () => {
    const ensureCalls: string[] = []
    script({
      ensureCalls,
      // Ordered by identifier: the personal row is deliberately not first.
      self: {
        tenants: [
          { id: 'aaa', alias: 'aaa' },
          { id: PERSONAL_ID, alias: PERSONAL_ID },
        ],
      },
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // A business membership is the default when the account has one; the
    // existing personal row means no ensure.
    await waitFor(() => expect(getCurrentTenantId()).toBe('aaa'))
    expect(ensureCalls).toHaveLength(0)
  })

  it('ensures without the roster read when the assembly does not serve it', async () => {
    const ensureCalls: string[] = []
    script({ ensureCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures('workspace.personal') }),
      ),
    })

    // No roster read is part of this assembly, so the order guard has nothing
    // to wait for: the settled empty list and the unknown current tenant are
    // enough.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(getCurrentTenantId()).toBe(PERSONAL_ID))
  })

  it('re-reads the self list after creating the personal tenant', async () => {
    const selfCalls: string[] = []
    let created = false
    bindScript(async (path, call) => {
      if ((call.method ?? 'GET') === 'GET' && path === SELF_PATH) {
        selfCalls.push(SELF_PATH)
        return created
          ? { tenants: [{ id: PERSONAL_ID, alias: PERSONAL_ID }] }
          : { tenants: [] }
      }
      if ((call.method ?? 'GET') === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if (call.method === 'POST' && path === ENSURE_PATH) {
        created = true
        return {
          tenant: { id: PERSONAL_ID, alias: PERSONAL_ID, kind: 'personal' },
          created: true,
        }
      }
      if ((call.method ?? 'GET') === 'GET' && path === '/api/v1/auth/me') {
        return INCOMPLETE_PROFILE
      }
      throw new Error(`unexpected ${call.method ?? 'GET'} ${path}`)
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The list the switcher renders is the read's answer: after the ensure
    // creates the space the host reads it again, so the new row comes from the
    // membership source rather than a special case.
    await waitFor(() => expect(getCurrentTenantId()).toBe(PERSONAL_ID))
    await waitFor(() => expect(selfCalls.length).toBeGreaterThanOrEqual(2))
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
  })

  it('offers the setup notice from an incomplete profile even when the backfill created nothing', async () => {
    // The account already held its personal tenant, so the backfill answer is
    // created: false. The profile is deliberately left incomplete: the notice
    // is driven by setup incomplete alone, so it still appears.
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
              contact: false,
              default_contact: false,
              sign_in: true,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
        },
      }),
    })

    // The backfill settled (the switcher labels the personal tenant), and the
    // home page offers the setup notice from the profile alone.
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()
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
    const selfCalls: string[] = []
    const calls: string[] = []
    script({ roster: 'refused', calls, selfCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures('tenant.admin') }),
      ),
    })

    // The empty self list is the account's membership fact: the current tenant
    // stays unknown, and nothing creates a personal tenant when the deployment
    // does not serve the policy.
    await waitFor(() => expect(selfCalls).toHaveLength(1))
    expect(getCurrentTenantId()).toBeNull()
    expect(calls).not.toContain(`POST ${ENSURE_PATH}`)
  })

  it('backfills while a settled business tenant stays current', async () => {
    const calls: string[] = []
    script({
      self: { tenants: [{ id: 'acme', alias: 'acme' }] },
      calls,
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The caller's own personal tenant is missing from its list, so it asks
    // for one; the settled current tenant is left in place.
    await waitFor(() =>
      expect(calls).toContain(`POST ${ENSURE_PATH}`),
    )
    expect(getCurrentTenantId()).toBe('acme')
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
            // The contract caps a grant at one hour from its start; a distant
            // expiry would schedule a timer past JavaScript's delay range.
            ends_at: new Date(Date.now() + 3_600_000).toISOString(),
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

  it('ensures the personal tenant for an account whose registration verification is pending', async () => {
    // Verification limits what the account may do, not whether its workspace
    // exists: the frame asks for the personal tenant and shows the verification
    // notice at the same time.
    const ensureCalls: string[] = []
    script({ roster: 'refused', ensureCalls, profile: () => PENDING_PROFILE })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(getCurrentTenantId()).toBe(PERSONAL_ID))
    expect(
      await screen.findByRole('region', {
        name: new RegExp(demoEnUS.verificationBanner.region),
      }),
    ).toBeInTheDocument()
  })

  it('renders a retryable failure on a 503, and the retry asks again', async () => {
    const ensureCalls: string[] = []
    let behavior: 'unavailable' | 'success' = 'unavailable'
    bindScript(async (path, call) => {
      if ((call.method ?? 'GET') === 'GET' && path === SELF_PATH) {
        return { tenants: [] }
      }
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

    const failure = await screen.findByText(demoEnUS.workspace.failure.title)
    expect(failure).toBeInTheDocument()
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

  it('shows the retryable failure on a 409 from the backfill', async () => {
    const ensureCalls: string[] = []
    script({
      roster: 'refused',
      ensure: 'conflict',
      ensureCalls,
      self: { tenants: [] },
    })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The two remaining 409s (a disabled personal tenant, an unavailable
    // identifier) do not change on a retry by the client; the tenant area shows
    // its retryable failure rather than inventing a second outcome.
    const failure = await screen.findByText(demoEnUS.workspace.failure.title)
    expect(failure).toBeInTheDocument()
    expect(ensureCalls).toHaveLength(1)
    expect(getCurrentTenantId()).toBeNull()
  })

  it('does not let a failed roster read stand in for membership: the empty self list still ensures', async () => {
    const ensureCalls: string[] = []
    const selfCalls: string[] = []
    script({ roster: { status: 500 }, ensureCalls, selfCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    // The self list is the membership fact. A 5xx roster read says nothing
    // about it; once the list has settled empty, the order guard is satisfied
    // (the roster read settled, even by failing) and the ensure runs.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    expect(selfCalls.length).toBeGreaterThanOrEqual(1)
    await waitFor(() => expect(getCurrentTenantId()).toBe(PERSONAL_ID))
  })

  it('does not let a network-failed roster read block the ensure either', async () => {
    const ensureCalls: string[] = []
    script({ roster: 'network', ensureCalls })
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures(...PERSONAL_FEATURES) }),
      ),
    })

    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(getCurrentTenantId()).toBe(PERSONAL_ID))
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
    const ensureOutcomes: ('settled' | 'rejected')[] = []
    script({
      roster: 'refused',
      ensure: 'unavailable',
      ensureCalls,
      ensureOutcomes,
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
    // The rejection, not the request's issue, is what makes the failure state
    // real: wait until the answer was decided before judging the page.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(ensureOutcomes).toEqual(['rejected']))
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
    const ensureOutcomes: ('settled' | 'rejected')[] = []
    script({
      roster: 'refused',
      ensure: 'unavailable',
      ensureCalls,
      ensureOutcomes,
      profile: () => INCOMPLETE_PROFILE,
    })
    renderDemoApp({
      session: createTestSession(snapshotWithProfile(false)),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    // The rejection, not the request's issue, is what makes the failure state
    // real: wait until the answer was decided before judging the page.
    await waitFor(() => expect(ensureCalls).toHaveLength(1))
    await waitFor(() => expect(ensureOutcomes).toEqual(['rejected']))
    expect(screen.queryByText(demoEnUS.workspace.failure.title)).toBeNull()
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
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
    let identities = 0
    script({
      roster: 'refused',
      // The server derives the identifier per account; the second identity's
      // own space is a different identifier, and the switcher must label it as
      // personal (not another account's row).
      ensureTenantId: () =>
        identities++ === 0 ? PERSONAL_ID : 'personal-account-2',
      profile: () => INCOMPLETE_PROFILE,
    })
    const session = createTestSession(snapshotWithProfile(false))
    renderDemoApp({ session })

    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()
    // Dismiss it for the first identity's session.
    fireEvent.click(
      screen.getByRole('button', { name: demoEnUS.home.setupNotice.dismiss }),
    )
    await waitFor(() =>
      expect(screen.queryByText(demoEnUS.home.setupNotice.title)).toBeNull(),
    )

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
    // The second identity's profile is incomplete, so its notice is offered
    // again: the first identity's dismissal belongs to that identity alone.
    expect(
      await screen.findByRole('button', { name: demoEnUS.workspace.personal }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()
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
    // The roster alias is gone too: the id it belonged to is no longer
    // nameable by any source, and there is no default list behind it to
    // announce a stale name.
    setCurrentTenantId('acme')
    expect(getCurrentTenantAlias()).toBeNull()
  })
})
