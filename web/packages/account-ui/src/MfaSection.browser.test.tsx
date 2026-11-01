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
