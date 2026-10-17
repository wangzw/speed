/**
 * useWriteFeedback contract: one slot per feedback scope, settled by the
 * attempt that lands last.
 *
 * The cases pin the rules a wrong implementation loses: overlapping
 * attempts settle the slot in landing order (not starting order), a null
 * settlement leaves the slot empty, a throwing attempt is captured as a
 * failure instead of propagating, reset clears the slot without
 * cancelling an attempt in flight, the in-flight count keeps `pending`
 * true until every attempt settles, and the success message is the
 * translated string captured at `succeed` time -- a later language
 * switch does not rewrite it while a failure code keeps following the
 * active language.
 */

import { act, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
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

    // The attempt started second settles first; its outcome occupies the
    // slot until the other attempt lands.
    await act(async () => {
      second.resolve('second landed')
      await secondRun
    })
    expect(result.current.outcome).toEqual({
      kind: 'success',
      message: 'second landed',
    })

    // The attempt started first settles last and replaces the slot.
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

  it('leaves the slot empty when an attempt settles null', async () => {
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
    // The attempt starts by clearing the slot: the previous success is gone
    // before the gate's outcome is known.
    expect(result.current.outcome).toBeNull()

    await act(async () => {
      await runPromise
    })
    expect(result.current.outcome).toBeNull()
    expect(result.current.pending).toBe(false)
  })

  it('captures a rejection as a failure and never rejects its own promise', async () => {
    const { result } = renderHook(() => useWriteFeedback())

    let runPromise!: Promise<void>
    act(() => {
      runPromise = result.current.run(() =>
        Promise.reject(new Error('write refused')),
      )
    })
    await expect(runPromise).resolves.toBeUndefined()
    await waitFor(() => {
      expect(result.current.outcome).toEqual({
        kind: 'failure',
        code: 'client.unknown',
      })
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
    await waitFor(() => {
      expect(result.current.outcome).toEqual({
        kind: 'failure',
        code: 'client.unknown',
      })
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
    // The attempt is still in flight: reset clears the slot, never the work.
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
    // One attempt is still in flight.
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
})

/** A host holding one feedback scope beside the region that renders it. */
function FeedbackHarness() {
  const feedback = useWriteFeedback()
  const { t, i18n } = useTranslation(UI_KIT_NAMESPACE)
  const [confirmed, setConfirmed] = useState(0)
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          feedback.succeed(t('emptyState.empty.title'))
          setConfirmed((count) => count + 1)
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
      <p>confirmed {confirmed}</p>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={(code) => `${i18n.language}:${code}`}
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

    // The success message is a snapshot: the region still shows exactly the
    // text translated before the switch, not the en-US bundle and not a
    // re-resolved code.
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
    // A failure never stands beside a success: the success region stays
    // mounted but empty.
    expect(screen.getByRole('status')).toHaveTextContent('')
  })
})
