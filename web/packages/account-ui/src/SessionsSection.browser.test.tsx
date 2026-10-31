/**
 * SessionsSection in a real browser: the action column's header text must
 * end on the same vertical line as the row action's glyph. jsdom lays
 * nothing out, so only Chromium can see the two edges; the case asks a
 * Range over the header's text for its right edge and compares it with
 * the sign-out icon's rendered box, both from getBoundingClientRect.
 *
 * The icon button carries its own inner inset (a compact MUI IconButton
 * pads its glyph), so a header whose cell ends where the button ends
 * leaves the header text a few pixels to the right of the glyph it
 * labels. The cell's right padding accounts for that inset.
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

const SESSIONS_PATH = '/api/v1/auth/sessions'
const PROFILE_PATH = '/api/v1/auth/me'

/** One live session the account can sign out: a row with its action, and
 * the profile read the timezone chain issues beside the list. */
const responder: RealResponder = (call: RealCall) => {
  if (call.path === PROFILE_PATH) {
    return jsonResponse(200, { time_zone: '' })
  }
  if (call.method === 'GET' && call.path === SESSIONS_PATH) {
    return jsonResponse(200, {
      sessions: [
        {
          id: 'session-2',
          kind: 'browser',
          created_at: '2026-08-01T02:05:00.000Z',
          last_seen_at: '2026-08-01T02:05:00.000Z',
          expires_at: '2026-09-01T02:05:00.000Z',
          client_ip: '198.51.100.4',
          user_agent: '',
          current: false,
        },
      ],
    })
  }
  return jsonResponse(404, { code: 'client.not_found' })
}

/** The revoke aria-label the component builds for a row's device label. */
function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

describe('SessionsSection action column alignment in Chromium', () => {
  it("ends the header text on the row action glyph's right edge", async () => {
    await page.viewport(900, 900)
    const rig = makeRealClientRigWithoutSession(responder)
    rig.store.set('access-1')
    renderWithProviders(<SessionsSection />)

    const header = await screen.findByRole('columnheader', {
      name: zhCN.sessions.table.action,
    })
    const revoke = await screen.findByRole('button', {
      name: revokeAria(zhCN.sessions.deviceUnknown),
    })
    const glyph = revoke.querySelector('svg')
    expect(glyph).not.toBeNull()
    await document.fonts.ready

    // The header's text is the cell's only content, so a Range over it
    // answers where the right-aligned text itself ends -- not where the
    // cell's padding box ends, which is what the cell's own rect would
    // report.
    const range = document.createRange()
    range.selectNodeContents(header)
    const headerTextRight = range.getBoundingClientRect().right
    const glyphRight = (glyph as SVGSVGElement).getBoundingClientRect().right

    // Only subpixel jitter is allowed: the two edges are placed by the
    // same layout pass, so a real gap reads as whole pixels.
    expect(
      Math.abs(headerTextRight - glyphRight),
      `header=${headerTextRight} glyph=${glyphRight}`,
    ).toBeLessThanOrEqual(0.5)
  })
})
