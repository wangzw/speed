// @vitest-environment jsdom
/**
 * features.test.ts -- the feature state's observable behaviour and the
 * compile-time name check.
 *
 * The session reads GET /api/v1/features on a login commit and on an
 * anonymous-start restore, keeps it on the same-session paths, and resets
 * it on logout. The hooks read it fail-closed: only a `ready` set that
 * contains the name answers true. The `@ts-expect-error` assertion pins
 * useFeature's parameter to the generated FeatureName, so a misspelled
 * name fails this package's typecheck (and, were the type to widen to
 * string, the directive would become unused and fail it too).
 */

import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import {
  authenticatedCompletion,
  completeFlow,
  elevatedCompletion,
  FEATURES_GET,
  LOGIN_ANSWER,
  LOGIN_START,
  LOGOUT,
  makeCookieHarness,
  makeHarness,
  principal,
  PROFILE_GET,
  SESSION_GET,
  sessionView,
} from '@speed/test-utils/session-harness'
import type { Harness } from '@speed/test-utils/session-harness'
import {
  attachSession,
  featurePresence,
  useFeature,
  useFeatureState,
} from './hooks'

/** The name type at useFeature's parameter position. */
type FeatureArgument = Parameters<typeof useFeature>[0]

const declaredName: FeatureArgument = 'authn.mfa'
// @ts-expect-error 'objectstore.objectz' is not a declared feature name
const misspelledName: FeatureArgument = 'objectstore.objectz'

afterEach(() => {
  cleanup()
})

const baseScript = {
  [LOGIN_START]: () => completeFlow(),
  [LOGIN_ANSWER]: () => authenticatedCompletion(),
  [PROFILE_GET]: () => principal(),
}

async function signIn(harness: Harness): Promise<void> {
  await harness.session.startFlow({ method: 'password' })
  await harness.session.answerFlow({ seq: 2, step: 'complete' })
}

describe('before any session is attached', () => {
  it('fails closed on the feature state and useFeature', () => {
    const { result: state } = renderHook(() => useFeatureState())
    expect(state.current).toEqual({ state: 'none' })
    const { result: feature } = renderHook(() => useFeature('authn.mfa'))
    expect(feature.current).toBe(false)
  })
})

