/**
 * NotificationPreferences behaviour over the generated preference
 * operations: the matrix renders one row per type with a checkbox per
 * served channel reflecting the effective set; without a presence
 * provider only the in-app column exists, and the host's optional
 * channels add their columns in the closed vocabulary's order; toggling a
 * channel PUTs that type/channel with the target state and re-reads; a
 * type the taxonomy marks not unsubscribable renders a disabled row with
 * the note; the type label and grouped rendering follow the optional
 * props; an empty taxonomy renders the empty placeholder; and the surface
 * passes axe.
 */

import { describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    const root = checkbox.closest('.MuiCheckbox-root')
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
    expect(await screen.findByText(zhCN.errors.tenantUnavailable)).toBeInTheDocument()
  })
})
