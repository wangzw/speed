/**
 * WriteFeedbackPageProvider: one page, one visible result.
 *
 * The cases pin the page-level rules: a scope's attempt start or
 * settlement clears the other scopes' slots, `reset` stays local, and
 * without the provider the slots keep the single-scope behaviour the
 * library ships. The two scopes live in one component, so the owner has
 * to be per hook instance rather than per component -- two scopes in one
 * component must still clear each other.
 */

import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { useWriteFeedback } from './write-feedback.js'
import { WriteFeedbackPageProvider } from './write-feedback-page.js'

interface Deferred<Value> {
  readonly promise: Promise<Value>
  readonly resolve: (value: Value) => void
}

function deferred<Value>(): Deferred<Value> {
  let resolve!: (value: Value) => void
  const promise = new Promise<Value>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
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
