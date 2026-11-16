/**
 * Table-cell addressing for the admin-ui page suites.
 *
 * A row is found by the value it renders, but the assertions about it
 * usually name a column instead of repeating a value. `cellOf` resolves
 * the cell by the table's own column headers, so a test reads a row's
 * column by its translated header the same way an operator would.
 */

import { within } from '@testing-library/react'

/** One cell of a row, addressed by its column's header. */
export function cellOf(row: HTMLElement, columnLabel: string): HTMLElement {
  const table = row.closest('table')
  if (table === null) {
    throw new Error('the row is not in a table')
  }
  const header = within(table).getByRole('columnheader', { name: columnLabel })
  const cell = row.children[(header as HTMLTableCellElement).cellIndex]
  if (!(cell instanceof HTMLElement)) {
    throw new Error(`no cell under ${columnLabel}`)
  }
  return cell
}
