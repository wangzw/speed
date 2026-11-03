/**
 * ActionRow in a real browser: one group, right-aligned, with adjacent
 * actions exactly one spacing unit apart and the primary action closing
 * the group at the right edge.
 *
 * jsdom lays nothing out, so only the browser tier can measure the boxes.
 * One case sits in a fixed-width container much wider than the two
 * controls, so the measured positions are the row's own alignment rather
 * than a wrap forced by a narrow width. The other sits in a container
 * that cannot hold all three controls on one line, so the wrap itself is
 * the subject: the controls move to a second visual line, the spacing
 * token separates them both across and between the lines, and nothing
 * spills past the container.
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

  it('wraps in a narrow container, one token between controls and between lines', async () => {
    await page.viewport(900, 700)

    // Two fixed-width controls fit on one line (100 + 8 + 100 = 208), the
    // third cannot (a further 100 + 8), so the row must wrap. The
    // container is narrower than the controls' total width and nothing
    // else constrains the wrap.
    const CONTAINER_WIDTH = 240
    const CONTROL_WIDTH = 100

    renderWithProviders(
      <div style={{ width: CONTAINER_WIDTH }}>
        <ActionRow>
          <ActionButton style={{ width: CONTROL_WIDTH }}>Cancel</ActionButton>
          <ActionButton style={{ width: CONTROL_WIDTH }}>Retry</ActionButton>
          <ActionButton emphasis="primary" style={{ width: CONTROL_WIDTH }}>
            Save
          </ActionButton>
        </ActionRow>
      </div>,
      { language: 'en-US' },
    )

    const cancel = await screen.findByRole('button', { name: 'Cancel' })
    const retry = screen.getByRole('button', { name: 'Retry' })
    const save = screen.getByRole('button', { name: 'Save' })
    await waitFor(() => {
      expect(cancel.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const row = actionRowOf(save)
    // All three controls belong to the one row.
    expect(actionRowOf(cancel)).toBe(row)
    expect(actionRowOf(retry)).toBe(row)

    const controls = [cancel, retry, save]
    const boxes = controls.map((control) => control.getBoundingClientRect())
    const rowBox = row.getBoundingClientRect()

    // The fixed-width container is the measure, not a wider viewport.
    expect(rowBox.width).toBeCloseTo(CONTAINER_WIDTH, 0)

    // More than one visual line: the narrow container forced a wrap.
    const firstLineTop = Math.min(...boxes.map((box) => box.top))
    const firstLine = boxes.filter(
      (box) => Math.abs(box.top - firstLineTop) <= TOLERANCE,
    )
    const nextLines = boxes.filter(
      (box) => Math.abs(box.top - firstLineTop) > TOLERANCE,
    )
    expect(nextLines.length).toBeGreaterThan(0)

    // Adjacent controls on a line are exactly one spacing token apart.
    const firstLineByLeft = [...firstLine].sort((a, b) => a.left - b.left)
    expect(firstLineByLeft.length).toBeGreaterThanOrEqual(2)
    for (let i = 1; i < firstLineByLeft.length; i += 1) {
      expect(
        firstLineByLeft[i].left - firstLineByLeft[i - 1].right,
      ).toBeCloseTo(SPACING_UNIT, 0)
    }

    // The next line sits exactly one spacing token below the first.
    const firstLineBottom = Math.max(...firstLine.map((box) => box.bottom))
    const nextLineTop = Math.min(...nextLines.map((box) => box.top))
    expect(
      Math.abs(nextLineTop - firstLineBottom - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)

    // Each control stays fully inside the container.
    for (const box of boxes) {
      expect(box.left).toBeGreaterThanOrEqual(rowBox.left - TOLERANCE)
      expect(box.right).toBeLessThanOrEqual(rowBox.right + TOLERANCE)
      expect(box.top).toBeGreaterThanOrEqual(rowBox.top - TOLERANCE)
      expect(box.bottom).toBeLessThanOrEqual(rowBox.bottom + TOLERANCE)
    }

    // No control overlaps another: the token gap is real, not just a
    // box-edge cap.
    for (let i = 0; i < boxes.length; i += 1) {
      for (let j = i + 1; j < boxes.length; j += 1) {
        const a = boxes[i]
        const b = boxes[j]
        const overlapX = a.left < b.right - TOLERANCE && b.left < a.right - TOLERANCE
        const overlapY =
          a.top < b.bottom - TOLERANCE && b.top < a.bottom - TOLERANCE
        expect(overlapX && overlapY).toBe(false)
      }
    }

    // The row contains its controls instead of overflowing horizontally.
    expect(row.scrollWidth).toBeLessThanOrEqual(row.clientWidth + TOLERANCE)
  })
})
