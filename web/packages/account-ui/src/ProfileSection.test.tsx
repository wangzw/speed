/**
 * ProfileSection behaviour: the display-name surface over the profile
 * read (GET /api/v1/auth/me) and the profile write (PATCH /api/v1/auth/me).
 *
 * What this suite pins: the stored display name renders into the field; a
 * save PATCHes a one-field body under `display_name`, fills the success
 * live region and refetches the profile; a refused save renders its code
 * text in the banner; a save refused with 401
 * authn.insufficient_user_authentication opens the step-up dialog, and a
 * won step-up re-runs that one save exactly once; and a load that failed
 * renders the error state with a retry that refetches.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { ProfileSection } from './ProfileSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function mount(respond: RealResponder) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<ProfileSection session={rig.session} />)
  return { rig, ...rendered }
}

describe('ProfileSection', () => {
  it('renders the stored display name and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Grace Hopper' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.profile.title }),
    ).toBeTruthy()
    expect(
      await screen.findByRole('textbox', { name: zhCN.profile.displayName.label }),
    ).toHaveValue('Grace Hopper')

    await expectNoAxeViolations()
  })

  it('PATCHes the display name, reports it and refetches the profile', async () => {
    let stored = 'Ada'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        stored = (call.body as { display_name: string }).display_name
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.profile.saved),
    )
    const patch = rig.calls.find((call) => call.method === 'PATCH')
    expect(patch?.body).toEqual({ display_name: 'Grace' })
    // The read converges on the account's own answer.
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === PROFILE_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
  })

  it('renders a refused save as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
  })

  it('disables the save button while the field still holds the account name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const save = await screen.findByRole('button', { name: zhCN.profile.save })
    // Nothing changed yet: the save is inert, so a stray click cannot fire a
    // no-op PATCH.
    expect(save).toBeDisabled()

    const field = screen.getByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.type(field, '!')
    expect(save).toBeEnabled()

    // Restoring the account's own name disables it again.
    await user.clear(field)
    await user.type(field, 'Ada')
    expect(save).toBeDisabled()
  })

  it('gates a step_up_required save behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    let stored = 'Ada'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        stored = (call.body as { display_name: string }).display_name
        return jsonResponse(200, {})
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    // The 401 opens the step-up dialog; winning it re-runs the save.
    await screen.findByRole('button', { name: 'App' })
    await user.click(screen.getByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.profile.saved),
    )
    const patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches).toHaveLength(2)
    expect(patches[1]?.body).toEqual({ display_name: 'Grace' })
    // The step-up shortfall's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    // One shortfall, one refresh failure: a duplicated save would warn again.
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('renders the error state with a retry that refetches when the load fails', async () => {
    const user = userEvent.setup()
    let failLoad = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return failLoad
          ? errorResponse(503, 'client.network')
          : jsonResponse(200, profileBody({ display_name: 'Grace' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.profile.error.title)).toBeTruthy()

    failLoad = false
    await user.click(screen.getByRole('button', { name: zhCN.profile.retry }))
    expect(
      await screen.findByRole('textbox', { name: zhCN.profile.displayName.label }),
    ).toHaveValue('Grace')
  })
})
