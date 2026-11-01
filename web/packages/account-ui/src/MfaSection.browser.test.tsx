/**
 * The factor row's overflow menu, driven in a real Chromium.
 *
 * The removal is reached the way a reader reaches it -- open the row's "⋯"
 * menu, choose remove, confirm -- and the case pins what the grouped
 * surface owes the reader after the write lands: the row leaves the list,
 * the group's own action word follows whether the group still has an
 * enabled row, and the success feedback names the row that went. jsdom
 * cannot tell the menu's portal and its focus order from a plain list, so
 * this interaction lives in the browser tier.
 *
 * The regression is pinned through the component and the real request
 * client: no fixture is written into the query cache, and the deletion's
 * answer is what the list reread observes.
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

/** The one enabled passkey row the case removes. */
const ROW_NAME = 'This Mac'

/** The single spacing token an action row puts between adjacent controls. */
const SPACING_UNIT = defaultTokens.spacing.unit

/** One pixel of tolerance for subpixel layout rounding. */
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
          human: { required: false, provider: 'off' },
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
                  created_at: '',
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
    // The group already holds an enabled passkey, so its action offers
    // another device rather than a plain add.
    const manage = await screen.findByRole('button', {
      name: zhCN.mfa.manageAria.replace('{{method}}', method),
    })
    expect(manage.textContent).toBe(zhCN.mfa.action.manage)

    // The removal is reached through the row's overflow menu.
    await user.click(
      screen.getByRole('button', {
        name: zhCN.mfa.actionsAria.replace('{{factor}}', ROW_NAME),
      }),
    )
    await user.click(
      await screen.findByRole('menuitem', { name: zhCN.mfa.remove }),
    )

    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.mfa.removeConfirm.confirmLabel,
      }),
    )

    // The landed write takes the row out of its group and names it in the
    // success feedback.
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
        zhCN.mfa.removed.replace('{{factor}}', ROW_NAME),
      ),
    ).toBeTruthy()
    // No enabled passkey is left, so the group's action falls back to a
    // plain add.
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
  /**
   * The editor is the row's in-place edit: the name field and the action
   * row share one line, with the actions starting right after the field,
   * so opening the editor does not push the actions onto a new line and
   * change the list's height. jsdom lays nothing out, so the geometry is
   * measured here.
   *
   * Which assertions discriminate: the shared line (the action row starts
   * one spacing token past the field's right edge, on the field's own top)
   * fails on the full-width-field-then-actions-below layout this change
   * replaces. The group's adjacency and order hold on both layouts -- the
   * action row itself did not move -- so those are a regression nail for
   * the group, not a discriminator of this fix.
   */
  it('keeps the name field and its actions on one row, cancel left of save', async () => {
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
          mfa: { factors: ['totp'] },
          human: { required: false, provider: 'off' },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            {
              id: 'f-totp',
              kind: 'totp',
              label: rowLabel,
              status: 'active',
              created_at: '',
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

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.mfa.actionsAria.replace('{{factor}}', rowLabel),
      }),
    )
    await user.click(
      await screen.findByRole('menuitem', { name: zhCN.mfa.rename }),
    )

    const field = await screen.findByLabelText(zhCN.mfa.renameLabel)
    const cancel = screen.getByRole('button', { name: zhCN.mfa.renameCancel })
    const save = screen.getByRole('button', { name: zhCN.mfa.renameSave })
    await waitFor(() =>
      expect(field.getBoundingClientRect().width).toBeGreaterThan(0),
    )
    await document.fonts.ready

    const control = field.closest<HTMLElement>('.MuiFormControl-root')
    const row = save.closest<HTMLElement>('[data-action-row]')
    expect(control).not.toBeNull()
    expect(row).not.toBeNull()
    const controlBox = (control as HTMLElement).getBoundingClientRect()
    const rowBox = (row as HTMLElement).getBoundingClientRect()
    const cancelBox = cancel.getBoundingClientRect()
    const saveBox = save.getBoundingClientRect()

    // One line: the actions follow the field's right edge by the form's one
    // spacing token, on the field's own top instead of under it.
    expect(
      Math.abs(rowBox.left - controlBox.right - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(rowBox.top - controlBox.top)).toBeLessThanOrEqual(TOLERANCE)

    // One group, never two ends: cancel sits left of save, the two are
    // exactly one spacing token apart, and the row hugs them.
    expect(cancelBox.left).toBeLessThan(saveBox.left)
    expect(Math.abs(cancelBox.top - saveBox.top)).toBeLessThanOrEqual(TOLERANCE)
    expect(saveBox.left).toBeGreaterThanOrEqual(cancelBox.right)
    expect(
      Math.abs(saveBox.left - cancelBox.right - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(cancelBox.left - rowBox.left)).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(saveBox.right - rowBox.right)).toBeLessThanOrEqual(TOLERANCE)
  })
})
