/**
 * NotificationInbox in a real browser: axe's label-content-name-mismatch
 * (WCAG 2.5.3 Label in Name) over an unread row's "mark as read" control.
 *
 * The rule reads a control's visible text through the canvas text-measurement
 * API; jsdom answers `getContext('2d')` with null, so the check throws there
 * and comes back indeterminate for every node. Only a real browser can pass or
 * fail it. The control shows "Mark as read" and carries a longer accessible
 * name that also identifies the message; the longer-name wording must keep the
 * visible text contiguous so it stays inside the name.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import axe from 'axe-core'
import { page } from 'vitest/browser'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { NotificationInbox } from './NotificationInbox.js'

const MESSAGES_PATH = '/api/v1/notifications/messages'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'
const TITLE = 'Build finished'

/** Answers the inbox read with one unread message, and the bell's count. */
const respond: RealResponder = (call) => {
  if (call.path === UNREAD_PATH) {
    return jsonResponse(200, { unread: 1 })
  }
  if (call.path === MESSAGES_PATH) {
    return jsonResponse(200, {
      messages: [
        {
          id: 'm1',
          type_key: 'demo.thing.happened',
          group: 'demo',
          title: TITLE,
          body: '',
          params: null,
          link: '',
          read_at: null,
          created_at: '2026-08-01T01:00:00.000Z',
        },
      ],
      total: 1,
    })
  }
  return jsonResponse(404, { code: 'notification.bad_request' })
}

describe('NotificationInbox row action Label in Name in Chromium', () => {
  it('keeps the visible mark-read text inside the accessible name', async () => {
    await page.viewport(1000, 900)
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox />, { language: 'en-US' })

    const markRead = await screen.findByRole('button', {
      name: enUS.inbox.markReadAria.replace('{{title}}', TITLE),
    })
    const result = await axe.run(markRead, {
      runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
    })
    expect(result.incomplete).toEqual([])
    expect(result.violations).toEqual([])
  })
})
