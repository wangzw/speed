/**
 * NotificationPreferences behaviour over the generated preference
 * operations: the matrix renders one row per type with a checkbox per
 * served channel reflecting the effective set; without a presence
 * provider only the in-app column exists, and the host's optional
 * channels add their columns in the closed vocabulary's order; toggling a
 * channel PUTs that type/channel with the target state and re-reads; a
 * type the taxonomy marks not unsubscribable renders a disabled row with
 * the note; the type label and grouped rendering follow the optional
 * props; an empty taxonomy renders the empty placeholder; a landed write
 * confirms the type, channel and new state under the table while a failure
 * resolves in that same region; the whole matrix locks while a write is in
 * flight; and the surface passes axe.
 */

import { describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { checkboxClasses } from '@mui/material/Checkbox'
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
import { NotificationPreferences } from './NotificationPreferences.js'
import { NotificationPresenceProvider } from './NotificationPresence.js'

const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

interface TypeRow {
  readonly key: string
  readonly unsubscribable: boolean
  readonly group?: string
  channels: string[]
}

/** A stateful preference matrix the responder reads and mutates. */
function preferencesResponder(types: TypeRow[]): RealResponder {
  return (call: RealCall) => {
    if (call.path === TYPES_PATH && call.method === 'GET') {
      return jsonResponse(200, {
        types: types.map((type) => ({
          key: type.key,
          group: type.group ?? 'demo',
          default_channels: type.channels,
          unsubscribable: type.unsubscribable,
          recipient_visible_params: [],
        })),
      })
    }
    const putMatch =
      /^\/api\/v1\/notifications\/preferences\/([^/]+)\/(in_app|email|sms)$/.exec(
        call.path,
      )
    if (putMatch !== null && call.method === 'PUT') {
      const [, typeKey, channel] = putMatch
      const type = types.find((candidate) => candidate.key === typeKey)
      const enabled = (call.body as { enabled?: boolean } | null)?.enabled === true
      if (type !== undefined) {
        const set = new Set(type.channels)
        if (enabled) {
          set.add(channel ?? '')
        } else {
          set.delete(channel ?? '')
        }
        type.channels = [...set]
      }
      return jsonResponse(200, { type_key: typeKey, channels: type?.channels ?? [] })
    }
    if (call.path === PREFERENCES_PATH && call.method === 'GET') {
      return jsonResponse(200, {
        preferences: types.map((type) => ({
          type_key: type.key,
          group: type.group ?? 'demo',
          channels: type.channels,
          explicit: true,
        })),
      })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

/** The surface with the given channels served by the host; no channels
 * means no presence provider at all. */
function withPresence(
  channels: readonly ('email' | 'sms')[],
  surface: ReactElement,
): ReactElement {
  return channels.length === 0 ? (
    surface
  ) : (
    <NotificationPresenceProvider preferences channels={channels}>
      {surface}
    </NotificationPresenceProvider>
  )
}

function mount(
  types: TypeRow[],
  channels: readonly ('email' | 'sms')[] = [],
) {
  const rig = makeRealClientRigWithoutSession(preferencesResponder(types))
  rig.store.set('access-1')
  return {
    rig,
    ...renderWithProviders(
      withPresence(channels, <NotificationPreferences />),
    ),
  }
}

/** The header labels of the matrix's columns, in rendered order. */
function columnLabels(): string[] {
  return screen
    .getAllByRole('columnheader')
    .map((header) => header.textContent ?? '')
}

/** A response the test settles by hand, so a write can be held in flight. */
function deferredResponse(): {
  readonly promise: Promise<Response>
  readonly resolve: (response: Response) => void
} {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** The checkbox of one channel in one type's row. */
function channelCheckbox(
  typeKey: string,
  channel: 'in_app' | 'email' | 'sms',
): HTMLElement {
  return within(rowFor(typeKey)).getByRole('checkbox', {
    name: zhCN.preferences.channelToggleAria
      .replace('{{type}}', typeKey)
      .replace('{{channel}}', zhCN.preferences.channel[channel]),
  })
}

/** The row a type key's cells live in. */
function rowFor(typeKey: string): HTMLElement {
  const cell = screen.getByText(typeKey)
  const row = cell.closest('tr')
  if (row === null) {
    throw new Error(`no row for ${typeKey}`)
  }
  return row
}

describe('NotificationPreferences', () => {
  it('reflects the effective set and PUTs a channel toggle, passing axe', async () => {
    const { rig } = mount(
      [
        { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      ],
      ['email', 'sms'],
    )
    await screen.findByText('demo.thing.happened')
    const inApp = within(rowFor('demo.thing.happened')).getByRole('checkbox', {
      name: zhCN.preferences.channelToggleAria
        .replace('{{type}}', 'demo.thing.happened')
        .replace('{{channel}}', zhCN.preferences.channel.in_app),
    })
    const email = within(rowFor('demo.thing.happened')).getByRole('checkbox', {
      name: zhCN.preferences.channelToggleAria
        .replace('{{type}}', 'demo.thing.happened')
        .replace('{{channel}}', zhCN.preferences.channel.email),
    })
    expect(inApp).toBeChecked()
    expect(email).not.toBeChecked()

    await userEvent.click(email)
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.method === 'PUT' &&
            call.path ===
              '/api/v1/notifications/preferences/demo.thing.happened/email' &&
            (call.body as { enabled?: boolean } | null)?.enabled === true,
        ),
      ).toBe(true),
    )
    // The matrix re-read: the email cell is now checked.
    await waitFor(() =>
      expect(
        within(rowFor('demo.thing.happened')).getByRole('checkbox', {
          name: zhCN.preferences.channelToggleAria
            .replace('{{type}}', 'demo.thing.happened')
            .replace('{{channel}}', zhCN.preferences.channel.email),
        }),
      ).toBeChecked(),
    )
    await expectNoAxeViolations()
  })

  it('confirms a landed toggle with the type, channel and new state', async () => {
    mount(
      [
        { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      ],
      ['email'],
    )
    await screen.findByText('demo.thing.happened')

    await userEvent.click(channelCheckbox('demo.thing.happened', 'email'))
    // The confirmation names what changed: the preference type, the
    // channel, and the state it now has.
    expect(
      await screen.findByText(
        zhCN.preferences.saved
          .replace('{{type}}', 'demo.thing.happened')
          .replace('{{channel}}', zhCN.preferences.channel.email)
          .replace('{{state}}', zhCN.preferences.state.enabled),
      ),
    ).toBeInTheDocument()
    // The matrix re-read settled the cell before the second toggle.
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeChecked(),
    )

    await userEvent.click(channelCheckbox('demo.thing.happened', 'email'))
    expect(
      await screen.findByText(
        zhCN.preferences.saved
          .replace('{{type}}', 'demo.thing.happened')
          .replace('{{channel}}', zhCN.preferences.channel.email)
          .replace('{{state}}', zhCN.preferences.state.disabled),
      ),
    ).toBeInTheDocument()
  })

  it('locks the whole matrix while a write is in flight', async () => {
    const gate = deferredResponse()
    const types: TypeRow[] = [
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      { key: 'billing.invoice.due', unsubscribable: true, channels: ['in_app'] },
    ]
    const base = preferencesResponder(types)
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.method === 'PUT' && call.path.includes('demo.thing.happened')) {
        return gate.promise.then(() => base(call))
      }
      return base(call)
    })
    rig.store.set('access-1')
    renderWithProviders(
      withPresence(['email'], <NotificationPreferences />),
    )
    await screen.findByText('billing.invoice.due')

    await userEvent.click(channelCheckbox('demo.thing.happened', 'email'))
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeDisabled(),
    )
    // The lock belongs to the scope, not to the toggling row: the whole
    // matrix disables so no second attempt can overlap the first.
    expect(channelCheckbox('billing.invoice.due', 'email')).toBeDisabled()

    gate.resolve(jsonResponse(200, {}))
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeEnabled(),
    )
    expect(channelCheckbox('billing.invoice.due', 'email')).toBeEnabled()
  })

  it('ignores a toggle on another row while a write is in flight', async () => {
    const gate = deferredResponse()
    const types: TypeRow[] = [
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      { key: 'billing.invoice.due', unsubscribable: true, channels: ['in_app'] },
    ]
    const base = preferencesResponder(types)
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.method === 'PUT' && call.path.includes('demo.thing.happened')) {
        return gate.promise.then(() => base(call))
      }
      return base(call)
    })
    rig.store.set('access-1')
    renderWithProviders(
      withPresence(['email'], <NotificationPreferences />),
    )
    await screen.findByText('billing.invoice.due')

    await userEvent.click(channelCheckbox('demo.thing.happened', 'email'))
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeDisabled(),
    )

    // The untouched row is not merely painted as disabled: operating it
    // while the first write is in flight starts no second write, so the
    // first attempt's result is the only one the scope can report.
    await userEvent.click(channelCheckbox('billing.invoice.due', 'email'))
    expect(
      rig.calls.some(
        (call) =>
          call.method === 'PUT' &&
          call.path ===
            '/api/v1/notifications/preferences/billing.invoice.due/email',
      ),
    ).toBe(false)
    expect(channelCheckbox('billing.invoice.due', 'email')).not.toBeChecked()

    gate.resolve(jsonResponse(200, {}))
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeEnabled(),
    )
    // The blocked click left nothing behind: the second row still shows the
    // server's answer and the scope ran exactly one attempt.
    expect(channelCheckbox('billing.invoice.due', 'email')).not.toBeChecked()
    expect(rig.calls.filter((call) => call.method === 'PUT')).toHaveLength(1)
  })

  it('offers the in-app column alone when no presence provider is mounted', async () => {
    mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
    await screen.findByText('demo.thing.happened')
    // Fail closed: the optional channels exist only when the host says the
    // deployment serves them.
    expect(columnLabels()).toEqual([
      zhCN.preferences.column.type,
      zhCN.preferences.channel.in_app,
    ])
  })

  it("renders exactly the served channels in the closed vocabulary's order", async () => {
    mount(
      [
        { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      ],
      // Deliberately reversed: the host's list order never decides the
      // matrix's column order.
      ['sms', 'email'],
    )
    await screen.findByText('demo.thing.happened')
    expect(columnLabels()).toEqual([
      zhCN.preferences.column.type,
      zhCN.preferences.channel.in_app,
      zhCN.preferences.channel.email,
      zhCN.preferences.channel.sms,
    ])
  })

  it('renders the type key by default and a host label when given one', async () => {
    mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
    // The default label is the key, on the row and in the cell's name.
    expect(await screen.findByText('demo.thing.happened')).toBeInTheDocument()
    expect(
      within(rowFor('demo.thing.happened')).getByRole('checkbox', {
        name: zhCN.preferences.channelToggleAria
          .replace('{{type}}', 'demo.thing.happened')
          .replace('{{channel}}', zhCN.preferences.channel.in_app),
      }),
    ).toBeInTheDocument()
  })

  it('labels a type through typeLabel and names the cell with it', async () => {
    const rig = makeRealClientRigWithoutSession(
      preferencesResponder([
        { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
      ]),
    )
    rig.store.set('access-1')
    renderWithProviders(
      <NotificationPreferences
        typeLabel={({ key, group }) => `${group}:${key}`}
      />,
    )
    const label = 'demo:demo.thing.happened'
    expect(await screen.findByText(label)).toBeInTheDocument()
    expect(screen.queryByText('demo.thing.happened')).not.toBeInTheDocument()
    expect(
      within(rowFor(label)).getByRole('checkbox', {
        name: zhCN.preferences.channelToggleAria
          .replace('{{type}}', label)
          .replace('{{channel}}', zhCN.preferences.channel.in_app),
      }),
    ).toBeInTheDocument()
  })

  it('renders rows grouped only when groupLabel is supplied', async () => {
    const rig = makeRealClientRigWithoutSession(
      preferencesResponder([
        {
          key: 'billing.invoice.due',
          unsubscribable: true,
          group: 'billing',
          channels: ['in_app'],
        },
        {
          key: 'authn.security.alert',
          unsubscribable: true,
          group: 'security',
          channels: ['in_app'],
        },
      ]),
    )
    rig.store.set('access-1')
    renderWithProviders(
      <NotificationPreferences groupLabel={(group) => `Group ${group}`} />,
    )
    expect(await screen.findByText('Group billing')).toBeInTheDocument()
    expect(screen.getByText('Group security')).toBeInTheDocument()
    // The group headings are full-width divider cells, not extra columns.
    expect(screen.getByText('Group billing').closest('td')).not.toBeNull()
    await expectNoAxeViolations()
  })

  it('does not render group headings without groupLabel', async () => {
    mount([
      {
        key: 'billing.invoice.due',
        unsubscribable: true,
        group: 'billing',
        channels: ['in_app'],
      },
    ])
    await screen.findByText('billing.invoice.due')
    expect(screen.queryByText('Group billing')).not.toBeInTheDocument()
  })

  it('disables a non-unsubscribable type and shows the note', async () => {
    mount([
      { key: 'authn.security.alert', unsubscribable: false, channels: ['in_app'] },
    ])
    await screen.findByText('authn.security.alert')
    expect(within(rowFor('authn.security.alert')).getByText(
      zhCN.preferences.unsubscribableNote,
    )).toBeInTheDocument()
    for (const checkbox of within(rowFor('authn.security.alert')).getAllByRole(
      'checkbox',
    )) {
      expect(checkbox).toBeDisabled()
    }
  })

  it('raises the bare channel checkboxes to the 44px floor under a coarse pointer', async () => {
    mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
    await screen.findByText('demo.thing.happened')
    // These checkboxes sit bare in a table cell (no FormControlLabel to
    // widen the hit area), so each one gates up to the 44x44 floor. The
    // sx lands on the checkbox root, not the inner input.
    const checkbox = within(rowFor('demo.thing.happened')).getByRole('checkbox', {
      name: zhCN.preferences.channelToggleAria
        .replace('{{type}}', 'demo.thing.happened')
        .replace('{{channel}}', zhCN.preferences.channel.in_app),
    })
    const root = checkbox.closest(`.${checkboxClasses.root}`)
    expect(root).not.toBeNull()
    expect(hasCoarsePointerTouchTarget(root as Element)).toBe(true)
  })

  it('renders the empty placeholder when the taxonomy declares no types', async () => {
    mount([])
    expect(await screen.findByText(zhCN.preferences.empty.title)).toBeInTheDocument()
  })

  it('renders the tenant state on the admission layer bare 404, without a retry', async () => {
    const rig = makeRealClientRigWithoutSession(
      () => new Response(null, { status: 404 }),
    )
    rig.store.set('access-1')
    renderWithProviders(<NotificationPreferences />)
    expect(
      await screen.findByText(zhCN.tenantUnavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.tenantUnavailable.description),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.preferences.error.title),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.preferences.retry }),
    ).not.toBeInTheDocument()
  })

  it('renders the retryable error placeholder, not the empty one, when the first read fails', async () => {
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === PREFERENCES_PATH && call.method === 'GET') {
        return jsonResponse(503, {})
      }
      return jsonResponse(200, { types: [] })
    })
    rig.store.set('access-1')
    renderWithProviders(<NotificationPreferences />)
    // A failed first read delivered no matrix: the empty placeholder would
    // claim there is nothing to configure, so the retryable error stands.
    expect(
      await screen.findByText(zhCN.preferences.error.title),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.preferences.retry }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.preferences.empty.title),
    ).not.toBeInTheDocument()
  })

  it('keeps the matrix and marks it stale when a refresh fails, offering a retry', async () => {
    const types: TypeRow[] = [
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ]
    const responder = preferencesResponder(types)
    let preferenceReads = 0
    // The test decides when a preference read stays in flight: it is
    // switched on only for the retry, so the stale refresh keeps settling.
    let holdNextRead = false
    const heldReads: Array<(response: Response) => void> = []
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === PREFERENCES_PATH && call.method === 'GET') {
        preferenceReads += 1
        if (holdNextRead) {
          return new Promise<Response>((resolve) => {
            heldReads.push(resolve)
          })
        }
        // The first read settles the matrix; every later refresh fails.
        if (preferenceReads > 1) {
          return jsonResponse(503, {})
        }
      }
      return responder(call)
    })
    rig.store.set('access-1')
    const { queryClient } = renderWithProviders(<NotificationPreferences />)
    expect(await screen.findByText('demo.thing.happened')).toBeInTheDocument()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The refresh failed, but the old matrix stays on screen under the
    // stale notice rather than being replaced by the error placeholder.
    expect(screen.getByText('demo.thing.happened')).toBeInTheDocument()
    expect(screen.getByRole('table')).toBeInTheDocument()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(
      screen.queryByText(zhCN.preferences.error.title),
    ).not.toBeInTheDocument()

    const retry = screen.getByRole('button', { name: zhCN.preferences.retry })
    holdNextRead = true
    await userEvent.click(retry)
    await waitFor(() => expect(heldReads.length).toBeGreaterThan(0))

    // While the retry is in flight the notice is withdrawn through the
    // read's own fetching state, not left standing.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('table')).toBeInTheDocument()

    holdNextRead = false
    await act(async () => {
      for (const settle of heldReads.splice(0)) {
        settle(jsonResponse(503, {}))
      }
    })
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.stale.description,
    )
    expect(screen.getByText('demo.thing.happened')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('renders the tenant sentence in the write banner when the refusal is a bare 404', async () => {
    const rig = makeRealClientRigWithoutSession((call) => {
      if (call.path === TYPES_PATH && call.method === 'GET') {
        return jsonResponse(200, {
          types: [
            {
              key: 'demo.thing.happened',
              group: 'demo',
              default_channels: ['in_app'],
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
              type_key: 'demo.thing.happened',
              group: 'demo',
              channels: ['in_app'],
              explicit: true,
            },
          ],
        })
      }
      if (call.method === 'PUT') {
        return new Response(null, { status: 404 })
      }
      return jsonResponse(200, {})
    })
    rig.store.set('access-1')
    renderWithProviders(
      withPresence(['email'], <NotificationPreferences />),
    )
    await screen.findByText('demo.thing.happened')

    await userEvent.click(
      within(rowFor('demo.thing.happened')).getByRole('checkbox', {
        name: zhCN.preferences.channelToggleAria
          .replace('{{type}}', 'demo.thing.happened')
          .replace('{{channel}}', zhCN.preferences.channel.email),
      }),
    )
    const message = await screen.findByText(zhCN.errors.tenantUnavailable)
    expect(message).toBeInTheDocument()
    // The write result is reported in the region under the matrix, not
    // above it: the failure belongs to the row the operator just changed.
    const alert = message.closest('[role="alert"]')
    expect(alert).not.toBeNull()
    expect(
      screen.getByRole('table').compareDocumentPosition(alert as Element) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })
})
