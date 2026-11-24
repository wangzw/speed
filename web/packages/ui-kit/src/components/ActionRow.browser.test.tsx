
import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import { defaultTokens } from '@speed/tokens'
import { renderWithProviders } from '../../test-utils/render.js'
import { ActionButton, ActionRow } from './ActionRow.js'

const SPACING_UNIT = defaultTokens.spacing.unit

const TOLERANCE = 0.5

function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

function boxAt(boxes: readonly DOMRect[], index: number): DOMRect {
  const box = boxes[index]
  if (box === undefined) {
    throw new Error(`no measured box at index ${index}`)
  }
  return box
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

    expect(Math.abs(cancelBox.top - saveBox.top)).toBeLessThanOrEqual(TOLERANCE)
    expect(Math.abs(cancelBox.bottom - saveBox.bottom)).toBeLessThanOrEqual(
      TOLERANCE,
    )

    expect(saveBox.left).toBeGreaterThanOrEqual(cancelBox.right)
    expect(
      Math.abs(saveBox.left - cancelBox.right - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)

    expect(saveBox.left).toBeGreaterThan(cancelBox.left)
    expect(Math.abs(saveBox.right - rowBox.right)).toBeLessThanOrEqual(TOLERANCE)
  })

  it('wraps in a narrow container, one token between controls and between lines', async () => {
    await page.viewport(900, 700)

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
    expect(actionRowOf(cancel)).toBe(row)
    expect(actionRowOf(retry)).toBe(row)

    const controls = [cancel, retry, save]
    const boxes = controls.map((control) => control.getBoundingClientRect())
    const rowBox = row.getBoundingClientRect()

    expect(rowBox.width).toBeCloseTo(CONTAINER_WIDTH, 0)

    const firstLineTop = Math.min(...boxes.map((box) => box.top))
    const firstLine = boxes.filter(
      (box) => Math.abs(box.top - firstLineTop) <= TOLERANCE,
    )
    const nextLines = boxes.filter(
      (box) => Math.abs(box.top - firstLineTop) > TOLERANCE,
    )
    expect(nextLines.length).toBeGreaterThan(0)

    const firstLineByLeft = [...firstLine].sort((a, b) => a.left - b.left)
    expect(firstLineByLeft.length).toBeGreaterThanOrEqual(2)
    for (let i = 1; i < firstLineByLeft.length; i += 1) {
      expect(
        boxAt(firstLineByLeft, i).left - boxAt(firstLineByLeft, i - 1).right,
      ).toBeCloseTo(SPACING_UNIT, 0)
    }

    const firstLineBottom = Math.max(...firstLine.map((box) => box.bottom))
    const nextLineTop = Math.min(...nextLines.map((box) => box.top))
    expect(
      Math.abs(nextLineTop - firstLineBottom - SPACING_UNIT),
    ).toBeLessThanOrEqual(TOLERANCE)

    for (const box of boxes) {
      expect(box.left).toBeGreaterThanOrEqual(rowBox.left - TOLERANCE)
      expect(box.right).toBeLessThanOrEqual(rowBox.right + TOLERANCE)
      expect(box.top).toBeGreaterThanOrEqual(rowBox.top - TOLERANCE)
      expect(box.bottom).toBeLessThanOrEqual(rowBox.bottom + TOLERANCE)
    }

    for (let i = 0; i < boxes.length; i += 1) {
      for (let j = i + 1; j < boxes.length; j += 1) {
        const a = boxAt(boxes, i)
        const b = boxAt(boxes, j)
        const overlapX = a.left < b.right - TOLERANCE && b.left < a.right - TOLERANCE
        const overlapY =
          a.top < b.bottom - TOLERANCE && b.top < a.bottom - TOLERANCE
        expect(overlapX && overlapY).toBe(false)
      }
    }

    expect(row.scrollWidth).toBeLessThanOrEqual(row.clientWidth + TOLERANCE)
  })

  it('keeps its width and focus when the focused action becomes busy', async () => {
    await page.viewport(900, 700)

    function BusyOnActivate() {
      const [busy, setBusy] = useState(false)
      return (
        <ActionButton busy={busy} onClick={() => setBusy(true)}>
          Save status
        </ActionButton>
      )
    }

    renderWithProviders(<BusyOnActivate />, { language: 'en-US' })

    const save = await screen.findByRole('button', { name: 'Save status' })
    await waitFor(() => {
      expect(save.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready
    const before = save.getBoundingClientRect()

    await userEvent.click(save)
    await waitFor(() => {
      expect(save).toHaveAttribute('aria-busy', 'true')
    })
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => resolve())
    })
    await document.fonts.ready
    const after = save.getBoundingClientRect()

    expect(Math.abs(after.width - before.width)).toBeLessThanOrEqual(TOLERANCE)
    expect(save).toHaveFocus()
  })
})
