import { describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import {
  LOGIN_ANSWER,
  LOGIN_CURRENT,
  LOGIN_START,
  PROFILE_GET,
  SESSION_GET,
  apiError,
  completeFlow,
  makeCookieHarness,
  openFlow,
  principal,
  sessionCompletion,
  sessionView,
} from '@speed/test-utils/session-harness'
import { useFlowDriver } from './use-flow-driver.js'

const CODE_STEP = {
  kind: 'challenge' as const,
  fields: [{ name: 'code', kind: 'code', secret: true }],
}

function startRequest() {
  return {
    method: 'sms',
    binding: 'cookie' as const,
    input: { phone: '+15550000001' },
  }
}

function answerBodies(harness: ReturnType<typeof makeCookieHarness>) {
  return harness.calls
    .filter((call) => call.path === '/api/v1/auth/flows/answer')
    .map((call) => call.options?.body)
}

describe('useFlowDriver', () => {
  it('retries a refused complete with the ready step’s own sequence', async () => {
    let codeAnswers = 0
    let completes = 0
    const harness = makeCookieHarness({
      [LOGIN_START]: () => openFlow({ seq: 1, next: CODE_STEP }),
      [LOGIN_ANSWER]: (call) => {
        const body = call.options?.body as { seq: number; step: string }
        if (body.step === 'answer') {
          codeAnswers += 1
          if (codeAnswers > 1) {
            throw apiError(401, 'authn.flow_invalid')
          }
          return completeFlow(2)
        }
        completes += 1
        if (completes === 1) {
          throw apiError(503, 'authn.unavailable')
        }
        return sessionCompletion()
      },
      [LOGIN_CURRENT]: () => completeFlow(2),
      [PROFILE_GET]: () => principal(),
    })
    const onFailure = vi.fn()
    const onSettled = vi.fn()
    const { result } = renderHook(() =>
      useFlowDriver(harness.session, { onFailure, onSettled }),
    )

    await act(async () => {
      await result.current.start(startRequest())
    })
    await act(async () => {
      await result.current.answer({ code: '123456' })
    })

    expect(onFailure).toHaveBeenCalledTimes(1)
    expect(onFailure.mock.calls[0]?.[0]).toBe('authn.unavailable')
    expect(result.current.step).toBeNull()
    expect(result.current.currentStep()).toMatchObject({
      seq: 2,
      next: { kind: 'complete' },
    })

    await act(async () => {
      await result.current.answer({})
    })

    expect(answerBodies(harness).at(-1)).toMatchObject({
      seq: 2,
      step: 'complete',
      input: {},
    })
    expect(onSettled).toHaveBeenCalledTimes(1)
  })

  it('leaves no step to resend when a committed login’s profile read fails', async () => {
    const refused = apiError(503, 'client.http.503')
    let profiles = 0
    const harness = makeCookieHarness({
      [LOGIN_START]: () => openFlow({ seq: 1, next: CODE_STEP }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'answer'
          ? completeFlow(2)
          : sessionCompletion(),
      [PROFILE_GET]: () => {
        profiles += 1
        if (profiles === 1) {
          throw refused
        }
        return principal()
      },
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
      [SESSION_GET]: () => sessionView({ authenticated: true }),
    })
    const onFailure = vi.fn()
    const onSettled = vi.fn()
    const { result } = renderHook(() =>
      useFlowDriver(harness.session, { onFailure, onSettled }),
    )

    await act(async () => {
      await result.current.start(startRequest())
    })
    await act(async () => {
      await result.current.answer({ code: '123456' })
    })

    expect(onFailure).toHaveBeenCalledTimes(1)
    expect(onFailure.mock.calls[0]?.[0]).toBe('client.http.503')
    expect(onFailure.mock.calls[0]?.[1]).toBe(refused)
    expect(result.current.currentStep()).toBeNull()
    expect(onSettled).not.toHaveBeenCalled()
    expect(answerBodies(harness)).toHaveLength(2)

    await act(async () => {
      await result.current.answer({})
    })
    expect(answerBodies(harness)).toHaveLength(2)

    await act(async () => {
      await expect(harness.session.restore()).resolves.toBe(true)
    })
    expect(harness.session.getSnapshot().state).toBe('authenticated')
  })
})
