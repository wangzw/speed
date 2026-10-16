/**
 * usage-example.test.tsx -- the README quick start, compiled and run.
 *
 * The quick start wires the notification surface for a signed-in viewer:
 * the bilingual i18n instance with both namespaces registered (the
 * notification-ui namespace for this package's strings, the ui-kit
 * namespace because the surfaces compose ui-kit components), the
 * AppThemeProvider tree, a QueryClientProvider and a TenantProvider (the
 * surfaces key their reads under the acting tenant). This file is that
 * journey: it renders the bell beside the notification center over a real
 * @speed/api-client whose scripted fetch answers genuine Response objects,
 * hears the bell count, marks every message read through the center, and
 * switches the whole page to the other supported language.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { switchLanguage } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { NotificationBell } from './NotificationBell.js'
import { NotificationCenter } from './NotificationCenter.js'

const MESSAGES_PATH = '/api/v1/notifications/messages'
const READ_ALL_PATH = '/api/v1/notifications/messages/read-all'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'
const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

/** One small inbox + an empty taxonomy. */
function responder(): RealResponder {
  let rows = [
    {
      id: 'm1',
      title: 'Build finished',
      body: 'Your build succeeded.',
      read: false,
    },
    { id: 'm2', title: 'Welcome', body: 'Glad you are here.', read: false },
  ]
  return (call: RealCall) => {
    if (call.path === UNREAD_PATH) {
      return jsonResponse(200, {
        unread: rows.filter((row) => !row.read).length,
      })
    }
    if (call.path === READ_ALL_PATH && call.method === 'POST') {
      const count = rows.length
      rows = rows.map((row) => ({ ...row, read: true }))
      return jsonResponse(200, { read_count: count })
    }
    if (call.path === MESSAGES_PATH && call.method === 'GET') {
      return jsonResponse(200, {
        messages: rows.map((row) => ({
          id: row.id,
          type_key: 'demo.thing.happened',
          group: 'demo',
          title: row.title,
          body: row.body,
          params: null,
          link: '',
          read_at: row.read ? '2026-08-01T02:00:00.000Z' : null,
          created_at: '2026-08-01T01:00:00.000Z',
        })),
        total: rows.length,
      })
    }
    if (call.path === PREFERENCES_PATH && call.method === 'GET') {
      return jsonResponse(200, { preferences: [] })
    }
    if (call.path === TYPES_PATH && call.method === 'GET') {
      return jsonResponse(200, { types: [] })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

describe('notification surface quick start', () => {
  it('reads the count, marks all read, and switches language', async () => {
    const rig = makeRealClientRigWithoutSession(responder())
    rig.store.set('access-1')
    const { i18n } = renderWithProviders(
      <div>
        <NotificationBell onClick={() => undefined} />
        <NotificationCenter />
      </div>,
    )

    // The bell reads the unread count.
    expect(
      await screen.findByRole('button', {
        name: zhCN.bell.labelWithCount.replace('{{n}}', '2'),
      }),
    ).toBeInTheDocument()

    // The center lists the messages and marks them all read.
    expect(await screen.findByText('Build finished')).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    expect(
      await screen.findByText(zhCN.inbox.markAllReadDone.replace('{{n}}', '2')),
    ).toBeInTheDocument()
    // The bell follows to zero.
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.bell.label }),
      ).toBeInTheDocument(),
    )

    // The whole page re-renders in the other language.
    await switchLanguage(i18n, 'en-US')
    expect(
      await screen.findByRole('button', { name: enUS.bell.label }),
    ).toBeInTheDocument()
    expect(screen.getByText(enUS.inbox.title)).toBeInTheDocument()

    await expectNoAxeViolations()
  })
})
