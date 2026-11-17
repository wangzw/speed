/**
 * NotificationBell in a real browser: axe's label-content-name-mismatch
 * (WCAG 2.5.3 Label in Name).
 *
 * The rule reads a control's visible text from a rendered box through the
 * canvas text-measurement API. jsdom answers `getContext('2d')` with null, so
 * the check throws there and comes back indeterminate for every node; only a
 * real browser can pass or fail it. These cases settle the rule for the
 * icon-only bell at one unread, zero unread, and above the badge cap: the
 * badge glyph is rendered inside an aria-hidden span, and the button's
 * accessible name carries the same text, so the visible text is part of the
 * accessible name.
 */
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

/** The largest count the badge prints before switching to "99+". */
const BADGE_MAX = 99

/** The read whose answer drives the badge. */
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

/**
 * The shipped accessible name for one unread count. Above BADGE_MAX the badge
 * prints the clipped "99+", so the name carries that same text.
 */
function bellName(unread: number): string {
  if (unread <= 0) {
    return enUS.bell.label
  }
  const shown = unread > BADGE_MAX ? `${BADGE_MAX}+` : String(unread)
  return enUS.bell.labelWithCount.replace('{{n}}', shown)
}

/**
 * Runs only the Label in Name rule: the full page-oriented set would fail an
 * isolated component render for reasons this tier does not own.
 */
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
      // The count read is gated and released by the case: the scan must not be
      // reachable until the answer has landed. At zero unread the placeholder
      // and the settled badge carry the same accessible name, so a name-based
      // wait cannot tell them apart -- which is how the pre-fix case scanned
      // the placeholder (the fail-before witness below).
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

      // The read is in flight: issued, still unresolved.
      await waitFor(() =>
        expect(rig.calls.some((call) => call.path === UNREAD_PATH)).toBe(true),
      )

      if (unread === 0) {
        // The old wait (`findByRole('button', { name: bellName(0) })`) resolves
        // right here, while the read is still pending, and the scan then
        // records the placeholder. Pinning it shows the name cannot be the
        // gate at zero.
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
      // The cache write and the hook's commit are separate ticks; flush the
      // commit so the rule measures the settled badge (or its deliberate
      // absence at zero), never the placeholder.
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
    // The unit suite's full scan of the bell cannot decide Label in Name
    // (jsdom measures no text); this case is the verdict it points at (see
    // @speed/test-utils/axe's BROWSER_DETERMINATIONS) and the whole page
    // rule set runs over the same settled state.
    const rig = makeRealClientRigWithoutSession(() =>
      jsonResponse(200, { unread: 3 }),
    )
    rig.store.set('access-1')
    renderWithProviders(<NotificationBell onClick={vi.fn()} />, {
      language: 'en-US',
    })

    await screen.findByRole('button', { name: bellName(3) })
    await expectNoAxeViolations()
  })
})