describe('the feature state over a session', () => {
  it('commits the served names with a login and answers useFeature from them', async () => {
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => ({ features: ['authn.mfa', 'notification.inbox'] }),
    })
    attachSession(harness.session)
    await signIn(harness)
    const { result: state } = renderHook(() => useFeatureState())
    expect(state.current).toEqual({
      state: 'ready',
      names: new Set(['authn.mfa', 'notification.inbox']),
    })
    const { result: present } = renderHook(() => useFeature('authn.mfa'))
    const { result: absent } = renderHook(() => useFeature('tenant.admin'))
    expect(present.current).toBe(true)
    expect(absent.current).toBe(false)
    // The feature read went out alongside the profile read.
    const featureReads = harness.calls.filter(
      (call) => call.path === '/api/v1/features',
    )
    expect(featureReads).toHaveLength(1)
  })

  it('keeps the login when the feature read fails: the state is failed, not absent or present', async () => {
    const refusal = new Error('features unavailable')
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => {
        throw refusal
      },
    })
    attachSession(harness.session)
    await signIn(harness)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    const { result: state } = renderHook(() => useFeatureState())
    expect(state.current).toEqual({ state: 'failed', error: refusal })
    const { result: feature } = renderHook(() => useFeature('authn.mfa'))
    expect(feature.current).toBe(false)
    expect(featurePresence(state.current)).toEqual({ state: 'failed' })
  })

  it('retries a failed read explicitly and shares one concurrent read', async () => {
    let attempts = 0
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => {
        attempts += 1
        if (attempts === 1) {
          throw new Error('first read fails')
        }
        return { features: ['authn.mfa'] }
      },
    })
    attachSession(harness.session)
    await signIn(harness)
    const { result: state } = renderHook(() => useFeatureState())
    expect(state.current.state).toBe('failed')
    await act(async () => {
      await Promise.all([
        harness.session.retryFeatures(),
        harness.session.retryFeatures(),
      ])
    })
    expect(attempts).toBe(2)
    expect(state.current).toEqual({
      state: 'ready',
      names: new Set(['authn.mfa']),
    })
  })

  it('never requests features while anonymous, and retryFeatures is a no-op', async () => {
    const harness = makeHarness({})
    attachSession(harness.session)
    await harness.session.retryFeatures()
    expect(harness.calls).toHaveLength(0)
    const { result } = renderHook(() => useFeatureState())
    expect(result.current).toEqual({ state: 'none' })
  })

  it('publishes pending while the retry read is in flight', async () => {
    let release!: () => void
    let reads = 0
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => {
        reads += 1
        if (reads === 1) {
          return { features: [] }
        }
        return new Promise((resolve) => {
          release = () => resolve({ features: ['authn.mfa'] })
        })
      },
    })
    attachSession(harness.session)
    await signIn(harness)
    expect(harness.session.getSnapshot().features.state).toBe('ready')

    const seen: string[] = []
    harness.session.subscribe((snapshot) => {
      seen.push(snapshot.features.state)
    })
    const retry = harness.session.retryFeatures()
    expect(harness.session.getSnapshot().features).toEqual({ state: 'pending' })
    expect(seen).toContain('pending')

    release()
    await retry
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['authn.mfa']),
    })
  })

  it('does not start a second read when a listener retries on the pending transition', async () => {
    let release!: () => void
    let reads = 0
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => {
        reads += 1
        if (reads === 1) {
          return { features: [] }
        }
        return new Promise((resolve) => {
          release = () => resolve({ features: ['authn.mfa'] })
        })
      },
    })
    attachSession(harness.session)
    await signIn(harness)

    // A subscriber that reacts to the `pending` transition by retrying
    // again: it re-enters while the outer call is publishing the state.
    // The in-flight read is already registered, so the re-entrant call
    // shares it instead of issuing a second GET /features.
    let reentered = false
    harness.session.subscribe((snapshot) => {
      if (!reentered && snapshot.features.state === 'pending') {
        reentered = true
        void harness.session.retryFeatures()
      }
    })
    const retry = harness.session.retryFeatures()
    release()
    await retry

    expect(reentered).toBe(true)
    expect(reads).toBe(2)
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['authn.mfa']),
    })
  })

  it('discards a retry read that resolves after the server ended the session', async () => {
    // A server-ended clear (an authenticated restore answered anonymous)
    // does not bump the generation, so the commit check's "still
    // authenticated" clause is the only thing that keeps the late read
    // from overwriting the cleared state.
    let release!: () => void
    let reads = 0
    const harness = makeHarness({
      ...baseScript,
      [SESSION_GET]: () => ({ authenticated: false }),
      [FEATURES_GET]: () => {
        reads += 1
        if (reads === 1) {
          return { features: [] }
        }
        return new Promise((resolve) => {
          release = () => resolve({ features: ['authn.mfa'] })
        })
      },
    })
    attachSession(harness.session)
    await signIn(harness)

    const retry = harness.session.retryFeatures()
    expect(harness.session.getSnapshot().features.state).toBe('pending')
    await expect(harness.session.restore()).resolves.toBe(false)
    expect(harness.session.getSnapshot().state).toBe('anonymous')

    release()
    await retry
    expect(harness.session.getSnapshot()).toEqual({
      state: 'anonymous',
      principal: null,
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'none' },
    })
  })

  it('does not hand an in-flight retry to a session that started after it', async () => {
    let release!: () => void
    let reads = 0
    const harness = makeHarness({
      ...baseScript,
      [LOGOUT]: () => undefined,
      [FEATURES_GET]: () => {
        reads += 1
        if (reads === 1) {
          return { features: ['authn.mfa'] }
        }
        if (reads === 2) {
          return new Promise((resolve) => {
            release = () => resolve({ features: ['tenant.admin'] })
          })
        }
        return { features: ['notification.inbox'] }
      },
    })
    attachSession(harness.session)
    await signIn(harness)

    const stale = harness.session.retryFeatures()
    expect(harness.session.getSnapshot().features.state).toBe('pending')
    await harness.session.logout()
    await signIn(harness)
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['notification.inbox']),
    })

    // The first session's read is still in flight. The new session's retry
    // must run its own read rather than await a flight that is bound to be
    // discarded.
    const retry = harness.session.retryFeatures()
    expect(reads).toBe(4)
    expect(harness.session.getSnapshot().features.state).toBe('pending')

    release()
    await stale
    await retry
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['notification.inbox']),
    })
  })

  it('restores the pre-retry state when a step-up supersedes the retry', async () => {
    let release!: () => void
    let reads = 0
    let answers = 0
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => {
        answers += 1
        return answers === 1 ? authenticatedCompletion() : elevatedCompletion()
      },
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => {
        reads += 1
        if (reads === 1) {
          return { features: ['authn.mfa'] }
        }
        return new Promise((resolve) => {
          release = () => resolve({ features: ['tenant.admin'] })
        })
      },
    })
    attachSession(harness.session)
    await signIn(harness)
    const previous = harness.session.getSnapshot().features

    const retry = harness.session.retryFeatures()
    expect(harness.session.getSnapshot().features.state).toBe('pending')
    // A step-up continues the same session and keeps the pending marker.
    await harness.session.answerFlow({ seq: 3, step: 'complete' })
    expect(harness.session.getSnapshot().features.state).toBe('pending')

    release()
    await retry
    // The superseded retry restores the state the marker replaced rather
    // than leaving the session stuck on pending.
    expect(harness.session.getSnapshot().features).toEqual(previous)
  })

  it('commits an authenticated restore with a failed feature state when the read fails', async () => {
    const refusal = new Error('features unavailable')
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true }),
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => {
        throw refusal
      },
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    // A failed read is neither every feature absent nor every feature
    // present: it must not settle as a ready empty set.
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'failed',
      error: refusal,
    })
  })

  it('clears the state on logout', async () => {
    const harness = makeHarness({
      ...baseScript,
      [FEATURES_GET]: () => ({ features: ['authn.mfa'] }),
      [LOGOUT]: () => undefined,
    })
    attachSession(harness.session)
    await signIn(harness)
    await harness.session.logout()
    const { result } = renderHook(() => useFeatureState())
    expect(result.current).toEqual({ state: 'none' })
  })

  it('reads features on an anonymous-start restore and keeps them on a later restore', async () => {
    const harness = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true }),
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => ({ features: ['tenant.admin'] }),
    })
    await expect(harness.session.restore()).resolves.toBe(true)
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['tenant.admin']),
    })
    const callsAfterFirst = harness.calls.length
    await expect(harness.session.restore()).resolves.toBe(true)
    // The same session continues: no second feature read.
    expect(harness.calls.slice(callsAfterFirst).map((call) => call.path)).toEqual([
      '/api/v1/auth/session',
      '/api/v1/auth/me',
    ])
  })

  it('maps the feature state onto the route presence shape', () => {
    expect(featurePresence({ state: 'none' })).toEqual({ state: 'pending' })
    expect(featurePresence({ state: 'pending' })).toEqual({ state: 'pending' })
    expect(featurePresence({ state: 'failed', error: null })).toEqual({
      state: 'failed',
    })
    const ready = featurePresence({
      state: 'ready',
      names: new Set(['authn.mfa']),
    })
    expect(ready.state).toBe('ready')
    if (ready.state === 'ready') {
      expect(ready.has('authn.mfa')).toBe(true)
      expect(ready.has('tenant.admin')).toBe(false)
    }
  })
})

describe('the name type', () => {
  it('accepts a declared name and refuses a misspelled one at compile time', () => {
    expect(declaredName).toBe('authn.mfa')
    expect(misspelledName).toBe('objectstore.objectz')
  })
})
