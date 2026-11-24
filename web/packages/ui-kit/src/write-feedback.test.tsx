
import { act, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createContext, useContext, useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { switchLanguage, useTranslation } from '@speed/i18n'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { UI_KIT_NAMESPACE } from './resources.js'
import { WriteFeedbackRegion } from './components/WriteFeedbackRegion.js'
import { useWriteFeedback } from './write-feedback.js'

interface Deferred<Value> {
  readonly promise: Promise<Value>
  readonly resolve: (value: Value) => void
  readonly reject: (reason: unknown) => void
}

function deferred<Value>(): Deferred<Value> {
  let resolve!: (value: Value) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<Value>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

describe('useWriteFeedback', () => {
  afterEach(async () => {
    await restoreRealTimers()
  })

  it('carries the wait a throttled refusal stated into the slot', async () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useWriteFeedback())
    const attempt = deferred<string | null>()

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
    })
    act(() => {
      vi.advanceTimersByTime(5_000)
    })
    await act(async () => {
      attempt.reject(
        Object.assign(new Error('throttled'), {
          code: 'authn.throttled',
          retryAfterMs: 45_500,
        }),
      )
      await runPromise
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'authn.throttled',
      retryAfterSeconds: 46,
      waitUntil: Date.now() + 46_000,
    })
  })

  it('records the wait deadline through fail as well', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.fail({
        code: 'authn.throttled',
        retryAfterMs: 1_500,
      })
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'authn.throttled',
      retryAfterSeconds: 2,
      waitUntil: Date.now() + 2_000,
    })
  })

  it('shows the outcome of the attempt that settles last, not the first one', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const first = deferred<string | null>()
    const second = deferred<string | null>()

    let firstRun!: Promise<void>
    let secondRun!: Promise<void>
    act(() => {
      firstRun = result.current.run(() => first.promise)
      secondRun = result.current.run(() => second.promise)
    })
    expect(result.current.pending).toBe(true)

    await act(async () => {
      second.resolve('second landed')
      await secondRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'second landed',
    })

    await act(async () => {
      first.resolve('first landed')
      await firstRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'first landed',
    })
    expect(result.current.pending).toBe(false)
  })

  it('replaces an earlier result when later attempts settle in start order', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const first = deferred<string | null>()
    const second = deferred<string | null>()

    let firstRun!: Promise<void>
    let secondRun!: Promise<void>
    act(() => {
      firstRun = result.current.run(() => first.promise)
      secondRun = result.current.run(() => second.promise)
    })

    await act(async () => {
      first.resolve('first landed')
      await firstRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'first landed',
    })

    await act(async () => {
      second.resolve('second landed')
      await secondRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'second landed',
    })
  })

  it('does not touch the slot when an attempt settles null', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.succeed('previous result')
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'previous result',
    })

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => Promise.resolve(null))
    })
    expect(result.current.outcome).toBeNull()

    await act(async () => {
      await runPromise
    })
    expect(result.current.outcome).toBeNull()
    expect(result.current.pending).toBe(false)
  })

  it('keeps the other attempt\'s success when a null settlement lands first', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const stringAttempt = deferred<string | null>()
    const nullAttempt = deferred<string | null>()

    let stringRun!: Promise<void>
    let nullRun!: Promise<void>
    act(() => {
      stringRun = result.current.run(() => stringAttempt.promise)
      nullRun = result.current.run(() => nullAttempt.promise)
    })

    await act(async () => {
      nullAttempt.resolve(null)
      await nullRun
    })
    expect(result.current.outcome).toBeNull()
    expect(result.current.pending).toBe(true)

    await act(async () => {
      stringAttempt.resolve('A landed')
      await stringRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'A landed',
    })
    expect(result.current.pending).toBe(false)
  })

  it('keeps a settled success when a null settlement lands last', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const stringAttempt = deferred<string | null>()
    const nullAttempt = deferred<string | null>()

    let stringRun!: Promise<void>
    let nullRun!: Promise<void>
    act(() => {
      stringRun = result.current.run(() => stringAttempt.promise)
      nullRun = result.current.run(() => nullAttempt.promise)
    })

    await act(async () => {
      stringAttempt.resolve('A landed')
      await stringRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'A landed',
    })

    await act(async () => {
      nullAttempt.resolve(null)
      await nullRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'A landed',
    })
    expect(result.current.pending).toBe(false)
  })

  it('keeps a rejection when the null settlement lands last', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const failing = deferred<string | null>()
    const nullAttempt = deferred<string | null>()

    let failingRun!: Promise<void>
    let nullRun!: Promise<void>
    act(() => {
      failingRun = result.current.run(() => failing.promise)
      nullRun = result.current.run(() => nullAttempt.promise)
    })

    await act(async () => {
      failing.reject({ code: 'write_failed' })
      await failingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })

    await act(async () => {
      nullAttempt.resolve(null)
      await nullRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })
    expect(result.current.pending).toBe(false)
  })

  it('keeps a rejection when the null settlement lands first', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const failing = deferred<string | null>()
    const nullAttempt = deferred<string | null>()

    let failingRun!: Promise<void>
    let nullRun!: Promise<void>
    act(() => {
      failingRun = result.current.run(() => failing.promise)
      nullRun = result.current.run(() => nullAttempt.promise)
    })

    await act(async () => {
      nullAttempt.resolve(null)
      await nullRun
    })
    expect(result.current.outcome).toBeNull()

    await act(async () => {
      failing.reject({ code: 'write_failed' })
      await failingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })
    expect(result.current.pending).toBe(false)
  })

  it('keeps a fail() result set while a null attempt is in flight', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const attempt = deferred<string | null>()

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
    })
    act(() => {
      result.current.fail({ code: 'write_failed' })
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })

    await act(async () => {
      attempt.resolve(null)
      await runPromise
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })
  })

  it('keeps a succeed() result set while a null attempt is in flight', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const attempt = deferred<string | null>()

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
    })
    act(() => {
      result.current.succeed('landed during the flight')
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'landed during the flight',
    })

    await act(async () => {
      attempt.resolve(null)
      await runPromise
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'landed during the flight',
    })
  })

  it('captures a rejection as a failure and never rejects its own promise', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() =>
        Promise.reject(new Error('write refused')),
      )
    })
    await act(async () => {
      await expect(runPromise).resolves.toBeUndefined()
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })
    expect(result.current.pending).toBe(false)
  })

  it('captures a synchronous throw as a failure instead of throwing', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => {
        throw new Error('thrown before the promise')
      })
    })
    await expect(runPromise).resolves.toBeUndefined()
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })
  })

  it('keeps a failure code from the thrown value', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() =>
        Promise.reject({ code: 'tenant.not_found' }),
      )
    })
    await act(async () => {
      await runPromise
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'tenant.not_found',
    })
  })

  it('clears the slot on reset without cancelling an attempt in flight', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const attempt = deferred<string | null>()

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
    })
    expect(result.current.pending).toBe(true)

    act(() => {
      result.current.reset()
    })
    expect(result.current.outcome).toBeNull()
    expect(result.current.pending).toBe(true)

    await act(async () => {
      attempt.resolve('settled after reset')
      await runPromise
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'settled after reset',
    })
    expect(result.current.pending).toBe(false)
  })

  it('stays pending until every overlapping attempt has settled', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const first = deferred<string | null>()
    const second = deferred<string | null>()

    let firstRun!: Promise<void>
    let secondRun!: Promise<void>
    act(() => {
      firstRun = result.current.run(() => first.promise)
      secondRun = result.current.run(() => second.promise)
    })
    expect(result.current.pending).toBe(true)

    await act(async () => {
      first.resolve('first')
      await firstRun
    })
    expect(result.current.pending).toBe(true)

    await act(async () => {
      second.resolve('second')
      await secondRun
    })
    expect(result.current.pending).toBe(false)
  })

  it('keeps its callbacks identical across renders', () => {
    const { result } = renderHook(() => useWriteFeedback())
    const before = result.current

    act(() => {
      result.current.succeed('saved')
    })
    const after = result.current

    expect(after.run).toBe(before.run)
    expect(after.reset).toBe(before.reset)
    expect(after.succeed).toBe(before.succeed)
    expect(after.fail).toBe(before.fail)
  })

  it('keeps all four callbacks identical across a pending flip', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const first = result.current
    const attempt = deferred<string | null>()

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
    })
    expect(result.current.pending).toBe(true)
    expect(result.current.run).toBe(first.run)
    expect(result.current.reset).toBe(first.reset)
    expect(result.current.succeed).toBe(first.succeed)
    expect(result.current.fail).toBe(first.fail)

    await act(async () => {
      attempt.resolve('landed')
      await runPromise
    })
    expect(result.current.pending).toBe(false)
    expect(result.current.run).toBe(first.run)
    expect(result.current.reset).toBe(first.reset)
    expect(result.current.succeed).toBe(first.succeed)
    expect(result.current.fail).toBe(first.fail)
  })

  it.each([
    ['a plain Error', new Error('boom')],
    ['a string', 'boom'],
    ['null', null],
    ['undefined', undefined],
    ['an empty code', { code: '' }],
    ['a non-string code', { code: 7 }],
  ])('collapses %s to client.unknown through fail', (_label, error) => {
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.fail(error)
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })
  })

  it('keeps a dotted code that it cannot resolve to a namespace', () => {
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.fail({ code: 'a.b' })
    })
    expect(result.current.outcome).toEqual({ kind: 'failure', code: 'a.b' })
  })

  it('collapses a thrown value whose code getter throws through run', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const throwing = {
      get code(): string {
        throw new Error('getter exploded')
      },
    }

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => Promise.reject(throwing))
    })
    await act(async () => {
      await expect(runPromise).resolves.toBeUndefined()
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })
  })

  it('does not throw when fail classifies a revoked proxy', () => {
    const { proxy, revoke } = Proxy.revocable({ code: 'tenant.not_found' }, {})
    revoke()
    const { result } = renderHook(() => useWriteFeedback())

    expect(() => {
      act(() => {
        result.current.fail(proxy)
      })
    }).not.toThrow()
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })
  })

  it('lets succeed and fail replace each other', () => {
    const { result } = renderHook(() => useWriteFeedback())

    act(() => {
      result.current.fail({ code: 'write_failed' })
    })
    act(() => {
      result.current.succeed('saved')
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'saved',
    })

    act(() => {
      result.current.fail({ code: 'write_failed' })
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })
  })

  it('lets a late success replace an early failure from an overlapping attempt', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const failing = deferred<string | null>()
    const succeeding = deferred<string | null>()

    let failingRun!: Promise<void>
    let succeedingRun!: Promise<void>
    act(() => {
      failingRun = result.current.run(() => failing.promise)
      succeedingRun = result.current.run(() => succeeding.promise)
    })

    await act(async () => {
      failing.reject(new Error('failed first'))
      await failingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'client.unknown',
    })

    await act(async () => {
      succeeding.resolve('succeeded later')
      await succeedingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'succeeded later',
    })
  })

  it('lets a late failure replace an early success from an overlapping attempt', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const succeeding = deferred<string | null>()
    const failing = deferred<string | null>()

    let succeedingRun!: Promise<void>
    let failingRun!: Promise<void>
    act(() => {
      succeedingRun = result.current.run(() => succeeding.promise)
      failingRun = result.current.run(() => failing.promise)
    })

    await act(async () => {
      succeeding.resolve('succeeded first')
      await succeedingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'succeeded first',
    })

    await act(async () => {
      failing.reject({ code: 'write_failed' })
      await failingRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'failure',
      code: 'write_failed',
    })
  })

  it('clears an old failure when a new attempt starts', () => {
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.fail({ code: 'write_failed' })
    })
    const attempt = deferred<string | null>()

    act(() => {
      void result.current.run(() => attempt.promise)
    })
    expect(result.current.pending).toBe(true)
    expect(result.current.outcome).toBeNull()
  })

  it('clears an old failure when a null attempt settles', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    act(() => {
      result.current.fail({ code: 'write_failed' })
    })

    await act(async () => {
      await result.current.run(() => Promise.resolve(null))
    })
    expect(result.current.outcome).toBeNull()
    expect(result.current.pending).toBe(false)
  })

  it('starts each instance empty and keeps two instances independent', () => {
    const { result } = renderHook(() => ({
      first: useWriteFeedback(),
      second: useWriteFeedback(),
    }))
    expect(result.current.first.outcome).toBeNull()
    expect(result.current.first.pending).toBe(false)
    expect(result.current.second.outcome).toBeNull()
    expect(result.current.second.pending).toBe(false)

    act(() => {
      result.current.first.succeed('first only')
    })
    expect(result.current.first.outcome).toEqual({
      kind: 'success',
      message: 'first only',
    })
    expect(result.current.second.outcome).toBeNull()
    expect(result.current.second.pending).toBe(false)

    act(() => {
      result.current.second.fail({ code: 'second_failed' })
    })
    expect(result.current.first.outcome).toEqual({
      kind: 'success',
      message: 'first only',
    })
    expect(result.current.second.outcome).toEqual({
      kind: 'failure',
      code: 'second_failed',
    })
  })

  it('resolves its own promise only after the attempt settles', async () => {
    const { result } = renderHook(() => useWriteFeedback())
    const attempt = deferred<string | null>()
    let runSettled = false

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() => attempt.promise)
      void runPromise.then(() => {
        runSettled = true
      })
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(runSettled).toBe(false)
    expect(result.current.pending).toBe(true)

    await act(async () => {
      attempt.resolve('late')
      await runPromise
    })
    expect(runSettled).toBe(true)
    expect(result.current.pending).toBe(false)
  })

  it('stores the success text verbatim', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    act(() => {
      result.current.succeed('  saved  ')
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: '  saved  ',
    })

    await act(async () => {
      await result.current.run(() => Promise.resolve('  saved  '))
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: '  saved  ',
    })
  })

  it('stores an empty success string literally', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    act(() => {
      result.current.succeed('')
    })
    expect(result.current.outcome).toEqual({ kind: 'success', message: '' })

    await act(async () => {
      await result.current.run(() => Promise.resolve(''))
    })
    expect(result.current.outcome).toEqual({ kind: 'success', message: '' })
  })
})

