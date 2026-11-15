/**
 * The browser tier's font pin, as the theme's components see it.
 *
 * The tier's setup (web/test-utils/src/browser-setup.ts) pins every
 * element's text to a metric-stable family so the geometry cases measure the
 * same widths on macOS and on the CI image. The theme declares its own font
 * stack on MUI's component classes -- a table cell, a chip, a button -- and a
 * declared value beats an inherited one, so a pin that only reaches the
 * body's inherited family leaves exactly the text those cases measure in each
 * platform's own system font. This case renders those components under the
 * speed theme and holds that each resolves to the pinned family.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import Button, { buttonClasses } from '@mui/material/Button'
import Chip, { chipClasses } from '@mui/material/Chip'
import { tableCellClasses } from '@mui/material/TableCell'
import { DataTable } from '../components/DataTable.js'
import type { DataTableColumn } from '../components/DataTable.js'
import { renderWithProviders } from '../../test-utils/render.js'

interface Row {
  readonly id: string
}

const VALUE = 'a cell value'
const STATUS = 'a chip label'
const ACTION = 'a button label'

const COLUMNS: readonly DataTableColumn<Row>[] = [
  { id: 'value', header: 'Value', cell: (row) => row.id },
  { id: 'status', header: 'Status', cell: () => <Chip size="small" label={STATUS} /> },
  { id: 'actions', header: 'Actions', cell: () => <Button size="small">{ACTION}</Button> },
]

/** The first family of a computed `font-family`, unquoted. */
function firstFamily(element: Element): string {
  const [first = ''] = getComputedStyle(element).fontFamily.split(',')
  return first.trim().replace(/^"|"$/g, '')
}

describe('the browser tier font pin in Chromium', () => {
  it('resolves the text of MUI components through the pinned family, not the theme stack', async () => {
    renderWithProviders(
      <DataTable rows={[{ id: VALUE }]} columns={COLUMNS} rowKey={(row) => row.id} />,
      { language: 'en-US' },
    )
    const cell = (await screen.findByText(VALUE)).closest(`.${tableCellClasses.root}`)
    const chip = screen.getByText(STATUS).closest(`.${chipClasses.root}`)
    const button = screen.getByRole('button', { name: ACTION })
    expect(cell).not.toBeNull()
    expect(chip).not.toBeNull()
    expect(button.classList.contains(buttonClasses.root)).toBe(true)

    const pinned = getComputedStyle(document.body).fontFamily
    expect(firstFamily(document.body)).toBe('Arial')
    for (const [name, element] of [
      ['table cell', cell],
      ['chip', chip],
      ['button', button],
    ] as const) {
      expect(
        getComputedStyle(element as Element).fontFamily,
        `the ${name} does not resolve through the pinned family`,
      ).toBe(pinned)
    }
  })
})
