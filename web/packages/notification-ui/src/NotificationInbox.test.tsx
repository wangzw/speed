/**
 * NotificationInbox behaviour over the generated inbox operations: the
 * caller's messages render (title, body, unread chip), an unread row's
 * "mark as read" POSTs that message's read route and the list re-reads
 * with it marked, the header action POSTs read-all and reports the
 * answer's count, an empty answer renders the empty placeholder, a
 * failed read renders the retryable error placeholder, and the whole
 * surface passes axe.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { NotificationInbox } from './NotificationInbox.js'

const MESSAGES_PATH = '/api/v1/notifications/messages'
const READ_ALL_PATH = '/api/v1/notifications/messages/read-all'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

interface Row {
  readonly id: string
  readonly title: string
  readonly body: string
  read: boolean
}

function message(row: Row): Record<string, unknown> {
  return {
    id: row.id,
    type_key: 'demo.thing.happened',
    group: 'demo',
    title: row.title,
    body: row.body,
    params: null,
    link: '',
    // The server's own shape: an unread message carries read_at as JSON
    // null (its view's *time.Time has no omitempty), a read one as an
    // instant string.
    read_at: row.read ? '2026-08-01T02:00:00.000Z' : null,
    created_at: '2026-08-01T01:00:00.000Z',
  }
}

/** A stateful inbox the responder reads and mutates. */
function inboxResponder(rows: Row[]): RealResponder {
  const unread = () => rows.filter((row) => !row.read).length
  return (call: RealCall) => {
    if (call.path === UNREAD_PATH) {
      return jsonResponse(200, { unread: unread() })
    }
    if (call.path === READ_ALL_PATH && call.method === 'POST') {
      const count = unread()
      for (const row of rows) {
        row.read = true
      }
      return jsonResponse(200, { read_count: count })
    }
    const readMatch = /^\/api\/v1\/notifications\/messages\/(.+)\/read$/.exec(
      call.path,
    )
    if (readMatch !== null && call.method === 'POST') {
      const id = readMatch[1]
      const row = rows.find((candidate) => candidate.id === id)
      if (row !== undefined) {
        row.read = true
      }
      return new Response(null, { status: 204 })
    }
    if (call.path === MESSAGES_PATH && call.method === 'GET') {
      return jsonResponse(200, {
        messages: rows.map(message),
        total: rows.length,
      })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

function mount(rows: Row[]) {
  const rig = makeRealClientRigWithoutSession(inboxResponder(rows))
  rig.store.set('access-1')
  return { rig, ...renderWithProviders(<NotificationInbox />) }
}

describe('NotificationInbox', () => {
  it('renders the messages, marks one read, and passes axe', async () => {
    const { rig } = mount([
      { id: 'm1', title: 'Build finished', body: 'The build succeeded.', read: false },
      { id: 'm2', title: 'Welcome', body: '', read: true },
    ])
    expect(await screen.findByText('Build finished')).toBeInTheDocument()
    expect(screen.getByText('The build succeeded.')).toBeInTheDocument()
    // The unread row offers the action; the read row does not.
    const markRead = screen.getByRole('button', {
      name: zhCN.inbox.markReadAria.replace('{{title}}', 'Build finished'),
    })
    expect(
      screen.queryByRole('button', {
        name: zhCN.inbox.markReadAria.replace('{{title}}', 'Welcome'),
      }),
    ).toBeNull()
    await userEvent.click(markRead)
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === `${MESSAGES_PATH}/m1/read`,
        ),
      ).toBe(true),
    )
    // The list re-read: the unread chip is gone from the first row.
    await waitFor(() => expect(screen.queryAllByText(zhCN.inbox.unread)).toHaveLength(0))
    await expectNoAxeViolations()
  })

  it('marks every unread message read and reports the count', async () => {
    const { rig } = mount([
      { id: 'm1', title: 'One', body: '', read: false },
      { id: 'm2', title: 'Two', body: '', read: false },
    ])
    await screen.findByText('One')
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    expect(
      await screen.findByText(zhCN.inbox.markAllReadDone.replace('{{n}}', '2')),
    ).toBeInTheDocument()
    expect(
      rig.calls.some((call) => call.path === READ_ALL_PATH && call.method === 'POST'),
    ).toBe(true)
  })

  it('renders the empty placeholder when there are no messages', async () => {
    mount([])
    const emptyTitle = await screen.findByText(zhCN.inbox.empty.title)
    expect(emptyTitle).toBeInTheDocument()
    // The empty placeholder is a status, not a section: it must not enter the
    // page heading outline (only the page's own "Notifications" h1 does), so
    // its title carries no heading role, and the placeholder is a polite
    // status region instead.
    expect(
      screen.queryByRole('heading', { name: zhCN.inbox.empty.title }),
    ).not.toBeInTheDocument()
    expect(emptyTitle.closest('[role="status"]')).not.toBeNull()
    // With nothing unread, the header action is disabled.
    expect(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    ).toBeDisabled()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const rig = makeRealClientRigWithoutSession(() => {
      throw new Error('down')
    })
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox />)
    expect(await screen.findByText(zhCN.inbox.error.title)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.inbox.retry })).toBeInTheDocument()
  })
})