function FeedbackHarness() {
  const feedback = useWriteFeedback()
  const { t, i18n } = useTranslation(UI_KIT_NAMESPACE)
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          feedback.succeed(t('emptyState.empty.title'))
        }}
      >
        confirm
      </button>
      <button
        type="button"
        onClick={() => {
          feedback.fail({ code: 'host.write_failed' })
        }}
      >
        fail
      </button>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={(code) => `${i18n.language}:${code}`}
      />
    </div>
  )
}

const ResetGeneration = createContext(0)

function ResetOnGenerationHost({
  attempt,
}: {
  readonly attempt: () => Promise<string | null>
}) {
  const feedback = useWriteFeedback()
  const generation = useContext(ResetGeneration)
  const { reset } = feedback
  useEffect(() => {
    reset()
  }, [generation, reset])
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          void feedback.run(attempt)
        }}
      >
        run
      </button>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={(code) => code}
      />
    </div>
  )
}

describe('useWriteFeedback with a rendered region', () => {
  it('keeps the success message as the translation captured at succeed time', async () => {
    const view = renderWithProviders(<FeedbackHarness />)
    await userEvent.click(view.getByRole('button', { name: 'confirm' }))
    expect(view.getByRole('status').textContent).toBe(
      zhCN.emptyState.empty.title,
    )

    await act(async () => {
      await switchLanguage(view.i18n, 'en-US')
    })

    expect(view.getByRole('status').textContent).toBe(
      zhCN.emptyState.empty.title,
    )
    expect(view.queryByText(enUS.emptyState.empty.title)).not.toBeInTheDocument()
  })

  it('renders a failure code through the current language', async () => {
    const view = renderWithProviders(<FeedbackHarness />)
    await userEvent.click(view.getByRole('button', { name: 'fail' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      'zh-CN:host.write_failed',
    )

    await act(async () => {
      await switchLanguage(view.i18n, 'en-US')
    })
    expect(screen.getByRole('alert')).toHaveTextContent(
      'en-US:host.write_failed',
    )
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('does not clear a settled result when the pending flip re-renders the host', async () => {
    const attempt = deferred<string | null>()
    const view = renderWithProviders(
      <ResetOnGenerationHost attempt={() => attempt.promise} />,
    )

    await userEvent.click(view.getByRole('button', { name: 'run' }))
    expect(view.getByRole('status').textContent).toBe('')

    await act(async () => {
      attempt.resolve('settled by run')
    })
    expect(view.getByRole('status').textContent).toBe('settled by run')
  })
})
