/** Table-cell addressing for the page suites: a row's cell resolved by its translated column header. */

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
