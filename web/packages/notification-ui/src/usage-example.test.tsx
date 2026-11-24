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
import { NotificationPresenceProvider } from './NotificationPresence.js'

const MESSAGES_PATH = '/api/v1/notifications/messages'
const READ_ALL_PATH = '/api/v1/notifications/messages/read-all'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'
const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

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
      <NotificationPresenceProvider preferences channels={['email', 'sms']}>
        <div>
          <NotificationBell onClick={() => undefined} />
          <NotificationCenter />
        </div>
      </NotificationPresenceProvider>,
    )

    expect(
      await screen.findByRole('button', {
        name: zhCN.bell.labelWithCount.replace('{{n}}', '2'),
      }),
    ).toBeInTheDocument()

    expect(await screen.findByText('Build finished')).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    expect(
      await screen.findByText(zhCN.inbox.markAllReadDone.replace('{{n}}', '2')),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.bell.label }),
      ).toBeInTheDocument(),
    )

    await switchLanguage(i18n, 'en-US')
    expect(
      await screen.findByRole('button', { name: enUS.bell.label }),
    ).toBeInTheDocument()
    expect(screen.getByText(enUS.inbox.title)).toBeInTheDocument()

    await expectNoAxeViolations()
  })
})
