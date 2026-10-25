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
import { screen } from '@testing-library/react'
import axe from 'axe-core'
import { page } from 'vitest/browser'
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { NotificationBell } from './NotificationBell.js'

/** The largest count the badge prints before switching to "99+". */
const BADGE_MAX = 99

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
      const rig = makeRealClientRigWithoutSession(() =>
        jsonResponse(200, { unread }),
      )
      rig.store.set('access-1')
      const onClick = vi.fn()
      renderWithProviders(<NotificationBell onClick={onClick} />, {
        language: 'en-US',
      })

      // Wait for the count read to land: the rule must judge the state the
      // badge carries, not the zero-count placeholder before the read.
      const button = await screen.findByRole('button', { name: bellName(unread) })
      const { violations, incomplete } = await labelInName(button)
      expect(incomplete).toEqual([])
      expect(violations).toEqual([])
    })
  }
})
