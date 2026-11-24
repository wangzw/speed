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

function columnLabels(): string[] {
  return screen
    .getAllByRole('columnheader')
    .map((header) => header.textContent ?? '')
}

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
    expect(
      await screen.findByText(
        zhCN.preferences.saved
          .replace('{{type}}', 'demo.thing.happened')
          .replace('{{channel}}', zhCN.preferences.channel.email)
          .replace('{{state}}', zhCN.preferences.state.enabled),
      ),
    ).toBeInTheDocument()
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

  it('keeps the whole matrix inoperable while a write is in flight', async () => {
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

    const initiating = channelCheckbox('demo.thing.happened', 'email')
    const sibling = channelCheckbox('billing.invoice.due', 'email')
    await userEvent.click(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(initiating).not.toBeDisabled()
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toBeDisabled()

    gate.resolve(jsonResponse(200, {}))
    await waitFor(() =>
      expect(initiating).not.toHaveAttribute('aria-disabled', 'true'),
    )
    expect(sibling).not.toHaveAttribute('aria-disabled', 'true')
    expect(initiating).not.toBeDisabled()
    expect(sibling).not.toBeDisabled()
  })

  it('leaves the sibling cells focusable but unavailable while a write is in flight', async () => {
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

    const initiating = channelCheckbox('demo.thing.happened', 'email')
    const sibling = channelCheckbox('billing.invoice.due', 'email')
    await userEvent.click(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-disabled', 'true'),
    )

    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toBeDisabled()
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAttribute('aria-describedby')

    await userEvent.click(sibling)
    sibling.focus()
    expect(sibling).toHaveFocus()
    await userEvent.keyboard(' ')
    expect(
      rig.calls.some(
        (call) =>
          call.method === 'PUT' &&
          call.path ===
            '/api/v1/notifications/preferences/billing.invoice.due/email',
      ),
    ).toBe(false)
    expect(sibling).not.toBeChecked()

    gate.resolve(jsonResponse(200, {}))
    await waitFor(() =>
      expect(initiating).not.toHaveAttribute('aria-disabled', 'true'),
    )
    expect(sibling).not.toBeChecked()
    expect(rig.calls.filter((call) => call.method === 'PUT')).toHaveLength(1)
  })

  it('reports the cell that starts the write as busy, keeping focus and the server value', async () => {
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

    const initiating = channelCheckbox('demo.thing.happened', 'email')
    const sibling = channelCheckbox('billing.invoice.due', 'email')
    await userEvent.click(initiating)
    expect(document.activeElement).toBe(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-busy', 'true'),
    )
    expect(initiating).toHaveAttribute('aria-disabled', 'true')

    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy', 'true')
    expect(sibling).not.toHaveAccessibleDescription()

    expect(initiating).not.toBeChecked()

    gate.resolve(jsonResponse(200, {}))
    await waitFor(() =>
      expect(channelCheckbox('demo.thing.happened', 'email')).toBeChecked(),
    )
    expect(
      channelCheckbox('demo.thing.happened', 'email'),
    ).not.toHaveAttribute('aria-busy', 'true')
    expect(document.activeElement).toBe(
      channelCheckbox('demo.thing.happened', 'email'),
    )
  })

  it('offers the in-app column alone when no presence provider is mounted', async () => {
    mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
    await screen.findByText('demo.thing.happened')
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

  it('marks a non-unsubscribable type unavailable with the note as its reason', async () => {
    const { rig } = mount([
      { key: 'authn.security.alert', unsubscribable: false, channels: ['in_app'] },
    ])
    await screen.findByText('authn.security.alert')
    expect(within(rowFor('authn.security.alert')).getByText(
      zhCN.preferences.unsubscribableNote,
    )).toBeInTheDocument()
    const checkbox = channelCheckbox('authn.security.alert', 'in_app')
    expect(checkbox).toHaveAttribute('aria-disabled', 'true')
    expect(checkbox).not.toBeDisabled()
    expect(checkbox).toHaveAccessibleDescription(
      zhCN.preferences.unsubscribableNote,
    )
    checkbox.focus()
    expect(checkbox).toHaveFocus()
    await userEvent.keyboard(' ')
    expect(rig.calls.some((call) => call.method === 'PUT')).toBe(false)
    expect(checkbox).toBeChecked()
  })

  it('raises the bare channel checkboxes to the 44px floor under a coarse pointer', async () => {
    mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
    await screen.findByText('demo.thing.happened')
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
    const alert = message.closest('[role="alert"]')
    expect(alert).not.toBeNull()
    expect(
      screen.getByRole('table').compareDocumentPosition(alert as Element) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })
})
