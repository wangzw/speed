
import { act, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { flushSync } from 'react-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { checkboxClasses } from '@mui/material/Checkbox'
import { tableCellClasses } from '@mui/material/TableCell'
import { tableContainerClasses } from '@mui/material/TableContainer'
import { switchLanguage } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import {
  expectNoAxeViolations,
  runHeadingOrderCheck,
} from '../../test-utils/axe.js'
import {
  containerMaxWidthOf,
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import {
  DataTable,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  primaryColumnMaxWidthSx,
} from './DataTable.js'
import type {
  DataTableColumn,
  DataTableProps,
  DataTableSort,
} from './DataTable.js'

interface Member {
  id: number
  name: string
  credits: number
}

const MEMBERS: readonly Member[] = [
  { id: 1, name: 'Ada', credits: 120 },
  { id: 2, name: 'Grace', credits: 80 },
  { id: 3, name: 'Katherine', credits: 240 },
]

const BASE_COLUMNS: readonly DataTableColumn<Member>[] = [
  { id: 'name', header: 'Name', sortable: true, cell: (row) => row.name },
  {
    id: 'credits',
    header: 'Credits',
    sortable: true,
    align: 'right',
    width: 120,
    cell: (row) => String(row.credits),
  },
]

const keyOf = (row: Member): number => row.id

function renderTable(
  props: Partial<DataTableProps<Member>> = {},
  options: { heading?: string; language?: string } = {},
) {
  const table = (
    <DataTable
      rows={MEMBERS}
      columns={BASE_COLUMNS}
      rowKey={keyOf}
      {...props}
    />
  )
  return renderWithProviders(
    options.heading !== undefined ? (
      <div>
        <h1>{options.heading}</h1>
        {table}
      </div>
    ) : (
      table
    ),
    { language: options.language },
  )
}

function renderTemplate(
  template: string,
  vars: Record<string, string | number>,
): string {
  return Object.entries(vars).reduce(
    (out, [key, value]) => out.replaceAll(`{{${key}}}`, String(value)),
    template,
  )
}

describe('DataTable', () => {
  it('defines the shared caps: the primary pair grows at md, the long-text cap does not', () => {
    expect(PRIMARY_COLUMN_MAX_WIDTH).toEqual({ belowMd: 160, fromMd: 200 })
    expect(LONG_TEXT_COLUMN_MAX_WIDTH).toBe(160)
    expect(primaryColumnMaxWidthSx).toEqual({
      maxWidth: { '@': 160, '@md': 200 },
    })
  })

  it('renders every column header and cell', () => {
    const utils = renderTable()
    expect(
      utils.getByRole('columnheader', { name: 'Name' }),
    ).toBeInTheDocument()
    expect(
      utils.getByRole('columnheader', { name: 'Credits' }),
    ).toBeInTheDocument()
    expect(utils.getByText('Ada')).toBeInTheDocument()
    expect(utils.getByText('Grace')).toBeInTheDocument()
    expect(utils.getByText('Katherine')).toBeInTheDocument()
  })

  it('passes the row index into the cell render prop', () => {
    const seen: number[] = []
    const columns: readonly DataTableColumn<Member>[] = [
      {
        id: 'name',
        header: 'Name',
        cell: (_row, index) => {
          seen.push(index)
          return 'rendered'
        },
      },
    ]
    renderTable({ columns })
    expect(seen).toEqual([0, 1, 2])
    expect(seen.length).toBe(MEMBERS.length)
  })

  it('wraps the table in a horizontally scrollable container regardless of column count or width', () => {
    const manyWideColumns: DataTableColumn<Member>[] = Array.from(
      { length: 12 },
      (_, index) => ({
        id: `extra-${index}`,
        header: `Column ${index}`,
        width: 300,
        cell: () => 'value',
      }),
    )
    const utils = renderTable({ columns: manyWideColumns })
    const container = utils.container.querySelector(
      `.${tableContainerClasses.root}`,
    )
    expect(container).not.toBeNull()
    expect(container?.tagName.toLowerCase()).toBe('div')
    expect(container?.querySelector('table')).not.toBeNull()
    expect(container).toHaveStyle({ overflowX: 'auto' })
  })

  it('pins the last column only when the host opts in and the table overflows', () => {
    const withActions: readonly DataTableColumn<Member>[] = [
      ...BASE_COLUMNS,
      { id: 'actions', header: 'Actions', cell: () => 'open' },
    ]

    const off = renderTable({ columns: withActions })
    const offHeaders = off.container.querySelectorAll('thead th')
    expect(offHeaders[offHeaders.length - 1]).not.toHaveStyle({
      position: 'sticky',
    })
    off.unmount()

    const fitting = renderTable({ columns: withActions, pinLastColumn: true })
    const fittingHeaders = fitting.container.querySelectorAll('thead th')
    expect(fittingHeaders[fittingHeaders.length - 1]).not.toHaveStyle({
      position: 'sticky',
    })
    fitting.unmount()

    const scrollWidth = vi
      .spyOn(Element.prototype, 'scrollWidth', 'get')
      .mockReturnValue(600)
    const clientWidth = vi
      .spyOn(Element.prototype, 'clientWidth', 'get')
      .mockReturnValue(300)
    try {
      const on = renderTable({ columns: withActions, pinLastColumn: true })
      const headers = on.container.querySelectorAll('thead th')
      expect(headers[headers.length - 1]).toHaveStyle({
        position: 'sticky',
        right: '0px',
      })
      const bodyCells = on.container.querySelectorAll(
        'tbody tr:first-child td',
      )
      expect(bodyCells[bodyCells.length - 1]).toHaveStyle({
        position: 'sticky',
        right: '0px',
      })
    } finally {
      scrollWidth.mockRestore()
      clientWidth.mockRestore()
    }
  })

  it('applies column alignment and width to cells', () => {
    const utils = renderTable()
    const creditCells = utils.container.querySelectorAll(
      `tbody td.${tableCellClasses.alignRight}`,
    )
    expect(creditCells.length).toBe(MEMBERS.length)
    expect(creditCells[0]).toHaveStyle({ width: '120px' })
  })

  it('renders a status row with the translated loading text while rows are empty', async () => {
    const utils = renderTable({ rows: [], loading: true })
    const status = utils.getByRole('status')
    expect(status.textContent).toBe('')
    await waitFor(() =>
      expect(status.textContent).toContain(zhCN.dataTable.loading),
    )
    expect(utils.getByRole('progressbar')).toBeInTheDocument()
    expect(utils.queryByRole('row', { name: /Ada/ })).not.toBeInTheDocument()
  })

  it('keeps showing rows during a load when rows are present', () => {
    const utils = renderTable({ loading: true })
    expect(utils.getByText('Ada')).toBeInTheDocument()
    expect(utils.queryByRole('status')).not.toBeInTheDocument()
  })

  it('renders the stock EmptyState placeholder for an empty table', () => {
    const utils = renderTable({ rows: [] })
    expect(utils.getByText(zhCN.emptyState.empty.title)).toBeInTheDocument()
    expect(
      utils.getByText(zhCN.emptyState.empty.description),
    ).toBeInTheDocument()
  })

  it('announces the loading state on the empty-then-refresh path through a live region that already exists (P2-6 regression)', async () => {
    function RefreshHarness() {
      const [loading, setLoading] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setLoading(true)}>
            refresh
          </button>
          <DataTable
            rows={[]}
            columns={BASE_COLUMNS}
            rowKey={keyOf}
            loading={loading}
          />
        </>
      )
    }
    const utils = renderWithProviders(<RefreshHarness />)
    const region = utils.getByRole('status')
    await waitFor(() =>
      expect(region).toHaveTextContent(zhCN.dataTable.noData),
    )
    fireEvent.click(utils.getByRole('button', { name: 'refresh' }))
    expect(region.textContent).toBe('')
    await waitFor(() =>
      expect(region).toHaveTextContent(zhCN.dataTable.loading),
    )
    expect(utils.getByRole('progressbar')).toBeInTheDocument()
  })

  it('mounts the loading region empty when the empty phase begins already loading (D5 regression)', async () => {
    function EmptyingRefreshHarness() {
      const [rows, setRows] = useState<readonly Member[]>(MEMBERS)
      const [loading, setLoading] = useState(false)
      return (
        <>
          <button
            type="button"
            onClick={() => {
              setRows([])
              setLoading(true)
            }}
          >
            clear and reload
          </button>
          <DataTable
            rows={rows}
            columns={BASE_COLUMNS}
            rowKey={keyOf}
            loading={loading}
          />
        </>
      )
    }
    const utils = renderWithProviders(<EmptyingRefreshHarness />)
    expect(utils.getByText('Ada')).toBeInTheDocument()
    expect(utils.queryByRole('status')).not.toBeInTheDocument()
    act(() => {
      flushSync(() => {
        utils.getByRole('button', { name: 'clear and reload' }).click()
      })
      const region = utils.getByRole('status')
      expect(region.textContent).toBe('')
    })
    const region = utils.getByRole('status')
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.loading),
    )
    expect(utils.getByRole('progressbar')).toBeInTheDocument()
  })

  it('announces the empty result when a load ends with no rows: the region fills with the empty-result wording (P2-2 regression)', async () => {
    function LoadsEmptyHarness() {
      const [rows, setRows] = useState<readonly Member[]>(MEMBERS)
      const [loading, setLoading] = useState(false)
      return (
        <>
          <button
            type="button"
            onClick={() => {
              setRows([])
              setLoading(true)
            }}
          >
            clear and load
          </button>
          <button type="button" onClick={() => setLoading(false)}>
            finish empty
          </button>
          <DataTable
            rows={rows}
            columns={BASE_COLUMNS}
            rowKey={keyOf}
            loading={loading}
          />
        </>
      )
    }
    const utils = renderWithProviders(<LoadsEmptyHarness />)
    expect(utils.getByText('Ada')).toBeInTheDocument()
    act(() => {
      utils.getByRole('button', { name: 'clear and load' }).click()
    })
    const region = utils.getByRole('status')
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.loading),
    )
    act(() => {
      utils.getByRole('button', { name: 'finish empty' }).click()
    })
    expect(region.textContent).toBe('')
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.noData),
    )
    expect(
      utils.getByRole('heading', { name: zhCN.emptyState.empty.title }),
    ).toBeInTheDocument()
    expect(
      utils.getByText(zhCN.emptyState.empty.description),
    ).toBeInTheDocument()
  })

  it('keeps the announced empty wording independent of an overridden visible title (P2-2 regression)', async () => {
    function OverrideHarness() {
      const [loading, setLoading] = useState(true)
      return (
        <>
          <button type="button" onClick={() => setLoading(false)}>
            finish empty
          </button>
          <DataTable
            rows={[]}
            columns={BASE_COLUMNS}
            rowKey={keyOf}
            loading={loading}
            emptyTitle="No matches"
          />
        </>
      )
    }
    const utils = renderWithProviders(<OverrideHarness />)
    const region = utils.getByRole('status')
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.loading),
    )
    act(() => {
      utils.getByRole('button', { name: 'finish empty' }).click()
    })
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.noData),
    )
    expect(
      utils.getByRole('heading', { name: 'No matches' }),
    ).toBeInTheDocument()
    expect(
      utils.queryByText(zhCN.emptyState.empty.title),
    ).not.toBeInTheDocument()
  })

  it("fills the empty-result wording only after the region's first committed frame (P2-2 regression)", async () => {
    function ShowsEmptyHarness() {
      const [rows, setRows] = useState<readonly Member[]>(MEMBERS)
      return (
        <>
          <button type="button" onClick={() => setRows([])}>
            show empty
          </button>
          <DataTable rows={rows} columns={BASE_COLUMNS} rowKey={keyOf} />
        </>
      )
    }
    const utils = renderWithProviders(<ShowsEmptyHarness />)
    expect(utils.getByText('Ada')).toBeInTheDocument()
    act(() => {
      flushSync(() => {
        utils.getByRole('button', { name: 'show empty' }).click()
      })
      const region = utils.getByRole('status')
      expect(region.textContent).toBe('')
    })
    await waitFor(() =>
      expect(utils.getByRole('status').textContent).toContain(
        zhCN.dataTable.noData,
      ),
    )
  })

  it('overrides the empty placeholder slots', () => {
    const utils = renderTable({
      rows: [],
      emptyTitle: 'No matches',
      emptyDescription: 'Widen the filter and try again',
      emptyAction: <button type="button">Clear filters</button>,
    })
    expect(utils.getByText('No matches')).toBeInTheDocument()
    expect(
      utils.getByText('Widen the filter and try again'),
    ).toBeInTheDocument()
    expect(utils.getByRole('button', { name: 'Clear filters' })).toBeInTheDocument()
    expect(utils.queryByText(zhCN.emptyState.empty.title)).not.toBeInTheDocument()
  })

  it('labels the select-all and row checkboxes from the namespace', () => {
    const utils = renderTable({ onSelectionChange: vi.fn() })
    expect(
      utils.getByLabelText(zhCN.dataTable.selectAllRows),
    ).toBeInTheDocument()
    expect(
      utils.getByLabelText(
        renderTemplate(zhCN.dataTable.selectRow, { row: 1 }),
      ),
    ).toBeInTheDocument()
    expect(
      utils.getByLabelText(
        renderTemplate(zhCN.dataTable.selectRow, { row: 3 }),
      ),
    ).toBeInTheDocument()
  })

  it('switches the checkbox aria-labels into the new language', async () => {
    const utils = renderTable({ onSelectionChange: vi.fn() })
    expect(
      utils.getByLabelText(
        renderTemplate(zhCN.dataTable.selectRow, { row: 1 }),
      ),
    ).toBeInTheDocument()
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(
      utils.getByLabelText(
        renderTemplate(enUS.dataTable.selectRow, { row: 1 }),
      ),
    ).toBeInTheDocument()
    expect(
      utils.queryByLabelText(zhCN.dataTable.selectAllRows),
    ).not.toBeInTheDocument()
  })

  it('selects all rendered rows from the header and clears them again', () => {
    const onSelectionChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[]}
        onSelectionChange={onSelectionChange}
      />,
    )
    const selectAll = utils.getByLabelText(zhCN.dataTable.selectAllRows)
    fireEvent.click(selectAll)
    expect(onSelectionChange).toHaveBeenLastCalledWith([1, 2, 3])
    utils.rerender(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[1, 2, 3]}
        onSelectionChange={onSelectionChange}
      />,
    )
    fireEvent.click(utils.getByLabelText(zhCN.dataTable.selectAllRows))
    expect(onSelectionChange).toHaveBeenLastCalledWith([])
  })

  it('leaves out-of-page keys untouched when toggling all', () => {
    const onSelectionChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[9]}
        onSelectionChange={onSelectionChange}
      />,
    )
    fireEvent.click(utils.getByLabelText(zhCN.dataTable.selectAllRows))
    expect(onSelectionChange).toHaveBeenLastCalledWith([9, 1, 2, 3])
    utils.rerender(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[9, 1, 2, 3]}
        onSelectionChange={onSelectionChange}
      />,
    )
    fireEvent.click(utils.getByLabelText(zhCN.dataTable.selectAllRows))
    expect(onSelectionChange).toHaveBeenLastCalledWith([9])
  })

  it('toggles one row on and off', () => {
    const onSelectionChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[]}
        onSelectionChange={onSelectionChange}
      />,
    )
    fireEvent.click(
      utils.getByLabelText(renderTemplate(zhCN.dataTable.selectRow, { row: 1 })),
    )
    expect(onSelectionChange).toHaveBeenLastCalledWith([1])
    utils.rerender(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[1]}
        onSelectionChange={onSelectionChange}
      />,
    )
    fireEvent.click(
      utils.getByLabelText(renderTemplate(zhCN.dataTable.selectRow, { row: 1 })),
    )
    expect(onSelectionChange).toHaveBeenLastCalledWith([])
  })

  it('keeps the header checkbox focusable but unavailable when the page is empty', () => {
    const onSelectionChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={[]}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[]}
        onSelectionChange={onSelectionChange}
      />,
    )
    const selectAll = utils.getByLabelText(zhCN.dataTable.selectAllRows)
    expect(selectAll).toHaveAttribute('aria-disabled', 'true')
    expect(selectAll).not.toBeDisabled()
    selectAll.focus()
    expect(selectAll).toBe(document.activeElement)
    fireEvent.click(selectAll)
    expect(onSelectionChange).not.toHaveBeenCalled()
  })

  it('marks the header checkbox indeterminate for a partial page selection', () => {
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        selectedRowKeys={[1]}
        onSelectionChange={vi.fn()}
      />,
    )
    const header = utils.getByLabelText(
      zhCN.dataTable.selectAllRows,
    ) as HTMLInputElement
    expect(header.indeterminate).toBe(true)
    expect(header.checked).toBe(false)
  })

  it('renders sortable headers as buttons only when sorting is enabled', () => {
    const utils = renderTable()
    expect(
      utils.queryByRole('button', { name: 'Name' }),
    ).not.toBeInTheDocument()
    expect(
      utils.getByRole('columnheader', { name: 'Name' }),
    ).not.toHaveAttribute('aria-sort')
  })

  it('stays inert when only one half of the sort props is given', () => {
    const onSortChange = vi.fn()
    const utils = renderTable({ onSortChange })
    fireEvent.click(utils.getByText('Name'))
    expect(onSortChange).not.toHaveBeenCalled()
  })

  it('fires the flipped direction when the active column header is clicked', () => {
    const onSortChange = vi.fn()
    const utils = renderTable({
      sort: { columnId: 'name', direction: 'asc' },
      onSortChange,
    })
    expect(
      utils.getByRole('columnheader', { name: 'Name' }),
    ).toHaveAttribute('aria-sort', 'ascending')
    fireEvent.click(utils.getByRole('button', { name: 'Name' }))
    expect(onSortChange).toHaveBeenLastCalledWith({
      columnId: 'name',
      direction: 'desc',
    })
  })

  it('fires ascending when a new sortable column is clicked', () => {
    const onSortChange = vi.fn()
    const utils = renderTable({
      sort: { columnId: 'name', direction: 'desc' },
      onSortChange,
    })
    fireEvent.click(utils.getByRole('button', { name: 'Credits' }))
    expect(onSortChange).toHaveBeenLastCalledWith({
      columnId: 'credits',
      direction: 'asc',
    })
    expect(
      utils.getByRole('columnheader', { name: 'Credits' }),
    ).not.toHaveAttribute('aria-sort', 'ascending')
  })

  it('supports an explicit null sort with sorting on', () => {
    const onSortChange = vi.fn()
    const utils = renderTable({ sort: null, onSortChange })
    expect(
      utils.getByRole('columnheader', { name: 'Name' }),
    ).not.toHaveAttribute('aria-sort')
    fireEvent.click(utils.getByRole('button', { name: 'Name' }))
    expect(onSortChange).toHaveBeenLastCalledWith({
      columnId: 'name',
      direction: 'asc',
    })
  })

  it('renders the pagination footer labels with interpolated counts', () => {
    const utils = renderTable({
      pagination: {
        page: 0,
        rowsPerPage: 2,
        count: 5,
        rowsPerPageOptions: [2, 5],
        onPageChange: vi.fn(),
        onRowsPerPageChange: vi.fn(),
      },
    })
    expect(utils.getByText(zhCN.dataTable.rowsPerPage)).toBeInTheDocument()
    expect(
      utils.getByText(
        renderTemplate(zhCN.dataTable.displayedRows, {
          from: 1,
          to: 2,
          count: 5,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('uses the unknown-total wording when count is -1', () => {
    const utils = renderTable({
      pagination: {
        page: 0,
        rowsPerPage: 2,
        count: -1,
        rowsPerPageOptions: [2, 5],
        onPageChange: vi.fn(),
        onRowsPerPageChange: vi.fn(),
      },
    })
    expect(
      utils.getByText(
        renderTemplate(zhCN.dataTable.displayedRowsUnknown, {
          from: 1,
          to: 2,
        }),
      ),
    ).toBeInTheDocument()
    expect(
      utils.queryByText(zhCN.dataTable.displayedRows.replace('{{count}}', '5')),
    ).not.toBeInTheDocument()
  })

  it('fires the next-page callback and marks previous unavailable on the first page', () => {
    const onPageChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        pagination={{
          page: 0,
          rowsPerPage: 2,
          count: 5,
          rowsPerPageOptions: [2, 5],
          onPageChange,
          onRowsPerPageChange: vi.fn(),
        }}
      />,
      { language: 'en-US' },
    )
    const previous = utils.getByRole('button', { name: 'Go to previous page' })
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toBeDisabled()
    fireEvent.click(previous)
    expect(onPageChange).not.toHaveBeenCalled()
    fireEvent.click(utils.getByRole('button', { name: 'Go to next page' }))
    expect(onPageChange).toHaveBeenLastCalledWith(1)
  })

  it('marks both directions unavailable while a write is in flight, and swallows activation', () => {
    const onPageChange = vi.fn()
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        pagination={{
          page: 1,
          rowsPerPage: 2,
          count: 5,
          rowsPerPageOptions: [2, 5],
          onPageChange,
          onRowsPerPageChange: vi.fn(),
          unavailable: true,
        }}
      />,
      { language: 'en-US' },
    )
    const previous = utils.getByRole('button', { name: 'Go to previous page' })
    const next = utils.getByRole('button', { name: 'Go to next page' })
    for (const direction of [previous, next]) {
      expect(direction).toHaveAttribute('aria-disabled', 'true')
      expect(direction).not.toBeDisabled()
    }

    fireEvent.click(previous)
    fireEvent.click(next)
    expect(onPageChange).not.toHaveBeenCalled()
  })

  it('keeps the footer wording and the direction states when the new fields are omitted', () => {
    const utils = renderWithProviders(
      <DataTable
        rows={MEMBERS}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        pagination={{
          page: 1,
          rowsPerPage: 2,
          count: 5,
          rowsPerPageOptions: [2, 5],
          onPageChange: vi.fn(),
          onRowsPerPageChange: vi.fn(),
        }}
      />,
      { language: 'en-US' },
    )

    expect(utils.getByText(enUS.dataTable.rowsPerPage)).toBeInTheDocument()
    expect(
      utils.getByText(
        renderTemplate(enUS.dataTable.displayedRows, {
          from: 3,
          to: 4,
          count: 5,
        }),
      ),
    ).toBeInTheDocument()
    for (const name of ['Go to previous page', 'Go to next page']) {
      const direction = utils.getByRole('button', { name })
      expect(direction).not.toHaveAttribute('aria-disabled')
      expect(direction).not.toHaveAttribute('aria-busy')
      expect(direction).not.toBeDisabled()
    }
  })

  it('fires the rows-per-page callback from the selector', () => {
    const onRowsPerPageChange = vi.fn()
    const utils = renderTable({
      pagination: {
        page: 0,
        rowsPerPage: 2,
        count: 5,
        rowsPerPageOptions: [2, 5],
        onPageChange: vi.fn(),
        onRowsPerPageChange,
      },
    })
    fireEvent.mouseDown(utils.getByRole('combobox'))
    fireEvent.click(utils.getByRole('option', { name: '5' }))
    expect(onRowsPerPageChange).toHaveBeenLastCalledWith(5)
  })

  describe("pager scope (the table's own filter and sort)", () => {
    function ScopedPagerHarness() {
      const [state, setState] = useState<{
        page: number
        reading: boolean
        filter: string
        sort: DataTableSort | null
      }>({ page: 1, reading: false, filter: '', sort: null })
      return (
        <DataTable
          rows={MEMBERS}
          columns={BASE_COLUMNS}
          rowKey={keyOf}
          filter={{
            value: state.filter,
            onValueChange: (value) =>
              setState((previous) => ({
                ...previous,
                filter: value,
                page: 0,
                reading: true,
              })),
          }}
          sort={state.sort}
          onSortChange={(sort) =>
            setState((previous) => ({
              ...previous,
              sort,
              page: 0,
              reading: true,
            }))
          }
          pagination={{
            page: state.page,
            rowsPerPage: 2,
            count: 6,
            rowsPerPageOptions: [2, 6],
            reading: state.reading,
            onPageChange: (page) =>
              setState((previous) => ({ ...previous, page, reading: true })),
            onRowsPerPageChange: () => undefined,
          }}
        />
      )
    }

    it('drops the page record when the built-in filter changes, and keeps the input focused', async () => {
      const utils = renderWithProviders(<ScopedPagerHarness />, {
        language: 'en-US',
      })
      const user = userEvent.setup()
      const input = utils.getByLabelText(enUS.dataTable.filterLabel)

      await user.click(
        utils.getByRole('button', { name: 'Go to previous page' }),
      )
      const started = utils.getByRole('button', {
        name: 'Go to previous page',
      })
      expect(started).toHaveAttribute('aria-busy', 'true')
      expect(started).toHaveFocus()

      await user.type(input, 'x')

      for (const name of ['Go to previous page', 'Go to next page']) {
        const direction = utils.getByRole('button', { name })
        expect(direction).toBeInTheDocument()
        expect(direction).toHaveAttribute('aria-disabled', 'true')
        expect(direction).not.toHaveAttribute('aria-busy')
      }
      expect(input).toHaveFocus()
      expect(input).toHaveValue('x')
    })

    it('drops the page record when the built-in sort changes, and keeps the header focused', async () => {
      const utils = renderWithProviders(<ScopedPagerHarness />, {
        language: 'en-US',
      })
      const user = userEvent.setup()

      await user.click(
        utils.getByRole('button', { name: 'Go to previous page' }),
      )
      const started = utils.getByRole('button', {
        name: 'Go to previous page',
      })
      expect(started).toHaveAttribute('aria-busy', 'true')
      expect(started).toHaveFocus()

      const header = utils.getByRole('button', { name: 'Name' })
      await user.click(header)

      for (const name of ['Go to previous page', 'Go to next page']) {
        const direction = utils.getByRole('button', { name })
        expect(direction).toBeInTheDocument()
        expect(direction).toHaveAttribute('aria-disabled', 'true')
        expect(direction).not.toHaveAttribute('aria-busy')
      }
      expect(header).toHaveFocus()
      expect(utils.getByRole('columnheader', { name: 'Name' })).toHaveAttribute(
        'aria-sort',
        'ascending',
      )
    })
  })

  it('re-renders loading, footer and checkbox text in the new language', async () => {
    const utils = renderWithProviders(
      <DataTable
        rows={[]}
        columns={BASE_COLUMNS}
        rowKey={keyOf}
        loading
        onSelectionChange={vi.fn()}
        pagination={{
          page: 0,
          rowsPerPage: 2,
          count: 5,
          rowsPerPageOptions: [2, 5],
          onPageChange: vi.fn(),
          onRowsPerPageChange: vi.fn(),
        }}
      />,
    )
    await waitFor(() =>
      expect(utils.getByRole('status').textContent).toContain(
        zhCN.dataTable.loading,
      ),
    )
    expect(utils.getByText(zhCN.dataTable.rowsPerPage)).toBeInTheDocument()
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    await waitFor(() =>
      expect(utils.getByRole('status').textContent).toContain(
        enUS.dataTable.loading,
      ),
    )
    expect(utils.getByText(enUS.dataTable.rowsPerPage)).toBeInTheDocument()
    expect(
      utils.getByText(
        renderTemplate(enUS.dataTable.displayedRows, {
          from: 1,
          to: 2,
          count: 5,
        }),
      ),
    ).toBeInTheDocument()
    expect(utils.queryByText(zhCN.dataTable.rowsPerPage)).not.toBeInTheDocument()
  })

  it('renders the controlled filter input and fires value changes', async () => {
    const recorded: string[] = []
    function FilterHarness() {
      const [value, setValue] = useState('gr')
      return (
        <DataTable
          rows={MEMBERS}
          columns={BASE_COLUMNS}
          rowKey={keyOf}
          filter={{
            value,
            onValueChange: (next) => {
              recorded.push(next)
              setValue(next)
            },
          }}
        />
      )
    }
    const utils = renderWithProviders(<FilterHarness />)
    const input = utils.getByLabelText(zhCN.dataTable.filterLabel)
    expect(input).toHaveValue('gr')
    expect(input).toHaveAttribute('placeholder', zhCN.dataTable.filterLabel)
    const user = userEvent.setup()
    await user.type(input, 'ace')
    expect(recorded.at(-1)).toBe('grace')
    expect(input).toHaveValue('grace')
  })

  it('relabels the filter input on language switch', async () => {
    const utils = renderTable({
      filter: { value: '', onValueChange: vi.fn() },
    })
    expect(
      utils.getByLabelText(zhCN.dataTable.filterLabel),
    ).toBeInTheDocument()
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(
      utils.getByLabelText(enUS.dataTable.filterLabel),
    ).toBeInTheDocument()
  })

  it('takes the filter visible label and placeholder over the namespace defaults', () => {
    const utils = renderTable({
      filter: {
        value: '',
        onValueChange: vi.fn(),
        label: 'Filter members',
        placeholder: 'Name or credits',
      },
    })
    const input = utils.getByLabelText('Filter members')
    expect(input).toHaveAttribute('placeholder', 'Name or credits')
    expect(input).not.toHaveAttribute('aria-label')
    const label = utils.getByText('Filter members', { selector: 'label' })
    expect(label.tagName).toBe('LABEL')
    expect(label).toHaveAttribute('for', input.id)
    expect(
      utils.queryByLabelText(zhCN.dataTable.filterLabel),
    ).not.toBeInTheDocument()
  })

  it('uses ariaLabel for the table name and the namespace default otherwise', () => {
    const named = renderTable({ ariaLabel: 'Member roster' })
    expect(
      named.getByRole('table', { name: 'Member roster' }),
    ).toBeInTheDocument()
    named.unmount()

    const defaulted = renderTable()
    expect(
      defaulted.getByRole('table', { name: zhCN.dataTable.ariaLabel }),
    ).toBeInTheDocument()
  })

  it('takes its name from ariaLabelledBy in place of the inline label', () => {
    const utils = renderWithProviders(
      <div>
        <h2 id="members-heading">Members</h2>
        <DataTable
          rows={MEMBERS}
          columns={BASE_COLUMNS}
          rowKey={keyOf}
          ariaLabel="Ignored inline name"
          ariaLabelledBy="members-heading"
        />
      </div>,
    )
    const table = utils.getByRole('table', { name: 'Members' })
    expect(table).toHaveAttribute('aria-labelledby', 'members-heading')
    expect(table).not.toHaveAttribute('aria-label')
  })

  it('uses the rowKey results as the selection keys', () => {
    const onSelectionChange = vi.fn()
    const utils = renderTable({
      rowKey: (row) => `m${row.id}`,
      onSelectionChange,
    })
    fireEvent.click(utils.getByLabelText(zhCN.dataTable.selectAllRows))
    expect(onSelectionChange).toHaveBeenLastCalledWith(['m1', 'm2', 'm3'])
  })

  it('applies the small table size class on request', () => {
    const utils = renderTable({ size: 'small' })
    const cell = utils.container.querySelector('tbody td')
    expect(cell?.className).toContain(tableCellClasses.sizeSmall)
  })

  describe('component container (the context every rule below resolves against)', () => {
    it('declares itself a container, so the rules inside it can resolve -- the parent declaration without which no container query fires', () => {
      renderTable()
      expect(emittedStyleText()).toMatch(/container-type:inline-size/)
    })
  })

  describe('cell wrap policy (per-column opt-in)', () => {
    const WRAP_COLUMNS: readonly DataTableColumn<Member>[] = [
      {
        id: 'name',
        header: 'Name',
        cellWrap: 'nowrap',
        cell: (row) => row.name,
      },
      {
        id: 'bio',
        header: 'Bio',
        cell: (row) => `Member ${row.id} has a longer description`,
      },
    ]

    it('keeps an opted-in column on one line in the header and every body cell', () => {
      const utils = renderTable({ columns: WRAP_COLUMNS })
      expect(
        utils.getByRole('columnheader', { name: 'Name' }),
      ).toHaveStyle({ whiteSpace: 'nowrap' })
      const nameCells = utils.container.querySelectorAll(
        'tbody tr td:first-child',
      )
      expect(nameCells.length).toBe(MEMBERS.length)
      for (const cell of nameCells) {
        expect(cell).toHaveStyle({ whiteSpace: 'nowrap' })
      }
    })

    it('leaves a column that does not opt in in normal text flow, so a long value can still wrap', () => {
      const utils = renderTable({ columns: WRAP_COLUMNS })
      expect(
        utils.getByRole('columnheader', { name: 'Bio' }),
      ).not.toHaveStyle({ whiteSpace: 'nowrap' })
      const bioCells = utils.container.querySelectorAll(
        'tbody tr td:nth-child(2)',
      )
      expect(bioCells.length).toBe(MEMBERS.length)
      for (const cell of bioCells) {
        expect(cell).not.toHaveStyle({ whiteSpace: 'nowrap' })
      }
    })

    it("treats an explicit 'wrap' policy as the default, emitting no declaration", () => {
      const columns: DataTableColumn<Member>[] = [
        { id: 'name', header: 'Name', cellWrap: 'wrap', cell: (row) => row.name },
        ...BASE_COLUMNS.slice(1),
      ]
      const utils = renderTable({ columns })
      expect(
        utils.getByRole('columnheader', { name: 'Name' }),
      ).not.toHaveStyle({ whiteSpace: 'nowrap' })
      expect(
        utils.container.querySelector('tbody tr td:first-child'),
      ).not.toHaveStyle({ whiteSpace: 'nowrap' })
    })

    it('keeps the horizontal scroll container that a nowrap column overflows into', () => {
      const utils = renderTable({ columns: WRAP_COLUMNS })
      const container = utils.container.querySelector(
      `.${tableContainerClasses.root}`,
    )
      expect(container).toHaveStyle({ overflowX: 'auto' })
      expect(
        utils.getByRole('columnheader', { name: 'Name' }),
      ).toHaveStyle({ whiteSpace: 'nowrap' })
    })
  })

  describe('cell full-value affordance (per-column opt-in)', () => {
    const FULL_VALUE_COLUMNS: readonly DataTableColumn<Member>[] = [
      {
        id: 'name',
        header: 'Name',
        cellWrap: 'nowrap',
        cellFullValue: (row) => `member-${row.id}`,
        cell: (row) => row.name,
      },
      {
        id: 'credits',
        header: 'Credits',
        align: 'right',
        width: 120,
        cell: (row) => String(row.credits),
      },
    ]

    function stubClipboard() {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      return writeText
    }

    afterEach(() => {
      Reflect.deleteProperty(navigator, 'clipboard')
    })

    it("renders the complete value as the opted-in cell's title", () => {
      const utils = renderTable({ columns: FULL_VALUE_COLUMNS })
      const nameCell = utils.getByText('Ada').closest('td')
      expect(nameCell).toHaveAttribute('title', 'member-1')
    })

    it('leaves a column without the accessor exactly as its renderer returned it', () => {
      const utils = renderTable({ columns: FULL_VALUE_COLUMNS })
      const creditCell = utils.getByText('120').closest('td')
      expect(creditCell).not.toHaveAttribute('title')
      expect(within(creditCell as HTMLElement).queryByRole('button')).toBeNull()
    })

    it('copies the complete value through a focusable control and announces the copy', async () => {
      const user = userEvent.setup()
      const writeText = stubClipboard()
      const utils = renderTable({ columns: FULL_VALUE_COLUMNS })

      const button = utils.getByRole('button', {
        name: renderTemplate(zhCN.dataTable.copyValue, { value: 'member-1' }),
      })
      await user.click(button)

      expect(button).toHaveFocus()
      await waitForCondition(() => {
        expect(writeText).toHaveBeenCalledWith('member-1')
      })
      const announcement = await utils.findByText(
        renderTemplate(zhCN.dataTable.copiedValue, { value: 'member-1' }),
      )
      expect(announcement).toHaveAttribute('role', 'status')
    })

    it("speaks the control's label and announcement in the active language", async () => {
      const user = userEvent.setup()
      stubClipboard()
      const utils = renderTable(
        { columns: FULL_VALUE_COLUMNS },
        { language: 'en-US' },
      )

      const button = utils.getByRole('button', {
        name: renderTemplate(enUS.dataTable.copyValue, { value: 'member-1' }),
      })
      await user.click(button)
      await utils.findByText(
        renderTemplate(enUS.dataTable.copiedValue, { value: 'member-1' }),
      )
    })
  })

  describe('whole-cell content cap (per-column opt-in)', () => {
    const CAPPED_COLUMNS: readonly DataTableColumn<Member>[] = [
      {
        id: 'name',
        header: 'Name',
        cellWrap: 'nowrap',
        cellFullValue: (row) => `member-${row.id}`,
        contentMaxWidth: { belowMd: 120, fromMd: 180 },
        cell: (row) => row.name,
      },
      {
        id: 'plain',
        header: 'Plain',
        cellWrap: 'nowrap',
        cellFullValue: (row) => `plain-${row.id}`,
        cell: (row) => row.name,
      },
    ]

    it('converts the named pair into the below-md and from-md rules', () => {
      const utils = renderTable({ columns: CAPPED_COLUMNS })
      const [cappedCell] = utils
        .getAllByText('Ada')
        .map((element) => element.closest('td'))
        .filter((cell): cell is HTMLTableCellElement => cell !== null)
      const wrapper = cappedCell?.firstElementChild
      expect(wrapper).not.toBeNull()
      expect(containerMaxWidthOf(wrapper as Element)).toEqual(
        new Map([
          [0, 120],
          [900, 180],
        ]),
      )
      expect(emittedStyleText()).toContain('@container (min-width:900px)')
    })

    it('applies a plain-number cap at every width', () => {
      const columns: readonly DataTableColumn<Member>[] = [
        {
          id: 'name',
          header: 'Name',
          cellWrap: 'nowrap',
          cellFullValue: (row) => `member-${row.id}`,
          contentMaxWidth: 150,
          cell: (row) => row.name,
        },
      ]
      const utils = renderTable({ columns })
      const cell = utils.getByText('Ada').closest('td') as HTMLElement
      const wrapper = cell.firstElementChild as HTMLElement
      expect(wrapper).toHaveStyle({ maxWidth: '150px' })
      expect(containerMaxWidthOf(wrapper)).toEqual(new Map())
    })

    it('truncates inside the cap and leaves the copy control whole beside it', () => {
      const utils = renderTable({ columns: CAPPED_COLUMNS })
      const cell = utils
        .getAllByText('Ada')
        .map((element) => element.closest('td') as HTMLElement)[0]
      const wrapper = cell!.firstElementChild as HTMLElement
      const button = within(wrapper).getByRole('button', {
        name: renderTemplate(zhCN.dataTable.copyValue, { value: 'member-1' }),
      })
      const textBox = button.previousElementSibling as HTMLElement
      expect(textBox).not.toBeNull()
      expect(textBox).toHaveStyle({
        minWidth: '0px',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      })
      expect(button.parentElement).toBe(wrapper)
      expect(button).toHaveStyle({ flexShrink: '0' })
    })

    it('wraps a capped wrap column so its longest word, not the cap, sets its minimum width', () => {
      const columns: readonly DataTableColumn<Member>[] = [
        {
          id: 'name',
          header: 'Name',
          cellWrap: 'wrap',
          cellFullValue: (row) => `member-${row.id}`,
          contentMaxWidth: { belowMd: 120, fromMd: 180 },
          cell: (row) => row.name,
        },
      ]
      const utils = renderTable({ columns })
      const cell = utils.getByText('Ada').closest('td') as HTMLElement
      const wrapper = cell.firstElementChild as HTMLElement
      const button = within(wrapper).getByRole('button', {
        name: renderTemplate(zhCN.dataTable.copyValue, { value: 'member-1' }),
      })
      const textBox = button.previousElementSibling as HTMLElement
      expect(textBox).toHaveStyle({
        minWidth: '0px',
        overflow: 'hidden',
        overflowWrap: 'anywhere',
        whiteSpace: 'normal',
      })
      expect(textBox).not.toHaveStyle({ textOverflow: 'ellipsis' })
      expect(containerMaxWidthOf(wrapper)).toEqual(
        new Map([
          [0, 120],
          [900, 180],
        ]),
      )
    })

    it('leaves a column without the field byte-for-byte free of the cap', () => {
      const utils = renderTable({ columns: CAPPED_COLUMNS })
      const plainCell = utils
        .getAllByText('Ada')
        .map((element) => element.closest('td'))
        .filter((cell): cell is HTMLTableCellElement => cell !== null)[1]
      const wrapper = plainCell?.firstElementChild
      expect(wrapper).not.toBeNull()
      expect(containerMaxWidthOf(wrapper as Element)).toEqual(new Map())
      expect(wrapper).toHaveTextContent('Ada')
      const button = within(wrapper as HTMLElement).getByRole('button', {
        name: renderTemplate(zhCN.dataTable.copyValue, { value: 'plain-1' }),
      })
      expect(button.previousElementSibling).toBeNull()
    })

    it('caps a row without a full value without inventing a copy control', () => {
      const columns: readonly DataTableColumn<Member>[] = [
        {
          id: 'name',
          header: 'Name',
          cellWrap: 'nowrap',
          cellFullValue: () => undefined,
          contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
          cell: (row) => row.name,
        },
      ]
      const utils = renderTable({ columns })
      const cell = utils.getByText('Ada').closest('td') as HTMLElement
      expect(cell).not.toHaveAttribute('title')
      expect(within(cell).queryByRole('button')).toBeNull()
      const wrapper = cell.firstElementChild
      expect(wrapper).not.toBeNull()
      expect(containerMaxWidthOf(wrapper as Element)).toEqual(
        new Map([
          [0, 160],
          [900, 200],
        ]),
      )
    })
  })

  describe('row vertical alignment (opt-in)', () => {
    const fullValueColumns: readonly DataTableColumn<Member>[] = [
      {
        id: 'name',
        header: 'Name',
        cellFullValue: (row) => `plain-${row.id}`,
        cell: (row) => row.name,
      },
    ]

    const copyWrapperOf = (
      utils: ReturnType<typeof renderTable>,
    ): HTMLElement => {
      const button = utils.getByRole('button', {
        name: renderTemplate(zhCN.dataTable.copyValue, { value: 'plain-1' }),
      })
      return button.parentElement as HTMLElement
    }

    it('keeps MUI centring when the prop is omitted', () => {
      const utils = renderTable({ columns: fullValueColumns })
      expect(copyWrapperOf(utils)).toHaveStyle({ alignItems: 'center' })
      const row = utils.getByText('Ada').closest('tr') as HTMLElement
      expect(row).not.toHaveStyle({ verticalAlign: 'top' })
    })

    it("top-aligns every body row and its copy control with 'top'", () => {
      const utils = renderTable({
        columns: fullValueColumns,
        rowVerticalAlign: 'top',
      })
      const row = utils.getByText('Ada').closest('tr') as HTMLElement
      expect(row).toHaveStyle({ verticalAlign: 'top' })
      expect(copyWrapperOf(utils)).toHaveStyle({ alignItems: 'flex-start' })
    })
  })

  describe('column priority (container-triggered reflow)', () => {
    it('renders a column with no priority with no visibility override -- unaffected by the feature', () => {
      const utils = renderTable()
      const creditCells = utils.container.querySelectorAll(
        `tbody td.${tableCellClasses.alignRight}`,
      )
      expect(creditCells[0]).toHaveStyle({ width: '120px' })
      expect(utils.getByText('Ada')).toBeInTheDocument()
    })

    it('hides a high-priority column at the container narrow end and restores it from sm up, keyed on the container', () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'high', cell: () => 'Pro' },
      ]
      renderTable({ columns })
      const css = emittedStyleText()
      expect(css).toMatch(/@container \(min-width:0px\)\{[^}]*display:none/)
      expect(css).toMatch(/@container \(min-width:600px\)\{[^}]*display:table-cell/)
    })

    it('hides a medium-priority column at the container md', () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'medium', cell: () => 'Pro' },
      ]
      renderTable({ columns })
      const css = emittedStyleText()
      expect(css).toMatch(/@container \(min-width:900px\)\{[^}]*display:table-cell/)
    })

    it('hides a low-priority column at the container lg', () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'low', cell: () => 'Pro' },
      ]
      renderTable({ columns })
      const css = emittedStyleText()
      expect(css).toMatch(/@container \(min-width:1200px\)\{[^}]*display:table-cell/)
    })

    it('keys the column display on the container, never a viewport media query', () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'low', cell: () => 'Pro' },
      ]
      renderTable({ columns })
      const css = emittedStyleText()
      expect(css).toContain('@container (min-width:1200px)')
      expect(css).not.toContain('@media (min-width:1200px)')
    })

    it('keeps a prioritized column as a real DOM column (CSS-hidden, never conditionally removed)', () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'low', cell: () => 'Pro' },
      ]
      const utils = renderTable({ columns })
      expect(
        utils.getByRole('columnheader', { name: 'Plan' }),
      ).toBeInTheDocument()
      expect(utils.getAllByText('Pro')).toHaveLength(MEMBERS.length)
      expect(utils.getByText('Ada')).toBeInTheDocument()
    })

    it('passes axe with a mix of prioritized and always-visible columns', async () => {
      const columns: DataTableColumn<Member>[] = [
        ...BASE_COLUMNS,
        { id: 'plan', header: 'Plan', priority: 'low', cell: () => 'Pro' },
      ]
      renderTable({ columns }, { heading: 'Members' })
      await expectNoAxeViolations()
    })
  })

  describe('toolbar degradation (container-triggered)', () => {
    it('gives the filter input a full-width row below the container sm and 20rem from it up', () => {
      renderTable({ filter: { value: '', onValueChange: vi.fn() } })
      const css = emittedStyleText()
      expect(css).toMatch(/@container \(min-width:0px\)\{[^}]*width:100%/)
      expect(css).toMatch(/@container \(min-width:600px\)\{[^}]*width:20rem/)
    })
  })

  describe('pointer capability', () => {
    it('raises the selection controls to the 44x44 touch-target floor under a coarse pointer', () => {
      const utils = renderTable({
        selectedRowKeys: [],
        onSelectionChange: vi.fn(),
      })
      expect(
        hasCoarsePointerTouchTarget(
          utils
            .getByRole('checkbox', { name: zhCN.dataTable.selectAllRows })
            .closest(`.${checkboxClasses.root}`) as Element,
        ),
      ).toBe(true)
      expect(
        hasCoarsePointerTouchTarget(
          utils
            .getByRole('checkbox', {
              name: renderTemplate(zhCN.dataTable.selectRow, { row: 1 }),
            })
            .closest(`.${checkboxClasses.root}`) as Element,
        ),
      ).toBe(true)
    })

    it('raises the pagination buttons and the rows-per-page selector to the floor', () => {
      const utils = renderWithProviders(
        <DataTable
          rows={MEMBERS}
          columns={BASE_COLUMNS}
          rowKey={keyOf}
          pagination={{
            page: 0,
            rowsPerPage: 2,
            count: 5,
            rowsPerPageOptions: [2, 5],
            onPageChange: vi.fn(),
            onRowsPerPageChange: vi.fn(),
          }}
        />,
        { language: 'en-US' },
      )
      expect(
        hasCoarsePointerTouchTarget(
          utils.getByRole('button', { name: 'Go to previous page' }),
        ),
      ).toBe(true)
      expect(
        hasCoarsePointerTouchTarget(
          utils.getByRole('button', { name: 'Go to next page' }),
        ),
      ).toBe(true)
      expect(
        hasCoarsePointerTouchTarget(
          utils.getByRole('combobox', { name: enUS.dataTable.rowsPerPage }),
        ),
      ).toBe(true)
    })

    it('enables the row hover highlight only under a fine, hovering pointer', () => {
      renderTable()
      const css = emittedStyleText()
      expect(css).toContain('@media (hover: hover) and (pointer: fine)')
      expect(css).toMatch(
        /@media \(hover: hover\) and \(pointer: fine\)\{[^}]*:hover/,
      )
    })
  })

  it('passes axe over a fully loaded table', async () => {
    renderTable(
      {
        selectedRowKeys: [1],
        onSelectionChange: vi.fn(),
        sort: { columnId: 'name', direction: 'asc' },
        onSortChange: vi.fn(),
        filter: { value: '', onValueChange: vi.fn() },
        pagination: {
          page: 0,
          rowsPerPage: 2,
          count: 3,
          rowsPerPageOptions: [2, 5],
          onPageChange: vi.fn(),
          onRowsPerPageChange: vi.fn(),
        },
      },
      { heading: 'Members' },
    )
    await expectNoAxeViolations()
  })

  it('passes axe over the loading state', async () => {
    renderTable({ rows: [], loading: true }, { heading: 'Members' })
    await expectNoAxeViolations()
  })

  it('passes axe over the empty placeholder state', async () => {
    renderTable(
      { rows: [], emptyHeadingLevel: 'h2' },
      { heading: 'Members' },
    )
    await expectNoAxeViolations()
  })

  describe('the empty placeholder under a real page heading (P2-3)', () => {

    it('still skips to the stock h6 default under a real h1 when no emptyHeadingLevel is given', async () => {
      renderWithProviders(
        <div>
          <h1>Notes</h1>
          <DataTable rows={[]} columns={BASE_COLUMNS} rowKey={keyOf} />
        </div>,
      )
      expect(document.querySelector('h6')).not.toBeNull()
      const violations = await runHeadingOrderCheck()
      expect(violations.length).toBeGreaterThan(0)
    })

    it('continues the page heading order when emptyHeadingLevel is supplied', async () => {
      renderWithProviders(
        <div>
          <h1>Notes</h1>
          <DataTable
            rows={[]}
            columns={BASE_COLUMNS}
            rowKey={keyOf}
            emptyHeadingLevel="h2"
          />
        </div>,
      )
      expect(document.querySelector('h6')).toBeNull()
      expect(document.querySelector('h2')).not.toBeNull()
      const violations = await runHeadingOrderCheck()
      expect(violations).toHaveLength(0)
    })
  })
})
