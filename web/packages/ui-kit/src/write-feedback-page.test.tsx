
import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { useWriteFeedback } from './write-feedback.js'
import { WriteFeedbackPageProvider } from './write-feedback-page.js'

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

function useTwoScopes() {
  return { first: useWriteFeedback(), second: useWriteFeedback() }
}

function pageWrapper({ children }: { readonly children: ReactNode }) {
  return <WriteFeedbackPageProvider>{children}</WriteFeedbackPageProvider>
}

describe('WriteFeedbackPageProvider', () => {
  it('clears the other scope when one succeeds', () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    act(() => {
      result.current.first.succeed('first saved')
      result.current.second.succeed('second saved')
    })
    expect(result.current.first.outcome).toBeNull()
    expect(result.current.second.outcome).toEqual({
      kind: 'success',
      message: 'second saved',
    })
  })

  it('clears the other scope as soon as an attempt starts', () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    const pending = deferred<string | null>()
    act(() => {
      result.current.first.succeed('first saved')
    })
    act(() => {
      void result.current.second.run(() => pending.promise)
    })
    expect(result.current.first.outcome).toBeNull()
    expect(result.current.second.outcome).toBeNull()
  })

  it('lets the later settlement replace the earlier one across scopes', async () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    const pending = deferred<string | null>()
    act(() => {
      void result.current.first.run(() => pending.promise)
    })
    act(() => {
      result.current.second.succeed('second saved')
    })
    await act(async () => {
      pending.resolve('first saved')
      await pending.promise
    })
    expect(result.current.first.outcome).toEqual({
      kind: 'success',
      message: 'first saved',
    })
    expect(result.current.second.outcome).toBeNull()
  })

  it('clears a sibling when one fails', () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    act(() => {
      result.current.first.succeed('first saved')
    })
    act(() => {
      result.current.second.fail(Object.assign(new Error('refused'), {
        code: 'authn.bad_request',
      }))
    })
    expect(result.current.first.outcome).toBeNull()
    expect(result.current.second.outcome).toMatchObject({
      kind: 'failure',
      code: 'authn.bad_request',
    })
  })

  it('clears a sibling that wrote while the attempt was in flight when it throws', async () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    const pending = deferred<string | null>()
    act(() => {
      void result.current.first.run(() => pending.promise)
    })
    act(() => {
      result.current.second.succeed('second saved')
    })
    await act(async () => {
      pending.reject(
        Object.assign(new Error('refused'), { code: 'authn.bad_request' }),
      )
      await pending.promise.catch(() => undefined)
    })
    expect(result.current.first.outcome).toMatchObject({
      kind: 'failure',
      code: 'authn.bad_request',
    })
    expect(result.current.second.outcome).toBeNull()
  })

  it('keeps reset local to its own scope', () => {
    const { result } = renderHook(useTwoScopes, { wrapper: pageWrapper })
    act(() => {
      result.current.first.succeed('first saved')
    })
    act(() => {
      result.current.second.reset()
    })
    expect(result.current.first.outcome).toEqual({
      kind: 'success',
      message: 'first saved',
    })
  })

  it('leaves the sibling slots alone without a provider', () => {
    const { result } = renderHook(useTwoScopes)
    act(() => {
      result.current.first.succeed('first saved')
      result.current.second.succeed('second saved')
    })
    expect(result.current.first.outcome).toEqual({
      kind: 'success',
      message: 'first saved',
    })
    expect(result.current.second.outcome).toEqual({
      kind: 'success',
      message: 'second saved',
    })
  })
})

describe('WriteFeedbackPageProvider across components', () => {
  function Scope({ name }: { readonly name: string }) {
    const feedback = useWriteFeedback()
    const message = feedback.outcome?.kind === 'success' ? feedback.outcome.message : ''
    return (
      <div>
        <button type="button" onClick={() => feedback.succeed(name)}>
          save {name}
        </button>
        <span data-testid={name}>{message}</span>
      </div>
    )
  }

  it('clears a sibling component\'s result when another component writes', () => {
    render(
      <WriteFeedbackPageProvider>
        <Scope name="first" />
        <Scope name="second" />
      </WriteFeedbackPageProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'save first' }))
    expect(screen.getByTestId('first')).toHaveTextContent('first')
    fireEvent.click(screen.getByRole('button', { name: 'save second' }))
    expect(screen.getByTestId('first')).toBeEmptyDOMElement()
    expect(screen.getByTestId('second')).toHaveTextContent('second')
    cleanup()
  })
})

describe('WriteFeedbackPageProvider after a scope leaves the page', () => {
  function SlowScope({ pending }: { readonly pending: Deferred<string | null> }) {
    const feedback = useWriteFeedback()
    return (
      <button type="button" onClick={() => void feedback.run(() => pending.promise)}>
        start slow
      </button>
    )
  }

  function FastScope() {
    const feedback = useWriteFeedback()
    const message = feedback.outcome?.kind === 'success' ? feedback.outcome.message : ''
    return (
      <div>
        <button type="button" onClick={() => feedback.succeed('fast saved')}>
          save fast
        </button>
        <span data-testid="fast">{message}</span>
      </div>
    )
  }

  function Harness({ pending }: { readonly pending: Deferred<string | null> }) {
    const [showSlow, setShowSlow] = useState(true)
    return (
      <WriteFeedbackPageProvider>
        {showSlow ? <SlowScope pending={pending} /> : null}
        <FastScope />
        <button type="button" onClick={() => setShowSlow(false)}>
          leave
        </button>
      </WriteFeedbackPageProvider>
    )
  }

  it("does not let an unmounted scope's late settlement clear the new page", async () => {
    const pending = deferred<string | null>()
    render(<Harness pending={pending} />)
    fireEvent.click(screen.getByRole('button', { name: 'start slow' }))
    fireEvent.click(screen.getByRole('button', { name: 'leave' }))
    fireEvent.click(screen.getByRole('button', { name: 'save fast' }))
    expect(screen.getByTestId('fast')).toHaveTextContent('fast saved')

    await act(async () => {
      pending.resolve('slow saved')
      await pending.promise
    })
    expect(screen.getByTestId('fast')).toHaveTextContent('fast saved')
    cleanup()
  })
})
