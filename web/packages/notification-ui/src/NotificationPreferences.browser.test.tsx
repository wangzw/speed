import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import Box from '@mui/material/Box'
import { page, userEvent } from 'vitest/browser'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { NotificationPreferences } from './NotificationPreferences.js'
import { NotificationPresenceProvider } from './NotificationPresence.js'

const PREFERENCES_PATH = '/api/v1/notifications/preferences'
const TYPES_PATH = '/api/v1/notifications/types'

const TYPE = {
  key: 'billing.invoice.due',
  group: 'billing',
  channels: ['in_app', 'email', 'sms'],
}

const responder: RealResponder = (call: RealCall) => {
  if (call.path === TYPES_PATH && call.method === 'GET') {
    return jsonResponse(200, {
      types: [
        {
          key: TYPE.key,
          group: TYPE.group,
          default_channels: TYPE.channels,
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
          type_key: TYPE.key,
          group: TYPE.group,
          channels: TYPE.channels,
          explicit: true,
        },
      ],
    })
  }
  return jsonResponse(404, { code: 'notification.bad_request' })
}

describe('NotificationPreferences column headers at 375 in Chromium', () => {
  it('renders the in-app header on one line', async () => {
    await page.viewport(375, 900)
    const rig = makeRealClientRigWithoutSession(responder)
    rig.store.set('access-1')
    renderWithProviders(
      <Box sx={{ p: 3 }}>
        <NotificationPresenceProvider preferences channels={['email', 'sms']}>
          <NotificationPreferences />
        </NotificationPresenceProvider>
      </Box>,
      { language: 'en-US' },
    )

    const header = await screen.findByRole('columnheader', {
      name: enUS.preferences.channel.in_app,
    })
    await document.fonts.ready

    const range = document.createRange()
    range.selectNodeContents(header)
    const tops = Array.from(range.getClientRects()).map((rect) =>
      Math.round(rect.top),
    )
    expect(new Set(tops).size).toBe(1)
  })
})

function deferredWrite(): {
  readonly promise: Promise<void>
  readonly resolve: () => void
} {
  let resolve!: () => void
  const promise = new Promise<void>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  return parts.join(' ')
}

describe('NotificationPreferences write focus in Chromium', () => {
  it('keeps focus on the toggled cell while its write is in flight and after it settles', async () => {
    await page.viewport(1200, 900)
    const gate = deferredWrite()
    const type = {
      key: 'demo.thing.happened',
      group: 'demo',
      channels: ['in_app'] as string[],
    }
    const holdingResponder: RealResponder = (call: RealCall) => {
      if (call.path === TYPES_PATH && call.method === 'GET') {
        return jsonResponse(200, {
          types: [
            {
              key: type.key,
              group: type.group,
              default_channels: type.channels,
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
              type_key: type.key,
              group: type.group,
              channels: type.channels,
              explicit: true,
            },
          ],
        })
      }
      if (call.method === 'PUT') {
        return gate.promise.then(() => {
          type.channels = ['in_app', 'email']
          return jsonResponse(200, {})
        })
      }
      return jsonResponse(404, { code: 'notification.bad_request' })
    }
    const rig = makeRealClientRigWithoutSession(holdingResponder)
    rig.store.set('access-1')
    renderWithProviders(
      <NotificationPresenceProvider preferences channels={['email']}>
        <NotificationPreferences />
      </NotificationPresenceProvider>,
      { language: 'en-US' },
    )
    const checkbox = await screen.findByRole('checkbox', {
      name: enUS.preferences.channelToggleAria
        .replace('{{type}}', type.key)
        .replace('{{channel}}', enUS.preferences.channel.email),
    })
    expect(checkbox).not.toBeChecked()

    await userEvent.click(checkbox)
    await nextFrame()

    expect(
      checkbox,
      `focus while the write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(checkbox).toHaveAttribute('aria-busy', 'true')
    expect(checkbox).toHaveAttribute('aria-disabled', 'true')
    expect(checkbox).not.toBeChecked()

    gate.resolve()
    await waitFor(() =>
      expect(checkbox).not.toHaveAttribute('aria-disabled', 'true'),
    )
    expect(
      checkbox,
      `focus after the write settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(checkbox).not.toHaveAttribute('aria-busy', 'true')
    await waitFor(() => expect(checkbox).toBeChecked())
  })
})
