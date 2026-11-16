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
import { tableContainerClasses } from '@mui/material/TableContainer'
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
    const wrapper = text.closest<HTMLElement>(
      `.${tableContainerClasses.root}`,
    )
    expect(wrapper).not.toBeNull()
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      document.documentElement.clientWidth,
    )
  })
})

/** One pixel of tolerance for subpixel layout rounding. */
const PIN_TOLERANCE = 0.5

/**
 * The alpha channel of a computed CSS colour. A hex or rgb() form is
 * opaque; rgba() carries its own alpha as the fourth component.
 */
function alphaOf(color: string): number {
  const rgba = /^rgba?\(([^)]+)\)$/.exec(color)
  const body = rgba?.[1]
  if (body === undefined) return 1
  const parts = body.split(',').map((part) => Number.parseFloat(part))
  return parts[3] ?? 1
}

describe('DataTable row-actions pinning in Chromium', () => {
  interface PinRow {
    readonly name: string
    readonly email: string
  }

  const PIN_ROW: PinRow = { name: LONG_TEXT, email: LONG_ID }

  /** Two unbroken main columns plus the row-actions column the host
   * places last, as the accounts page does. */
  const PIN_COLUMNS: readonly DataTableColumn<PinRow>[] = [
    {
      id: 'name',
      header: 'Name',
      cellWrap: 'nowrap',
      cell: (row) => row.name,
    },
    {
      id: 'email',
      header: 'Email',
      cellWrap: 'nowrap',
      cell: (row) => row.email,
    },
    {
      id: 'actions',
      header: 'Actions',
      cellWrap: 'nowrap',
      cell: () => (
        <div style={{ display: 'flex', columnGap: 8 }}>
          <button type="button">Manage</button>
          <button type="button">Delete</button>
        </div>
      ),
    },
  ]

  it('pins the last column to the right edge while the main columns scroll', async () => {
    await page.viewport(1280, 900)
    renderWithProviders(
      <div style={{ width: 280 }}>
        <DataTable
          rows={[PIN_ROW]}
          columns={PIN_COLUMNS}
          rowKey={(row) => row.email}
          pinLastColumn
        />
      </div>,
      { language: 'en-US' },
    )

    const manage = await screen.findByRole('button', { name: 'Manage' })
    const remove = screen.getByRole('button', { name: 'Delete' })
    await waitFor(() => {
      expect(manage.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const container = manage.closest<HTMLElement>(
      `.${tableContainerClasses.root}`,
    )
    expect(container).not.toBeNull()
    // The table is genuinely wider than its host box, so the wrapper can
    // scroll horizontally and the pin is observable.
    expect(container?.scrollWidth).toBeGreaterThan(container?.clientWidth ?? 0)

    const box = container?.getBoundingClientRect() as DOMRect
    for (const control of [manage, remove]) {
      const rect = control.getBoundingClientRect()
      expect(rect.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
      expect(rect.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
    }

    const before = manage.getBoundingClientRect().left
    if (container !== null) {
      container.scrollLeft = 40
    }
    await waitFor(() => {
      expect(container?.scrollLeft ?? 0).toBeGreaterThan(0)
    })

    // The main columns have moved under the pinned column; the pinned
    // controls have not moved with them.
    await waitFor(() => {
      expect(
        Math.abs(manage.getBoundingClientRect().left - before),
      ).toBeLessThanOrEqual(PIN_TOLERANCE)
    })
    const after = manage.getBoundingClientRect()
    expect(after.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
    expect(after.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
  })

  it('keeps the pinned band opaque when the surface fill is translucent', async () => {
    await page.viewport(1280, 900)
    renderWithProviders(
      <div style={{ width: 280 }}>
        <DataTable
          rows={[PIN_ROW]}
          columns={PIN_COLUMNS}
          rowKey={(row) => row.email}
          pinLastColumn
        />
      </div>,
      {
        language: 'en-US',
        // The glass preset's container alpha, projected onto the default
        // tokens: the surface fill no longer guarantees opaqueness, so the
        // pinned cell must supply an opaque base underneath it.
        projectTokens: { surface: { container: { opacity: 0.4 } } },
      },
    )

    const manage = await screen.findByRole('button', { name: 'Manage' })
    await waitFor(() => {
      expect(manage.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const container = manage.closest<HTMLElement>(
      `.${tableContainerClasses.root}`,
    )
    expect(container).not.toBeNull()
    // The pin is real only while the table overflows its container.
    expect(container?.scrollWidth).toBeGreaterThan(container?.clientWidth ?? 0)

    const headers = Array.from(container?.querySelectorAll('thead th') ?? [])
    const bodyCells = Array.from(
      container?.querySelectorAll('tbody tr:first-child td') ?? [],
    )
    const pinnedHeader = headers[headers.length - 1] as HTMLElement
    const pinnedBody = bodyCells[bodyCells.length - 1] as HTMLElement

    await waitFor(() => {
      expect(getComputedStyle(pinnedHeader).position).toBe('sticky')
      expect(getComputedStyle(pinnedBody).position).toBe('sticky')
    })

    for (const cell of [pinnedHeader, pinnedBody]) {
      const computed = getComputedStyle(cell)
      // The base layer is opaque, so the main columns cannot show through.
      expect(alphaOf(computed.backgroundColor)).toBe(1)
      // ... while the translucent surface fill stays the visible layer, so
      // the pinned band still reads as part of the table.
      expect(computed.backgroundImage).toContain('0.4')
    }
  })

  it('leaves the last cells untouched when the table fits its container', async () => {
    await page.viewport(1280, 900)

    const FITS_ROW: PinRow = { name: 'Ada', email: 'ada@example.com' }
    const FITS_COLUMNS: readonly DataTableColumn<PinRow>[] = [
      { id: 'name', header: 'Name', cellWrap: 'nowrap', cell: (row) => row.name },
      {
        id: 'actions',
        header: 'Actions',
        cellWrap: 'nowrap',
        cell: () => <button type="button">Manage</button>,
      },
    ]

    renderWithProviders(
      <div style={{ width: 900 }}>
        <DataTable
          rows={[FITS_ROW]}
          columns={FITS_COLUMNS}
          rowKey={(row) => row.email}
          pinLastColumn
        />
      </div>,
      { language: 'en-US' },
    )

    const manage = await screen.findByRole('button', { name: 'Manage' })
    await waitFor(() => {
      expect(manage.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const container = manage.closest<HTMLElement>(
      `.${tableContainerClasses.root}`,
    )
    expect(container).not.toBeNull()
    // The premise of this case: the table genuinely fits its container.
    await waitFor(() => {
      expect(container?.scrollWidth ?? 0).toBeLessThanOrEqual(
        container?.clientWidth ?? 0,
      )
    })

    const headers = Array.from(container?.querySelectorAll('thead th') ?? [])
    const bodyCells = Array.from(
      container?.querySelectorAll('tbody tr:first-child td') ?? [],
    )
    const firstHeader = headers[0] as HTMLElement
    const lastHeader = headers[headers.length - 1] as HTMLElement
    const firstBody = bodyCells[0] as HTMLElement
    const lastBody = bodyCells[bodyCells.length - 1] as HTMLElement

    // A fitting table is unaffected: the last cells sit in normal flow and
    // read exactly like their row siblings, with no start-edge divider.
    expect(getComputedStyle(lastHeader).position).toBe('static')
    expect(getComputedStyle(lastBody).position).toBe('static')
    expect(getComputedStyle(lastHeader).backgroundColor).toBe(
      getComputedStyle(firstHeader).backgroundColor,
    )
    expect(getComputedStyle(lastBody).backgroundColor).toBe(
      getComputedStyle(firstBody).backgroundColor,
    )
    expect(getComputedStyle(lastHeader).boxShadow).toBe('none')
    expect(getComputedStyle(lastBody).boxShadow).toBe('none')
  })

  it('carries the row hover highlight into the pinned cell', async () => {
    await page.viewport(1280, 900)

    // A short first column stays clear of the pinned band, so the pointer
    // can rest on the row without the pinned controls intercepting it.
    const HOVER_ROW: PinRow = { name: 'Ada', email: LONG_ID }
    const HOVER_COLUMNS: readonly DataTableColumn<PinRow>[] = [
      { id: 'name', header: 'Name', cellWrap: 'nowrap', cell: (row) => row.name },
      { id: 'email', header: 'Email', cellWrap: 'nowrap', cell: (row) => row.email },
      {
        id: 'actions',
        header: 'Actions',
        cellWrap: 'nowrap',
        cell: () => <button type="button">Manage</button>,
      },
    ]

    renderWithProviders(
      <div style={{ width: 280 }}>
        <DataTable
          rows={[HOVER_ROW]}
          columns={HOVER_COLUMNS}
          rowKey={(row) => row.email}
          pinLastColumn
        />
      </div>,
      { language: 'en-US' },
    )

    const manage = await screen.findByRole('button', { name: 'Manage' })
    await waitFor(() => {
      expect(manage.getBoundingClientRect().width).toBeGreaterThan(0)
    })
    await document.fonts.ready

    const container = manage.closest<HTMLElement>(
      `.${tableContainerClasses.root}`,
    )
    expect(container).not.toBeNull()
    expect(container?.scrollWidth).toBeGreaterThan(container?.clientWidth ?? 0)

    const row = manage.closest('tr')
    expect(row).not.toBeNull()
    const cells = Array.from(row?.querySelectorAll('td') ?? [])
    const pinned = cells[cells.length - 1] as HTMLElement
    expect(getComputedStyle(pinned).position).toBe('sticky')

    // This tier runs a hovering, fine pointer, which is what turns the row
    // highlight on in the first place.
    expect(
      window.matchMedia('(hover: hover) and (pointer: fine)').matches,
    ).toBe(true)

    const restingImage = getComputedStyle(pinned).backgroundImage
    await page.getByText('Ada').hover()
    await waitFor(() => {
      expect(getComputedStyle(row as HTMLElement).backgroundColor).not.toBe(
        'rgba(0, 0, 0, 0)',
      )
    })

    const hovered = getComputedStyle(pinned)
    // The same layer the row's own highlight paints is present in the
    // pinned cell, so the highlight no longer stops at the pinned edge.
    expect(hovered.backgroundImage).not.toBe(restingImage)
    expect(hovered.backgroundImage).toContain(
      getComputedStyle(row as HTMLElement).backgroundColor,
    )
    // ... and the opaque base survives underneath the extra layer.
    expect(alphaOf(hovered.backgroundColor)).toBe(1)
  })
})
