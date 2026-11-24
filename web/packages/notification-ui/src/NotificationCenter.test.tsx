import { describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import { screen, waitFor } from '@testing-library/react'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { NotificationCenter } from './NotificationCenter.js'
import { NotificationPresenceProvider } from './NotificationPresence.js'

const MESSAGES_PATH = '/api/v1/notifications/messages'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'
const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

function responder(): RealResponder {
  return (call: RealCall) => {
    if (call.path === MESSAGES_PATH && call.method === 'GET') {
      return jsonResponse(200, { messages: [], total: 0 })
    }
    if (call.path === UNREAD_PATH) {
      return jsonResponse(200, { unread: 0 })
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
    return jsonResponse(404, { code: 'notification.bad_request' })
  }
}

function mount(ui: ReactElement) {
  const rig = makeRealClientRigWithoutSession(responder())
  rig.store.set('access-1')
  return { rig, ...renderWithProviders(ui) }
}

function columnLabels(): string[] {
  return screen
    .getAllByRole('columnheader')
    .map((header) => header.textContent ?? '')
}

describe('NotificationCenter', () => {
  it('renders only the inbox and reads no preference data without a provider', async () => {
    const { rig } = mount(<NotificationCenter />)
    expect(await screen.findByText(zhCN.inbox.empty.title)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.preferences.title)).toBeNull()
    expect(rig.calls.some((call) => call.path === PREFERENCES_PATH)).toBe(false)
    expect(rig.calls.some((call) => call.path === TYPES_PATH)).toBe(false)
  })

  it('renders the preference matrix when the feature is present', async () => {
    mount(
      <NotificationPresenceProvider preferences channels={[]}>
        <NotificationCenter />
      </NotificationPresenceProvider>,
    )
    expect(await screen.findByText(zhCN.preferences.title)).toBeInTheDocument()
    expect(await screen.findByText('demo.thing.happened')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('renders the optional channels the presence provider serves, and only those', async () => {
    mount(
      <NotificationPresenceProvider preferences channels={['email']}>
        <NotificationCenter />
      </NotificationPresenceProvider>,
    )
    expect(await screen.findByText(zhCN.preferences.title)).toBeInTheDocument()
    await waitFor(() =>
      expect(columnLabels()).toEqual([
        zhCN.preferences.column.type,
        zhCN.preferences.channel.in_app,
        zhCN.preferences.channel.email,
      ]),
    )
    expect(
      screen.queryByRole('columnheader', {
        name: zhCN.preferences.channel.sms,
      }),
    ).toBeNull()
  })

  it('renders the sms column when only that channel is served', async () => {
    mount(
      <NotificationPresenceProvider preferences channels={['sms']}>
        <NotificationCenter />
      </NotificationPresenceProvider>,
    )
    expect(await screen.findByText(zhCN.preferences.title)).toBeInTheDocument()
    await waitFor(() =>
      expect(columnLabels()).toEqual([
        zhCN.preferences.column.type,
        zhCN.preferences.channel.in_app,
        zhCN.preferences.channel.sms,
      ]),
    )
    expect(
      screen.queryByRole('columnheader', {
        name: zhCN.preferences.channel.email,
      }),
    ).toBeNull()
  })

  it('keeps the preference half hidden when the feature is absent even with channels', async () => {
    const { rig } = mount(
      <NotificationPresenceProvider preferences={false} channels={['sms']}>
        <NotificationCenter />
      </NotificationPresenceProvider>,
    )
    expect(await screen.findByText(zhCN.inbox.empty.title)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.preferences.title)).toBeNull()
    expect(rig.calls.some((call) => call.path === PREFERENCES_PATH)).toBe(false)
  })

  it('passes the optional inbox and preferences props through', async () => {
    mount(
      <NotificationPresenceProvider preferences channels={[]}>
        <NotificationCenter
          inbox={{ variant: 'embedded', headingLevel: 'h3' }}
          preferences={{ typeLabel: () => 'Friendly type' }}
        />
      </NotificationPresenceProvider>,
    )
    expect(
      await screen.findByRole('heading', { level: 3, name: zhCN.inbox.title }),
    ).toBeInTheDocument()
    expect(await screen.findByText('Friendly type')).toBeInTheDocument()
  })
})
