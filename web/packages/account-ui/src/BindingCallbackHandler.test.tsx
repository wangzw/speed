/**
 * BindingCallbackHandler checks the link flow read back and completed on the callback route.
 */

import { useState } from 'react'
import type { ComponentProps } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { switchLanguage } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { BindingCallbackHandler } from './BindingCallbackHandler.js'

const FLOW_CURRENT = '/api/v1/auth/flows/current'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function RerenderingHost({
  session,
  onBound,
}: {
  readonly session: ComponentProps<typeof BindingCallbackHandler>['session']
  readonly onBound: () => void
}) {
  const [tick, setTick] = useState(0)
  return (
    <>
      <button type="button" onClick={() => setTick((value) => value + 1)}>
        rerender {tick}
      </button>
      <BindingCallbackHandler
        session={session}
        onBound={() => onBound()}
        absent={<p>No such page</p>}
      />
    </>
  )
}

describe('BindingCallbackHandler', () => {
  it('takes a ready flow, commits the link and fires onBound once', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onBound = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onBound={onBound} />,
    )

    await waitFor(() => expect(onBound).toHaveBeenCalledTimes(1))
    const done = await screen.findByText(zhCN.bindingCallback.done)
    expect(done).toBeTruthy()
    expect(done.closest('[role="status"]')).not.toBeNull()
    expect(zhCN.bindingCallback.done).not.toBe(zhCN.bindingCallback.pending)
    expect(enUS.bindingCallback.done).toMatch(/social account/i)
    const answer = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answer?.body).toEqual({ seq: 2, step: 'complete' })
  })

  it('fills the done region after it is mounted, so the confirmation is announced', async () => {
    let releaseRead: ((response: Response) => void) | undefined
    const read = new Promise<Response>((resolve) => {
      releaseRead = resolve
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return read
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<BindingCallbackHandler session={rig.session} />)

    await screen.findByText(zhCN.bindingCallback.pending)
    const emptyRegions = screen
      .getAllByRole('status')
      .filter((element) => element.textContent === '')
    expect(emptyRegions).toHaveLength(1)
    const doneRegion = emptyRegions[0] as HTMLElement

    await act(async () => {
      releaseRead?.(
        jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        }),
      )
    })
    await screen.findByText(zhCN.bindingCallback.done)
    expect(doneRegion).toHaveTextContent(zhCN.bindingCallback.done)
  })

  it('keeps the done confirmation across a language switch without re-reading the flow', async () => {
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        reads += 1
        if (reads === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'ready',
            next: { kind: 'complete' },
          })
        }
        return errorResponse(401, 'authn.flow_invalid')
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const { i18n } = renderWithProviders(
      <BindingCallbackHandler session={rig.session} />,
    )

    await screen.findByText(zhCN.bindingCallback.done)
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })

    expect(screen.getByText(zhCN.bindingCallback.done)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(reads).toBe(1)
  })

  it('takes the flow once when the host re-renders with a new callback', async () => {
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        reads += 1
        if (reads === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'ready',
            next: { kind: 'complete' },
          })
        }
        return errorResponse(401, 'authn.flow_invalid')
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onBound = vi.fn()
    renderWithProviders(
      <RerenderingHost session={rig.session} onBound={onBound} />,
    )
    const user = userEvent.setup()

    await waitFor(() => expect(onBound).toHaveBeenCalledTimes(1))

    await user.click(screen.getByRole('button', { name: /rerender/ }))
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(reads).toBe(1)
    expect(onBound).toHaveBeenCalledTimes(1)
    expect(screen.getByText(zhCN.bindingCallback.done)).toBeTruthy()
  })

  it('holds the pending notice while the flow is still on a step', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onBound = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onBound={onBound} />,
    )

    expect(await screen.findByText(zhCN.bindingCallback.pending)).toBeTruthy()
    expect(onBound).not.toHaveBeenCalled()
  })

  it('renders a parked step and completes the link when it is answered', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onBound = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onBound={onBound} />,
    )
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.bindingCallback.continueLabel }),
    )

    await waitFor(() => expect(onBound).toHaveBeenCalledTimes(1))
    const answer = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answer?.body).toEqual({
      seq: 1,
      step: 'answer',
      input: { code: '123456' },
    })
  })

  it('renders a failed read as code text under a retry that re-reads', async () => {
    let fail = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        if (fail) {
          return errorResponse(503, 'authn.unavailable')
        }
        return jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { state: 'linked' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onBound = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onBound={onBound} />,
    )
    const user = userEvent.setup()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)

    fail = false
    await user.click(
      screen.getByRole('button', { name: zhCN.bindingCallback.retry }),
    )
    await waitFor(() => expect(onBound).toHaveBeenCalledTimes(1))
  })

  it('renders the terminal notice when no flow is read and the host offers a way back', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return errorResponse(401, 'authn.flow_invalid')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const warn = captureRefreshWarning()
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onDismiss = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onDismiss={onDismiss} />,
    )

    expect(
      await screen.findByText(zhCN.bindingCallback.flowGone),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.bindingCallback.retry }),
    ).toBeNull()
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', {
          name: zhCN.bindingCallback.backToAccount,
        }),
      )
    expect(onDismiss).toHaveBeenCalledTimes(1)
    expect(
      rig.calls.filter((call) => call.path === FLOW_CURRENT),
    ).toHaveLength(1)
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('renders the terminal notice when the complete exchange is refused as flow-invalid', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return errorResponse(401, 'authn.flow_invalid')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const warn = captureRefreshWarning()
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onDismiss = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onDismiss={onDismiss} />,
    )

    expect(
      await screen.findByText(zhCN.bindingCallback.flowGone),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.bindingCallback.retry }),
    ).toBeNull()
    await userEvent
      .setup()
      .click(
        screen.getByRole('button', {
          name: zhCN.bindingCallback.backToAccount,
        }),
      )
    expect(onDismiss).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('keeps the retry when the complete exchange is refused for another reason', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FLOW_CURRENT) {
        return jsonResponse(200, {
          seq: 2,
          state: 'ready',
          next: { kind: 'complete' },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onDismiss = vi.fn()
    renderWithProviders(
      <BindingCallbackHandler session={rig.session} onDismiss={onDismiss} />,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )
    expect(
      screen.getByRole('button', { name: zhCN.bindingCallback.retry }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.bindingCallback.flowGone),
    ).not.toBeInTheDocument()
    expect(onDismiss).not.toHaveBeenCalled()
  })

  describe('a refused callback', () => {
    function refusedRig() {
      const respond: RealResponder = (call: RealCall) => {
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      return rig
    }

    it('words the conflict for this surface, reads no flow and offers only the way back', async () => {
      const rig = refusedRig()
      const onDismiss = vi.fn()
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          initialError="authn.conflict"
          onDismiss={onDismiss}
        />,
      )

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(zhCN.bindingCallback.conflict)
      expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
      expect(rig.calls).toHaveLength(0)
      expect(
        screen.queryByRole('button', { name: zhCN.bindingCallback.retry }),
      ).toBeNull()

      const back = screen.getByRole('button', {
        name: zhCN.bindingCallback.backToAccount,
      })
      expect(back).toHaveClass('MuiButton-contained')
      await userEvent.setup().click(back)
      expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('renders another known code through the surface’s existing mapping', async () => {
      const rig = refusedRig()
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          initialError="authn.login_failed"
        />,
      )

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
      expect(rig.calls).toHaveLength(0)
      expect(
        screen.queryByRole('button', { name: zhCN.bindingCallback.retry }),
      ).toBeNull()
    })

    it('keeps the generic text for a code outside the surface’s vocabulary', async () => {
      const rig = refusedRig()
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          initialError="authn.future_code"
        />,
      )

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(zhCN.errors.unknown)
      expect(rig.calls).toHaveLength(0)
    })

    it('offers no action when the host gives no dismiss callback', async () => {
      const rig = refusedRig()
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          initialError="authn.conflict"
        />,
      )

      await screen.findByRole('alert')
      expect(screen.queryByRole('button')).toBeNull()
    })
  })

  describe('the absent slot', () => {
    const METHODS_PATH = '/api/v1/auth/methods'

    function methodsDoc(kinds: readonly string[]): Record<string, unknown> {
      return {
        methods: kinds.map((kind) => ({ name: kind, kind })),
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: {
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        },
      }
    }

    it('renders the host view when no flow is bound and no redirect method exists', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === FLOW_CURRENT) {
          return errorResponse(401, 'authn.flow_invalid')
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc(['password']))
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const warn = captureRefreshWarning()
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          absent={<p>No such page</p>}
        />,
      )

      expect(await screen.findByText('No such page')).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({
          status: 401,
          code: 'authn.flow_invalid',
          trace_id: 'trace-1',
        }),
      )
      expect(warn).toHaveBeenCalledTimes(1)
    })

    it('keeps the failure banner when a redirect method exists', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === FLOW_CURRENT) {
          return errorResponse(401, 'authn.flow_invalid')
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc(['redirect']))
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const warn = captureRefreshWarning()
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          absent={<p>No such page</p>}
        />,
      )

      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(screen.queryByText('No such page')).not.toBeInTheDocument()
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({
          status: 401,
          code: 'authn.flow_invalid',
          trace_id: 'trace-1',
        }),
      )
      expect(warn).toHaveBeenCalledTimes(1)
    })

    it('keeps the failure banner when the discovery read fails', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === FLOW_CURRENT) {
          return errorResponse(401, 'authn.flow_invalid')
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return errorResponse(503, 'authn.unavailable')
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const warn = captureRefreshWarning()
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <BindingCallbackHandler
          session={rig.session}
          absent={<p>No such page</p>}
        />,
      )

      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(screen.queryByText('No such page')).not.toBeInTheDocument()
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({
          status: 401,
          code: 'authn.flow_invalid',
          trace_id: 'trace-1',
        }),
      )
      expect(warn).toHaveBeenCalledTimes(1)
    })

    it('reads no discovery document without an absent slot', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === FLOW_CURRENT) {
          return errorResponse(401, 'authn.flow_invalid')
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const warn = captureRefreshWarning()
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(<BindingCallbackHandler session={rig.session} />)

      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(
        rig.calls.some((call) => call.path === METHODS_PATH),
      ).toBe(false)
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({
          status: 401,
          code: 'authn.flow_invalid',
          trace_id: 'trace-1',
        }),
      )
      expect(warn).toHaveBeenCalledTimes(1)
    })
  })
})
