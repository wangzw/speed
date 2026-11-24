/**
 * MfaSection in a real browser: the factor row's remove action and its success text.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { defaultTokens } from '@speed/tokens'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { MfaSection } from './MfaSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'
const FACTOR_PATH = '/api/v1/auth/factors/f-passkey'

const ROW_NAME = 'This Mac'

const SPACING_UNIT = defaultTokens.spacing.unit

const TOLERANCE = 0.5

describe('MfaSection row overflow menu', () => {
  it('removes a factor from its row menu and updates the group action', async () => {
    await page.viewport(1100, 900)
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['webauthn'] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: removed
            ? []
            : [
                {
                  id: 'f-passkey',
                  kind: 'webauthn',
                  label: ROW_NAME,
                  status: 'active',
                  created_at: null,
                },
              ],
        })
      }
      if (call.method === 'DELETE' && call.path === FACTOR_PATH) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<MfaSection session={rig.session} />)
    const user = userEvent.setup()

    const method = zhCN.mfa.factor.kind.webauthn
    const initialAdd = await screen.findByRole('button', {
      name: zhCN.mfa.addAria.replace('{{method}}', method),
    })
    expect(initialAdd.textContent).toBe(zhCN.mfa.action.add)

    const row = (await screen.findByText(ROW_NAME, { exact: true })).closest('li')
    expect(row).not.toBeNull()
    await user.click(
      within(row as HTMLElement).getByRole('button', {
        name: zhCN.mfa.remove,
      }),
    )

    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    await waitFor(() => {
      expect(
        within(screen.getByRole('region', { name: method })).queryByText(
          ROW_NAME,
          { exact: true },
        ),
      ).toBeNull()
    })
    expect(
      await screen.findByText(
        zhCN.mfa.removedLast.replace('{{factor}}', ROW_NAME),
      ),
    ).toBeTruthy()
    const add = await screen.findByRole('button', {
      name: zhCN.mfa.addAria.replace('{{method}}', method),
    })
    expect(add.textContent).toBe(zhCN.mfa.action.add)
    expect(
      rig.calls.filter(
        (call) => call.method === 'DELETE' && call.path === FACTOR_PATH,
      ),
    ).toHaveLength(1)
  })
})

describe('MfaSection rename editor', () => {
  it('stacks the name field above its actions, closed on the right edge', async () => {
    await page.viewport(1100, 900)
    const rowLabel = 'Daily codes'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['webauthn'] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            {
              id: 'f-passkey',
              kind: 'webauthn',
              label: rowLabel,
              status: 'active',
              created_at: null,
            },
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<MfaSection session={rig.session} />)
    const user = userEvent.setup()

    const factorRowElement = (
      await screen.findByText(rowLabel, { exact: true })
    ).closest('li')
    expect(factorRowElement).not.toBeNull()
    await user.click(
      within(factorRowElement as HTMLElement).getByRole('button', {
        name: zhCN.mfa.rename,
      }),
    )

    const field = await screen.findByLabelText(zhCN.mfa.renameLabel)
    const cancel = screen.getByRole('button', { name: zhCN.mfa.renameCancel })
    const save = screen.getByRole('button', { name: zhCN.mfa.renameSave })
    await waitFor(() =>
      expect(field.getBoundingClientRect().width).toBeGreaterThan(0),
    )
    await document.fonts.ready

    const control = field.closest<HTMLElement>('.MuiFormControl-root')
    const form = field.closest('form')
    const row = save.closest<HTMLElement>('[data-action-row]')
    expect(control).not.toBeNull()
    expect(form).not.toBeNull()
    expect(row).not.toBeNull()
    const controlBox = (control as HTMLElement).getBoundingClientRect()
    const formBox = (form as HTMLFormElement).getBoundingClientRect()
    const rowBox = (row as HTMLElement).getBoundingClientRect()
    const cancelBox = cancel.getBoundingClientRect()
    const saveBox = save.getBoundingClientRect()

    expect(
      Math.abs(controlBox.width - formBox.width),
    ).toBeLessThanOrEqual(TOLERANCE)

    expect(rowBox.top).toBeGreaterThanOrEqual(controlBox.bottom - TOLERANCE)

    expect(cancelBox.left).toBeLessThan(saveBox.left)
    expect(Math.abs(cancelBox.top - saveBox.top)).toBeLessThanOrEqual(TOLERANCE)
    expect(saveBox.left).toBeGreaterThanOrEqual(cancelBox.right)
    expect(
      Math.abs(saveBox.left - cancelBox.right - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(saveBox.right - formBox.right)).toBeLessThanOrEqual(TOLERANCE)
  })
})
