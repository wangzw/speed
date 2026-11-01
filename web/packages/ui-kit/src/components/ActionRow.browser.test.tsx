/**
 * ActionRow in a real browser: one group, right-aligned, with adjacent
 * actions exactly one spacing unit apart and the primary action closing
 * the group at the right edge.
 *
 * jsdom lays nothing out, so only the browser tier can measure the boxes.
 * The row sits in a fixed-width container much wider than the two
 * controls, so the measured positions are the row's own alignment rather
 * than a wrap forced by a narrow width.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page } from 'vitest/browser'
import { defaultTokens } from '@speed/tokens'
import { renderWithProviders } from '../../test-utils/render.js'
import { ActionButton, ActionRow } from './ActionRow.js'

/** The single spacing token an action row puts between adjacent controls. */
const SPACING_UNIT = defaultTokens.spacing.unit

/** One pixel of tolerance for subpixel layout rounding. */
const TOLERANCE = 0.5

/**
 * The row an action belongs to, found by the row's own marker rather than
 * by guessing that a child's parent is the row.
 */
function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

describe('ActionRow in Chromium', () => {
  it('keeps the actions adjacent, one spacing unit apart, with the primary rightmost', async () => {
    await page.viewport(900, 700)

    renderWithProviders(
      <div style={{ width: 480 }}>
        <ActionRow>
          <ActionButton>Cancel</ActionButton>
          <ActionButton emphasis="primary">Save</ActionButton>
        </ActionRow>
      </div>,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const save = screen.getByRole('button', { name: 'Save' })
    await waitFor(() => {
      expect(cancel.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const row = actionRowOf(save)
    expect(actionRowOf(cancel)).toBe(row)

    const cancelBox = cancel.getBoundingClientRect()
    const saveBox = save.getBoundingClientRect()
    const rowBox = row.getBoundingClientRect()

    // One group: both controls share a line.
    expect(Math.abs(cancelBox.top - saveBox.top)).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(cancelBox.bottom - saveBox.bottom)).toBeLessThanOrEqual(
      TOLERANCE,
    )

    // Adjacent, with the one spacing token between them and nothing else.
    expect(saveBox.left).toBeGreaterThanOrEqual(cancelBox.right)
    expect(
      Math.abs(saveBox.left - cancelBox.right - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)

    // The primary action is the rightmost control and the group ends at
    // the row's right edge: the row is right-aligned, not spread between
    // two ends.
    expect(saveBox.left).toBeGreaterThan(cancelBox.left)
    expect(Math.abs(saveBox.right - rowBox.right)).toBeLessThanOrEqual(TOLERANCE)
  })
})
