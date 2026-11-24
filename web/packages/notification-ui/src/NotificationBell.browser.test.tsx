import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import axe from 'axe-core'
import { page } from 'vitest/browser'
import { getNotificationInboxGetUnreadCountQueryKey } from '@speed/api-sdk'
import { tenantQueryKey } from '@speed/auth-core'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
} from '../test-utils/real-client.js'
import { renderWithProviders, TEST_TENANT_ID } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { NotificationBell } from './NotificationBell.js'

const BADGE_MAX = 99

const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

function bellName(unread: number): string {
  if (unread <= 0) {
    return enUS.bell.label
  }
  const shown = unread > BADGE_MAX ? `${BADGE_MAX}+` : String(unread)
  return enUS.bell.labelWithCount.replace('{{n}}', shown)
}

async function labelInName(scope: Element): Promise<{
  readonly violations: readonly axe.Result[]
  readonly incomplete: readonly axe.Result[]
}> {
  const result = await axe.run(scope, {
    runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
  })
  return { violations: result.violations, incomplete: result.incomplete }
}

describe('NotificationBell Label in Name in Chromium', () => {
  for (const unread of [1, 0, BADGE_MAX + 1] as const) {
    it(`keeps the unread badge inside the accessible name at ${unread} unread`, async () => {
      await page.viewport(900, 900)
      let release!: () => void
      const gate = new Promise<void>((resolve) => {
        release = resolve
      })
      const rig = makeRealClientRigWithoutSession(async () => {
        await gate
        return jsonResponse(200, { unread })
      })
      rig.store.set('access-1')
      const onClick = vi.fn()
      const { queryClient } = renderWithProviders(
        <NotificationBell onClick={onClick} />,
        { language: 'en-US' },
      )
      const unreadKey = tenantQueryKey(
        TEST_TENANT_ID,
        ...getNotificationInboxGetUnreadCountQueryKey(),
      )

      await waitFor(() =>
        expect(rig.calls.some((call) => call.path === UNREAD_PATH)).toBe(true),
      )

      if (unread === 0) {
        const placeholder = screen.getByRole('button', {
          name: bellName(0),
        })
        expect(placeholder).toBeInTheDocument()
        expect(queryClient.getQueryState(unreadKey)?.status).toBe('pending')
      }

      await act(async () => {
        release()
        await Promise.resolve()
      })
      await waitFor(() =>
        expect(queryClient.getQueryState(unreadKey)?.status).toBe('success'),
      )
      await act(async () => {
        await Promise.resolve()
      })
      expect(queryClient.getQueryState(unreadKey)?.status).toBe('success')

      const button = await screen.findByRole('button', {
        name: bellName(unread),
      })
      const { violations, incomplete } = await labelInName(button)
      expect(incomplete).toEqual([])
      expect(violations).toEqual([])
    })
  }

  it('settles Label in Name for the bell with an unread count', async () => {
    const rig = makeRealClientRigWithoutSession(() =>
      jsonResponse(200, { unread: 3 }),
    )
    rig.store.set('access-1')
    renderWithProviders(<NotificationBell onClick={vi.fn()} />, {
      language: 'en-US',
    })

    await screen.findByRole('button', { name: bellName(3) })
    await expectNoAxeViolations({
      decidedInBrowser: 'notificationBellUnread',
    })
  })
})
