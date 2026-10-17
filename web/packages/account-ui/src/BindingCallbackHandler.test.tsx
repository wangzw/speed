/**
 * BindingCallbackHandler behaviour: the link flow read back from the
 * callback route (GET /auth/flows/current) and completed (POST
 * /auth/flows/answer).
 *
 * What this suite pins: a ready flow is taken with `step: 'complete'`,
 * the `link` completion (state 'linked') fires onBound exactly once and
 * never signs anyone in; a flow still parked on a step holds the pending
 * notice (the handler waits for the provider's callback to land); and a
 * read that failed renders its code text under a retry that re-reads the
 * flow.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

const FLOW_CURRENT = '/auth/flows/current'
const FLOW_ANSWER = '/auth/flows/answer'

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
    const answer = rig.calls.find(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    // The ready flow is taken with the sequence number the server sent.
    expect(answer?.body).toEqual({ seq: 2, step: 'complete' })
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
    await user.type(await screen.findByLabelText('code'), '123456')
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
    const METHODS_PATH = '/auth/methods'

    /** A GET /auth/methods document carrying the given method kinds. */
    function methodsDoc(kinds: readonly string[]): Record<string, unknown> {
      return {
        methods: kinds.map((kind) => ({ name: kind, kind })),
        signup: { enabled: false },
        mfa: { factors: [] },
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
