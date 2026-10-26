/**
 * NotificationPreferences at the 375px floor in a real browser: the English
 * "In-app" column header must sit on one line rather than break at its
 * hyphen. jsdom lays nothing out, so only the browser can see the header's
 * line boxes; the case asks a Range over the header's text for its client
 * rects and requires them all to land in one vertical band.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import Box from '@mui/material/Box'
import { page } from 'vitest/browser'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { NotificationPreferences } from './NotificationPreferences.js'
import { NotificationPresenceProvider } from './NotificationPresence.js'

const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

/** One declared type the matrix lists, with every channel served. */
const TYPE = {
  key: 'billing.invoice.due',
  group: 'billing',
  channels: ['in_app', 'email', 'sms'],
}

/** The matrix read: one type, every channel for that type. */
const responder: RealResponder = (call: RealCall) => {
  if (call.path === TYPES_PATH && call.method === 'GET') {
    return jsonResponse(200, {
      types: [
        {
          key: TYPE.key,
          group: TYPE.group,
          default_channels: TYPE.channels,
          unsubscribable: true,
          recipient_visible_params: [],
        },
      ],
    })
  }
  if (call.path === PREFERENCES_PATH && call.method === 'GET') {
    return jsonResponse(200, {
      preferences: [
        {
          type_key: TYPE.key,
          group: TYPE.group,
          channels: TYPE.channels,
          explicit: true,
        },
      ],
    })
  }
  return jsonResponse(404, { code: 'notification.bad_request' })
}

describe('NotificationPreferences column headers at 375 in Chromium', () => {
  it('renders the in-app header on one line', async () => {
    await page.viewport(375, 900)
    const rig = makeRealClientRigWithoutSession(responder)
    rig.store.set('access-1')
    // The tenant shell gives page content the theme's spacing-3 inset on
    // each side; reproduce it so the matrix meets the content width the
    // account page actually offers at this viewport.
    renderWithProviders(
      <Box sx={{ p: 3 }}>
        <NotificationPresenceProvider preferences channels={['email', 'sms']}>
          <NotificationPreferences />
        </NotificationPresenceProvider>
      </Box>,
      { language: 'en-US' },
    )

    const header = await screen.findByRole('columnheader', {
      name: enUS.preferences.channel.in_app,
    })
    await document.fonts.ready

    // One rect per line box: a header that breaks at the hyphen answers
    // with two rects on two different top edges, one that stays on one
    // line with rects sharing a single band.
    const range = document.createRange()
    range.selectNodeContents(header)
    const tops = Array.from(range.getClientRects()).map((rect) =>
      Math.round(rect.top),
    )
    expect(new Set(tops).size).toBe(1)
  })
})
