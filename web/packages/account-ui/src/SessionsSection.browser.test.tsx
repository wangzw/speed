/**
 * SessionsSection in a real browser: every row's sign-out action closes on
 * one right rail. The rows are a list, not a table, so there is no header
 * cell to line up with; what a reader sees is the action column of the
 * rows -- a short device name and a long one -- ending on the same vertical
 * line, and that line is the list's own content edge. jsdom lays nothing
 * out, so only Chromium can tell the rail from a per-row shrink-wrap.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
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

/** A desktop browser and a command-line client: two device names of very
 * different lengths, so a per-row shrink-wrapped action column would put
 * their glyphs on different lines. */
const DESKTOP_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36'
const CLI_UA = 'curl/8.4.0'

/** One live session the account can sign out, with its own device name. */
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

/** The revoke aria-label the component builds for a row's device label. */
function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

/** The device label a row renders for one raw User-Agent. */
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
    const firstGlyph = first.querySelector('svg')
    const secondGlyph = second.querySelector('svg')
    expect(firstGlyph).not.toBeNull()
    expect(secondGlyph).not.toBeNull()
    await document.fonts.ready

    // One right rail: the two glyphs end on the same vertical line however
    // long their rows' device names are.
    const firstRight =
      (firstGlyph as SVGSVGElement).getBoundingClientRect().right
    const secondRight =
      (secondGlyph as SVGSVGElement).getBoundingClientRect().right
    expect(
      Math.abs(firstRight - secondRight),
      `desktop=${firstRight} cli=${secondRight}`,
    ).toBeLessThanOrEqual(0.5)

    // And that rail is the list's content edge: the action closes its row
    // flush with the list, not a shrink-wrapped island inside it.
    const list = screen.getByRole('list')
    const listRight = list.getBoundingClientRect().right
    const buttonRight = first.getBoundingClientRect().right
    expect(
      Math.abs(buttonRight - listRight),
      `button=${buttonRight} list=${listRight}`,
    ).toBeLessThanOrEqual(0.5)
  })
})
