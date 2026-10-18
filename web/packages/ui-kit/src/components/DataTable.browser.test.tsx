/**
 * DataTable in a real browser: the per-column content width cap.
 *
 * The cap is a container query against the table's own root box, so only
 * a browser resolves it. These cases place the table in a fixed-width
 * container and assert the rendered cap: a primary-identity column
 * (`PRIMARY_COLUMN_MAX_WIDTH`) at 160px below the container's md and
 * 200px from it up, and a flat cap that does not grow. The capped cell's
 * `cellFullValue` copy control keeps its own full width inside the cap --
 * the cap bounds content and control together, never clipping the
 * control.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page } from 'vitest/browser'
import {
  DataTable,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
} from './DataTable.js'
import type { DataTableColumn } from './DataTable.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { uiKitResources } from '../resources.js'

interface Row {
  readonly id: string
  readonly name: string
}

const LONG_ID = 'account-identifier-with-a-long-unbroken-value-0000000001'
const LONG_TEXT = 'an unbroken descriptive value that is much wider than its cap'

/** The shipped en-US copy-control label for this fixed value. */
const COPY_LABEL = (
  uiKitResources['en-US'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue.replace('{{value}}', LONG_ID)

const ROW: Row = { id: LONG_ID, name: LONG_TEXT }

/** One primary-identity column: nowrap, full-value copy control, the
 * shared container-query cap. */
const PRIMARY_COLUMNS: readonly DataTableColumn<Row>[] = [
  {
    id: 'id',
    header: 'Identifier',
    cellWrap: 'nowrap',
    cellFullValue: (row) => row.id,
    contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
    cell: (row) => row.id,
  },
]

/** One flat-cap column with no copy control. */
const FLAT_COLUMNS: readonly DataTableColumn<Row>[] = [
  {
    id: 'name',
    header: 'Name',
    cellWrap: 'nowrap',
    contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
    cell: (row) => row.name,
  },
]

/** Renders the table inside a fixed-width box, the host's placement. */
function renderInWidth(width: number, columns: readonly DataTableColumn<Row>[]) {
  return renderWithProviders(
    <div style={{ width }}>
      <DataTable rows={[ROW]} columns={columns} rowKey={(row) => row.id} />
    </div>,
    // en-US keeps the measured text Latin-only under the pinned font.
    { language: 'en-US' },
  )
}

describe('DataTable content caps in Chromium', () => {
  for (const { width, cap } of [
    { width: 800, cap: PRIMARY_COLUMN_MAX_WIDTH.belowMd },
    { width: 1000, cap: PRIMARY_COLUMN_MAX_WIDTH.fromMd },
  ]) {
    it(`caps a primary-identity cell at ${cap}px in a ${width}px container`, async () => {
      await page.viewport(1280, 900)
      renderInWidth(width, PRIMARY_COLUMNS)

      const text = await screen.findByText(LONG_ID)
      await waitFor(() => {
        expect(text.getBoundingClientRect().width).toBeGreaterThan(0)
      })

      // The inline-flex wrapper is the element the cap is declared on; it
      // holds the shrinking text and the pinned copy control.
      const wrapper = text.parentElement
      expect(wrapper).not.toBeNull()
      const wrapperWidth = wrapper?.getBoundingClientRect().width ?? Infinity
      expect(wrapperWidth).toBeLessThanOrEqual(cap + 0.5)

      // The copy control keeps its whole width inside that cap instead of
      // being clipped by it.
      const copy = screen.getByRole('button', { name: COPY_LABEL })
      const copyRect = copy.getBoundingClientRect()
      expect(copyRect.width).toBeGreaterThan(0)
      expect(copyRect.right).toBeLessThanOrEqual(
        (wrapper?.getBoundingClientRect().right ?? 0) + 0.5,
      )
    })
  }

  it('keeps a flat long-text cap the same at both container tiers', async () => {
    await page.viewport(1280, 900)

    for (const width of [800, 1000]) {
      const { unmount } = renderInWidth(width, FLAT_COLUMNS)
      const text = await screen.findByText(LONG_TEXT)
      await waitFor(() => {
        expect(text.getBoundingClientRect().width).toBeGreaterThan(0)
      })
      expect(text.getBoundingClientRect().width).toBeLessThanOrEqual(
        LONG_TEXT_COLUMN_MAX_WIDTH + 0.5,
      )
      unmount()
    }
  })

  it('keeps an uncapped cell at the table container edge rather than the page edge', async () => {
    await page.viewport(1280, 900)
    const columns: readonly DataTableColumn<Row>[] = [
      { id: 'name', header: 'Name', cell: (row) => row.name },
    ]
    renderInWidth(600, columns)

    const text = await screen.findByText(LONG_TEXT)
    // The TableContainer is the horizontal scroll fallback: content wider
    // than the host's container scrolls inside it, never out of the page.
    const wrapper = text.closest<HTMLElement>('.MuiTableContainer-root')
    expect(wrapper).not.toBeNull()
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      document.documentElement.clientWidth,
    )
  })
})
