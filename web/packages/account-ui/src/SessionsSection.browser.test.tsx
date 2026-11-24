/**
 * SessionsSection in a real browser: every row's sign-out action closes on one right rail.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { SessionsSection } from './SessionsSection.js'
import { summarizeUserAgent } from './internal/ua-summary.js'

const SESSIONS_PATH = '/api/v1/auth/sessions'
const PROFILE_PATH = '/api/v1/auth/me'

const DESKTOP_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36'
const CLI_UA = 'curl/8.4.0'

function session(id: string, userAgent: string) {
  return {
    id,
    kind: 'browser',
    created_at: '2026-08-01T02:05:00.000Z',
    last_seen_at: '2026-08-01T02:05:00.000Z',
    expires_at: '2026-09-01T02:05:00.000Z',
    client_ip: '198.51.100.4',
    user_agent: userAgent,
    current: false,
  }
}

const responder: RealResponder = (call: RealCall) => {
  if (call.path === PROFILE_PATH) {
    return jsonResponse(200, { time_zone: '' })
  }
  if (call.method === 'GET' && call.path === SESSIONS_PATH) {
    return jsonResponse(200, {
      sessions: [session('session-2', DESKTOP_UA), session('session-3', CLI_UA)],
    })
  }
  return jsonResponse(404, { code: 'client.not_found' })
}

function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

function deviceOf(userAgent: string): string {
  const label = summarizeUserAgent(userAgent)
  if (label === null) {
    throw new Error(`no device label for ${userAgent}`)
  }
  return label
}

describe('SessionsSection action rail in Chromium', () => {
  it('ends every row action on the list content edge', async () => {
    await page.viewport(900, 900)
    const rig = makeRealClientRigWithoutSession(responder)
    rig.store.set('access-1')
    renderWithProviders(<SessionsSection />)

    const first = await screen.findByRole('button', {
      name: revokeAria(deviceOf(DESKTOP_UA)),
    })
    const second = await screen.findByRole('button', {
      name: revokeAria(deviceOf(CLI_UA)),
    })
    expect(first.querySelector('svg')).not.toBeNull()
    expect(second.querySelector('svg')).not.toBeNull()
    expect(first).toHaveTextContent(zhCN.sessions.revokeRowLabel)
    expect(second).toHaveTextContent(zhCN.sessions.revokeRowLabel)
    await document.fonts.ready

    const firstRight = first.getBoundingClientRect().right
    const secondRight = second.getBoundingClientRect().right
    expect(
      Math.abs(firstRight - secondRight),
      `desktop=${firstRight} cli=${secondRight}`,
    ).toBeLessThanOrEqual(0.5)

    const list = screen.getByRole('list')
    const listRight = list.getBoundingClientRect().right
    const buttonRight = first.getBoundingClientRect().right
    expect(
      Math.abs(buttonRight - listRight),
      `button=${buttonRight} list=${listRight}`,
    ).toBeLessThanOrEqual(0.5)
  })

  it('lands focus on the list with a shell drawer dialog mounted', async () => {
    let revoked = false
    const bulkResponder: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return jsonResponse(200, { time_zone: '' })
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            { ...session('session-current', ''), current: true },
            ...(revoked
              ? []
              : [session('session-2', DESKTOP_UA), session('session-3', CLI_UA)]),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === SESSIONS_PATH) {
        revoked = true
        return jsonResponse(200, { revoked: 2 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRigWithoutSession(bulkResponder)
    rig.store.set('access-1')
    renderWithProviders(
      <>
        <div role="dialog" aria-label="shell drawer" />
        <SessionsSection />
      </>,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.sessions.revokeOthers.label,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.sessions.revokeOthers.confirmTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: zhCN.sessions.revokeOthers.label,
        }),
      ).toBeNull(),
    )
    const list = await screen.findByRole('list', {
      name: zhCN.sessions.title,
    })
    await waitFor(() => expect(document.activeElement).toBe(list))
  })
})
