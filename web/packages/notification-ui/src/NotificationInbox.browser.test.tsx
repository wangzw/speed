/**
 * NotificationInbox in a real browser: axe's label-content-name-mismatch
 * (WCAG 2.5.3 Label in Name) over an unread row's "mark as read" control,
 * and the focus a write must keep on the control that started it.
 *
 * The rule reads a control's visible text through the canvas text-measurement
 * API; jsdom answers `getContext('2d')` with null, so the check throws there
 * and comes back indeterminate for every node. Only a real browser can pass or
 * fail it. The control shows "Mark as read" and carries a longer accessible
 * name that also identifies the message; the longer-name wording must keep the
 * visible text contiguous so it stays inside the name.
 *
 * The focus cases live here because jsdom does not blur a focused control
 * when it turns natively disabled; Chromium does, which is the failure the
 * controls must not reproduce. The title of an unread message starts a read
 * write and reports busy while it runs; the pager control that reaches the
 * last page becomes unavailable. Both keep the focus they had.
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import axe from 'axe-core'
import { page, userEvent } from 'vitest/browser'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
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

/** A response the case settles by hand, so one write can be held in flight. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** What currently holds focus, for a failure message. */
function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  return `${active.tagName.toLowerCase()} "${text}"`
}

interface Row {
  readonly id: string
  readonly title: string
  readonly link: string
  read: boolean
}

/** A stateful inbox the responder reads and mutates, honouring the listing's
 * limit and offset. */
function inboxResponder(rows: Row[]): RealResponder {
  const unread = () => rows.filter((row) => !row.read).length
  return (call: RealCall) => {
    if (call.path === UNREAD_PATH) {
      return jsonResponse(200, { unread: unread() })
    }
    if (call.path === MESSAGES_PATH) {
      const params = new URLSearchParams(call.query)
      const rawLimit = params.get('limit')
      const limit = rawLimit === null ? rows.length : Number(rawLimit)
      const offset = Number(params.get('offset') ?? '0')
      return jsonResponse(200, {
        messages: rows.slice(offset, offset + limit).map((row) => ({
          id: row.id,
          type_key: 'demo.thing.happened',
          group: 'demo',
          title: row.title,
          body: '',
          params: null,
          link: row.link,
          read_at: row.read ? '2026-08-01T02:00:00.000Z' : null,
          created_at: '2026-08-01T01:00:00.000Z',
        })),
        total: rows.length,
      })
    }
    const readMatch = /^\/api\/v1\/notifications\/messages\/(.+)\/read$/.exec(
      call.path,
    )
    if (readMatch !== null && call.method === 'POST') {
      const row = rows.find((candidate) => candidate.id === readMatch[1])
      if (row !== undefined) {
        row.read = true
      }
      return new Response(null, { status: 204 })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

describe('NotificationInbox write focus in Chromium', () => {
  it('keeps focus on a message title while its open write runs and after it settles', async () => {
    await page.viewport(1000, 900)
    const gate = deferred<Response>()
    const rows: Row[] = [
      { id: 'm1', title: TITLE, link: '/objects/1', read: false },
    ]
    const responder = inboxResponder(rows)
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.method === 'POST' && call.path === `${MESSAGES_PATH}/m1/read`) {
        return gate.promise.then(() => responder(call))
      }
      return responder(call)
    })
    rig.store.set('access-1')
    const onOpen = vi.fn()
    renderWithProviders(<NotificationInbox onOpen={onOpen} />, {
      language: 'en-US',
    })

    const title = await screen.findByRole('button', { name: TITLE })
    await userEvent.click(title)

    // The title starts the message's read write and reports busy; it stays
    // focusable and keeps the focus it had. Focus is asserted first so a
    // control that dropped it is reported as the defect rather than as a
    // missing attribute.
    expect(
      title,
      `focus while the open write runs; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    await waitFor(() => expect(title).toHaveAttribute('aria-busy', 'true'))
    expect(title).toHaveAttribute('aria-disabled', 'true')

    gate.resolve(new Response(null, { status: 204 }))
    await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1))
    expect(
      title,
      `focus after the open write settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    await waitFor(() => expect(title).not.toHaveAttribute('aria-busy', 'true'))
    expect(title).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps focus on the pager control that reaches the last page', async () => {
    await page.viewport(1000, 900)
    const rows: Row[] = ['One', 'Two', 'Three', 'Four', 'Five'].map(
      (title, index) => ({
        id: `m${index + 1}`,
        title,
        link: '',
        read: false,
      }),
    )
    const rig = makeRealClientRigWithoutSession(inboxResponder(rows))
    rig.store.set('access-1')
    renderWithProviders(<NotificationInbox pageSize={2} />, {
      language: 'en-US',
    })

    const next = await screen.findByRole('button', { name: enUS.inbox.next })
    await userEvent.click(next)
    await screen.findByText('Three')
    await userEvent.click(next)
    await screen.findByText('Five')

    // The control that moved the operator to the last page is unavailable
    // there, and it is the control that keeps the focus.
    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(
      next,
      `focus on the last page; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })
})
