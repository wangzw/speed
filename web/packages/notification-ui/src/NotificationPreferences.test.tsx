/**
 * NotificationPreferences behaviour over the generated preference
 * operations: the matrix renders one row per type with a checkbox per
 * channel reflecting the effective set; toggling a channel PUTs that
 * type/channel with the target state and re-reads; a type the taxonomy
 * marks not unsubscribable renders a disabled row with the note; an empty
 * taxonomy renders the empty placeholder; and the surface passes axe.
 */

import { describe, expect, it } from 'vitest'
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
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { NotificationPreferences } from './NotificationPreferences.js'

const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

interface TypeRow {
  readonly key: string
  readonly unsubscribable: boolean
  channels: string[]
}

/** A stateful preference matrix the responder reads and mutates. */
function preferencesResponder(types: TypeRow[]): RealResponder {
  return (call: RealCall) => {
    if (call.path === TYPES_PATH && call.method === 'GET') {
      return jsonResponse(200, {
        types: types.map((type) => ({
          key: type.key,
          group: 'demo',
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
          group: 'demo',
          channels: type.channels,
          explicit: true,
        })),
      })
    }
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

function mount(types: TypeRow[]) {
  const rig = makeRealClientRigWithoutSession(preferencesResponder(types))
  rig.store.set('access-1')
  return { rig, ...renderWithProviders(<NotificationPreferences />) }
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
    const { rig } = mount([
      { key: 'demo.thing.happened', unsubscribable: true, channels: ['in_app'] },
    ])
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

  it('renders the empty placeholder when the taxonomy declares no types', async () => {
    mount([])
    expect(await screen.findByText(zhCN.preferences.empty.title)).toBeInTheDocument()
  })
})
