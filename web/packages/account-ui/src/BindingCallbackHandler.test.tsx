/**
 * BindingCallbackHandler behaviour: the link flow read back from the
 * callback route (GET /api/v1/auth/flows/current) and completed (POST
 * /api/v1/auth/flows/answer).
 *
 * What this suite pins: a ready flow is taken with `step: 'complete'`,
 * the `link` completion (state 'linked') fires onBound exactly once and
 * never signs anyone in; a flow still parked on a step holds the pending
 * notice (the handler waits for the provider's callback to land); and a
 * read that failed renders its code text under a retry that re-reads the
 * flow.
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

/**
 * A host that re-renders on demand with fresh inline props, the way the app
 * shell does when an unrelated read lands (a profile refetch, a notification).
 * The handler is a child of this host, so the re-render hands it a new
 * `onBound` and a new `absent` element without remounting it.
 */
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
    // The done stage reports the binding success, naming the bound object,
    // in a polite status region -- a host that stays on the route is not
    // left on the pending notice.
    const done = await screen.findByText(zhCN.bindingCallback.done)
    expect(done).toBeTruthy()
    expect(done.closest('[role="status"]')).not.toBeNull()
    expect(zhCN.bindingCallback.done).not.toBe(zhCN.bindingCallback.pending)
    expect(enUS.bindingCallback.done).toMatch(/social account/i)
    const answer = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    // The ready flow is taken with the sequence number the server sent.
    expect(answer?.body).toEqual({ seq: 2, step: 'complete' })
  })

  it('fills the done region after it is mounted, so the confirmation is announced', async () => {
    // The success region stands in the tree from the first render; the
    // confirmation fills an already-mounted polite region rather than
    // arriving in the commit that mounts it -- a region born with its text
    // is not announced.
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

    // While the flow is still being read, the pending notice occupies its
    // own status region and the done scope's region is already there, empty.
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
    // The same region now carries the confirmation: it was not born with it.
    expect(doneRegion).toHaveTextContent(zhCN.bindingCallback.done)
  })

  it('keeps the done confirmation across a language switch without re-reading the flow', async () => {
    // The flow is taken once. `t` changing identity on a language switch
    // must not re-arm the read effect: the flow is gone by then, and a
    // second read would replace the confirmation with the failure banner.
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

    // The success message is the translation snapshot taken at settle time;
    // the switch neither rewrites it nor re-reads the flow it already took.
    expect(screen.getByText(zhCN.bindingCallback.done)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(reads).toBe(1)
  })

  it('takes the flow once when the host re-renders with a new callback', async () => {
    // A host passes its callbacks inline, so any parent re-render hands the
    // handler new `onBound` and `absent` props. That must not re-arm the read
    // effect: the flow was taken once and is gone, and a second read would
    // even try to complete it again.
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

    // The unrelated re-render: the host re-renders itself with a fresh
    // inline callback and a fresh absent element.
    await user.click(screen.getByRole('button', { name: /rerender/ }))
    // Give a re-armed read effect time to issue its request.
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

    // The parked challenge step is rendered and answered under the
    // flow's own first-factor answer step.
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

  describe('the absent slot', () => {
    const METHODS_PATH = '/api/v1/auth/methods'

    /** A GET /api/v1/auth/methods document carrying the given method kinds. */
    function methodsDoc(kinds: readonly string[]): Record<string, unknown> {
      return {
        methods: kinds.map((kind) => ({ name: kind, kind })),
        signup: { enabled: false, password: { enabled: false } },
        mfa: { factors: [] },
        human: { required: false, provider: 'off' },
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

      // Nothing can reach this callback: the deployment offers no redirect
      // method, so the host's 404 answers instead of a flow-not-found banner.
      expect(await screen.findByText('No such page')).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      // The refused read's 401 is the expected refresh-failure diagnostic.
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({
          status: 401,
          code: 'authn.flow_invalid',
          trace_id: 'trace-1',
        }),
      )
      // One refused read, one refresh failure: a duplicated read would warn
      // again.
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
      // One refused read, one refresh failure: a duplicated read would warn
      // again.
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

      // An unreadable discovery document cannot settle "no binding exists";
      // the retryable banner is the honest answer.
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
      // One refused read, one refresh failure: a duplicated read would warn
      // again.
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
      // One refused read, one refresh failure: a duplicated read would warn
      // again.
      expect(warn).toHaveBeenCalledTimes(1)
    })
  })
})
