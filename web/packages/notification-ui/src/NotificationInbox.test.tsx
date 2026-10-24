/**
 * NotificationInbox behaviour over the generated inbox operations: the
 * caller's messages render (title, body, unread chip), an unread row's
 * "mark as read" POSTs that message's read route and the list re-reads
 * with it marked, the header action POSTs read-all and reports the
 * answer's count, a message carrying a link activates the host's
 * `onOpen` after marking it read, a page size pages through the
 * response's total, the embedded variant renders its own heading, a
 * per-row detail renderer appends content, an empty answer renders the
 * empty placeholder, a failed read renders the retryable error
 * placeholder, and the whole surface passes axe.
 */

import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import type { ReactElement } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formatInstant } from '@speed/i18n'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
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
  readonly link?: string
}

function message(row: Row): Record<string, unknown> {
  return {
    id: row.id,
    type_key: 'demo.thing.happened',
    group: 'demo',
    title: row.title,
    body: row.body,
    params: null,
    link: row.link ?? '',
    // The server's own shape: an unread message carries read_at as JSON
    // null (its view's *time.Time has no omitempty), a read one as an
    // instant string.
    read_at: row.read ? '2026-08-01T02:00:00.000Z' : null,
    created_at: '2026-08-01T01:00:00.000Z',
  }
}

/** A stateful inbox the responder reads and mutates, honouring the
 * listing's limit and offset. */
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
      const params = new URLSearchParams(call.query)
      const rawLimit = params.get('limit')
      const limit = rawLimit === null ? rows.length : Number(rawLimit)
      const offset = Number(params.get('offset') ?? '0')
      return jsonResponse(200, {
        messages: rows.slice(offset, offset + limit).map(message),
        total: rows.length,
      })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

function mount(rows: Row[], ui: ReactElement = <NotificationInbox />) {
  const rig = makeRealClientRigWithoutSession(inboxResponder(rows))
  rig.store.set('access-1')
  return { rig, ...renderWithProviders(ui) }
}

/** A host that changes the list context the inbox renders with: both a
 * group filter and a page size change are new lists. */
function ContextHarness({ pageSize }: { readonly pageSize: number }) {
  const [group, setGroup] = useState('')
  const [size, setSize] = useState(pageSize)
  return (
    <>
      <button type="button" onClick={() => setGroup('billing')}>
        change group
      </button>
      <button type="button" onClick={() => setSize(pageSize + 2)}>
        change page size
      </button>
      <NotificationInbox group={group} pageSize={size} />
    </>
  )
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
    // The confirmation is one standing status region: it is already in the
    // tree, empty, before the write -- so the count lands as a change a
    // screen reader announces, not a region born together with its text.
    const notice = screen.getByRole('status')
    expect(notice.textContent).toBe('')
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    const done = zhCN.inbox.markAllReadDone.replace('{{n}}', '2')
    expect(await screen.findByText(done)).toBeInTheDocument()
    // Still the same region (never a second born with its text), now filled.
    expect(screen.getByRole('status')).toBe(notice)
    expect(notice).toHaveTextContent(done)
    expect(
      rig.calls.some((call) => call.path === READ_ALL_PATH && call.method === 'POST'),
    ).toBe(true)
  })

  it('marks a linked message read before handing it to onOpen', async () => {
    const order: string[] = []
    const rows: Row[] = [
      { id: 'm1', title: 'Open me', body: '', read: false, link: '/objects/1' },
    ]
    const responder = inboxResponder(rows)
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.method === 'POST' && call.path === `${MESSAGES_PATH}/m1/read`) {
        order.push('read')
      }
      return responder(call)
    })
    rig.store.set('access-1')
    const onOpen = vi.fn((message: { link: string }) =>
      order.push(`open:${message.link}`),
    )
    renderWithProviders(<NotificationInbox onOpen={onOpen} />)

    await userEvent.click(
      await screen.findByRole('button', { name: 'Open me' }),
    )
    await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1))
    // The read lands first: a row is handed over already marked.
    expect(order).toEqual(['read', 'open:/objects/1'])
  })

  it('opens an already-read linked message without writing a read', async () => {
    const onOpen = vi.fn()
    const { rig } = mount(
      [{ id: 'm2', title: 'Already read', body: '', read: true, link: '/objects/2' }],
      <NotificationInbox onOpen={onOpen} />,
    )
    await userEvent.click(
      await screen.findByRole('button', { name: 'Already read' }),
    )
    await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1))
    expect(
      rig.calls.some(
        (call) => call.method === 'POST' && call.path === `${MESSAGES_PATH}/m2/read`,
      ),
    ).toBe(false)
  })

  it('pages through the response total with previous and next', async () => {
    const { rig } = mount(
      [
        { id: 'm1', title: 'One', body: '', read: false },
        { id: 'm2', title: 'Two', body: '', read: false },
        { id: 'm3', title: 'Three', body: '', read: false },
        { id: 'm4', title: 'Four', body: '', read: false },
        { id: 'm5', title: 'Five', body: '', read: false },
      ],
      <NotificationInbox pageSize={2} />,
    )
    await screen.findByText('One')
    expect(screen.queryByText('Three')).toBeNull()
    const previous = screen.getByRole('button', { name: zhCN.inbox.previous })
    expect(previous).toBeDisabled()

    await userEvent.click(screen.getByRole('button', { name: zhCN.inbox.next }))
    expect(await screen.findByText('Three')).toBeInTheDocument()
    expect(screen.queryByText('One')).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.inbox.previous }),
    ).toBeEnabled()
    // The next page is the second window of the list.
    expect(
      rig.calls.some(
        (call) =>
          call.path === MESSAGES_PATH &&
          call.method === 'GET' &&
          new URLSearchParams(call.query).get('limit') === '2' &&
          new URLSearchParams(call.query).get('offset') === '2',
      ),
    ).toBe(true)
    expect(
      screen.getByRole('button', { name: zhCN.inbox.next }),
    ).toBeEnabled()

    // The last page: five rows at a page size of two leave one row in the
    // third window, and no fourth window exists to read. The pager must
    // stop there rather than point past the total.
    await userEvent.click(screen.getByRole('button', { name: zhCN.inbox.next }))
    expect(await screen.findByText('Five')).toBeInTheDocument()
    expect(screen.queryByText('Three')).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.inbox.next }),
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: zhCN.inbox.previous }),
    ).toBeEnabled()
    expect(
      rig.calls.some(
        (call) =>
          call.path === MESSAGES_PATH &&
          call.method === 'GET' &&
          new URLSearchParams(call.query).get('offset') === '6',
      ),
    ).toBe(false)

    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.previous }),
    )
    expect(await screen.findByText('Three')).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.previous }),
    )
    expect(await screen.findByText('One')).toBeInTheDocument()
  })

  it('gates mark-all on the whole inbox, not the visible page', async () => {
    const rows: Row[] = [
      { id: 'm1', title: 'One', body: '', read: false },
      { id: 'm2', title: 'Two', body: '', read: false },
      { id: 'm3', title: 'Three', body: '', read: true },
      { id: 'm4', title: 'Four', body: '', read: true },
    ]
    const { rig } = mount(rows, <NotificationInbox pageSize={2} />)
    await screen.findByText('One')

    await userEvent.click(screen.getByRole('button', { name: zhCN.inbox.next }))
    // Page two holds only read rows, but the inbox still has two unread
    // messages on page one: the whole-inbox action stays available.
    await screen.findByText('Three')
    const markAll = screen.getByRole('button', { name: zhCN.inbox.markAllRead })
    expect(markAll).toBeEnabled()

    await userEvent.click(markAll)
    expect(
      await screen.findByText(zhCN.inbox.markAllReadDone.replace('{{n}}', '2')),
    ).toBeInTheDocument()
    expect(
      rig.calls.some((call) => call.path === READ_ALL_PATH && call.method === 'POST'),
    ).toBe(true)
    expect(rows.filter((row) => !row.read)).toHaveLength(0)
  })

  it('clears the list feedback when the page changes', async () => {
    const rows: Row[] = [
      { id: 'm1', title: 'One', body: '', read: false },
      { id: 'm2', title: 'Two', body: '', read: false },
      { id: 'm3', title: 'Three', body: '', read: false },
    ]
    const responder = inboxResponder(rows)
    let failNext = true
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === READ_ALL_PATH && call.method === 'POST' && failNext) {
        failNext = false
        return jsonResponse(500, { code: 'notification.bad_request' })
      }
      return responder(call)
    })
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox pageSize={2} />)
    await screen.findByText('One')

    // A failed write belongs to the same feedback scope: the page it was
    // raised on owns it, so the move clears the alert.
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: zhCN.inbox.next }))
    expect(await screen.findByText('Three')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())

    // Read-all covers the whole inbox, not just the visible page.
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    const done = zhCN.inbox.markAllReadDone.replace('{{n}}', '3')
    expect(await screen.findByText(done)).toBeInTheDocument()

    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.previous }),
    )
    expect(await screen.findByText('One')).toBeInTheDocument()
    // The confirmation described the previous page's context, so it goes
    // with it.
    expect(screen.queryByText(done)).toBeNull()
  })

  it('reads a new group from its first page, not from the previous page index', async () => {
    const { rig } = mount(
      [
        { id: 'm1', title: 'One', body: '', read: false },
        { id: 'm2', title: 'Two', body: '', read: false },
        { id: 'm3', title: 'Three', body: '', read: false },
        { id: 'm4', title: 'Four', body: '', read: false },
      ],
      <ContextHarness pageSize={2} />,
    )
    await screen.findByText('One')
    await userEvent.click(screen.getByRole('button', { name: zhCN.inbox.next }))
    await screen.findByText('Three')

    // Inspect only the list reads issued after the context change: the
    // previous page's offset must never be one of them.
    rig.calls.length = 0
    await userEvent.click(screen.getByRole('button', { name: 'change group' }))
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.path === MESSAGES_PATH &&
            new URLSearchParams(call.query).get('group') === 'billing' &&
            new URLSearchParams(call.query).get('offset') === '0',
        ),
      ).toBe(true),
    )
    expect(
      rig.calls.some(
        (call) =>
          call.path === MESSAGES_PATH &&
          new URLSearchParams(call.query).get('group') === 'billing' &&
          new URLSearchParams(call.query).get('offset') === '2',
      ),
    ).toBe(false)
  })

  it('clears a failed write when the page size changes', async () => {
    const responder = inboxResponder([
      { id: 'm1', title: 'One', body: '', read: false },
      { id: 'm2', title: 'Two', body: '', read: false },
    ])
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === READ_ALL_PATH && call.method === 'POST') {
        return jsonResponse(500, { code: 'notification.bad_request' })
      }
      return responder(call)
    })
    rig.store.set('access-1')
    renderWithProviders(<ContextHarness pageSize={2} />)
    await screen.findByText('One')

    await userEvent.click(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    )
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await userEvent.click(
      screen.getByRole('button', { name: 'change page size' }),
    )
    // The failure described the previous list; a new page size is a new
    // context, so the banner goes with it.
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })

  it('renders its own heading in the embedded variant instead of the page header', async () => {
    mount(
      [{ id: 'm1', title: 'One', body: '', read: false }],
      <NotificationInbox variant="embedded" headingLevel="h3" />,
    )
    expect(
      await screen.findByRole('heading', { level: 3, name: zhCN.inbox.title }),
    ).toBeInTheDocument()
    // The page-level h1 of the page variant is gone; the header's action
    // still rides beside the heading.
    expect(
      screen.queryByRole('heading', { level: 1, name: zhCN.inbox.title }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
    ).toBeInTheDocument()
  })

  it('appends the per-row detail the host renders', async () => {
    mount(
      [{ id: 'm1', title: 'One', body: '', read: false }],
      <NotificationInbox
        renderDetail={(message) => <span>detail:{message.title}</span>}
      />,
    )
    expect(await screen.findByText('detail:One')).toBeInTheDocument()
  })

  it('restricts the listing to the given group', async () => {
    const { rig } = mount(
      [{ id: 'm1', title: 'One', body: '', read: false }],
      <NotificationInbox group="billing" />,
    )
    await screen.findByText('One')
    expect(
      rig.calls.some(
        (call) =>
          call.path === MESSAGES_PATH &&
          call.method === 'GET' &&
          new URLSearchParams(call.query).get('group') === 'billing',
      ),
    ).toBe(true)
  })

  it('formats a time in the display zone the host supplies', async () => {
    const instant = '2026-08-01T01:00:00.000Z'
    mount(
      [{ id: 'm1', title: 'One', body: '', read: false }],
      <DisplayTimeZoneProvider timeZone="Asia/Tokyo">
        <NotificationInbox />
      </DisplayTimeZoneProvider>,
    )
    expect(
      await screen.findByText(
        formatInstant(instant, 'zh-CN', { timeZone: 'Asia/Tokyo' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(formatInstant(instant, 'zh-CN', { timeZone: 'UTC' })),
    ).toBeNull()
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

  it('raises the inbox actions to the 44px floor under a coarse pointer', async () => {
    mount([
      { id: 'm1', title: 'Build finished', body: '', read: false },
    ])
    await screen.findByText('Build finished')
    // The header action and the per-message action are compact controls;
    // on a finger-driven device each one gates up to the 44x44 floor.
    const gated = [
      screen.getByRole('button', { name: zhCN.inbox.markAllRead }),
      screen.getByRole('button', {
        name: zhCN.inbox.markReadAria.replace('{{title}}', 'Build finished'),
      }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
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

  it('renders the tenant state on the admission layer bare 404, without a retry', async () => {
    const rig = makeRealClientRigWithoutSession(
      () => new Response(null, { status: 404 }),
    )
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox />)
    expect(
      await screen.findByText(zhCN.tenantUnavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.tenantUnavailable.description),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.inbox.error.title)).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.inbox.retry }),
    ).not.toBeInTheDocument()
  })

  it('keeps the retryable placeholder for a 503', async () => {
    const rig = makeRealClientRigWithoutSession(() => jsonResponse(503, {}))
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox />)
    expect(await screen.findByText(zhCN.inbox.error.title)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.inbox.retry }),
    ).toBeInTheDocument()
  })

  it('keeps the old list and marks it stale when a refresh fails, offering a retry', async () => {
    const rows: Row[] = [
      { id: 'm1', title: 'Build finished', body: '', read: false },
    ]
    const responder = inboxResponder(rows)
    let listReads = 0
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === MESSAGES_PATH && call.method === 'GET') {
        listReads += 1
        // The first read settles the list; every later refresh fails.
        if (listReads > 1) {
          return jsonResponse(503, {})
        }
      }
      return responder(call)
    })
    rig.store.set('access-1')
    const { queryClient } = renderWithProviders(<NotificationInbox />)
    expect(await screen.findByText('Build finished')).toBeInTheDocument()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The refresh failed, but the old list stays on screen under the stale
    // notice rather than being replaced by the error placeholder.
    expect(screen.getByText('Build finished')).toBeInTheDocument()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(
      alert.compareDocumentPosition(screen.getByText('Build finished')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.inbox.error.title)).not.toBeInTheDocument()

    const retry = screen.getByRole('button', { name: zhCN.inbox.retry })
    const readsBeforeRetry = listReads
    await userEvent.click(retry)
    await waitFor(() => expect(listReads).toBeGreaterThan(readsBeforeRetry))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.stale.description,
    )
    expect(screen.getByText('Build finished')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('renders the tenant sentence in the write banner when the refusal is a bare 404', async () => {
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.method === 'GET' && call.path === MESSAGES_PATH) {
        return jsonResponse(200, {
          messages: [
            message({
              id: 'm1',
              title: 'Build finished',
              body: '',
              read: false,
            }),
          ],
          total: 1,
        })
      }
      if (call.method === 'POST' && call.path === `${MESSAGES_PATH}/m1/read`) {
        return new Response(null, { status: 404 })
      }
      return jsonResponse(200, {})
    })
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox />)
    await screen.findByText('Build finished')

    await userEvent.click(
      screen.getByRole('button', {
        name: zhCN.inbox.markReadAria.replace('{{title}}', 'Build finished'),
      }),
    )
    expect(await screen.findByText(zhCN.errors.tenantUnavailable)).toBeInTheDocument()
  })
})
