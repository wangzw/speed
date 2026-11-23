/**
 * DataTable contract: a fully controlled table view -- rows are shown
 * exactly as given (no implicit sorting or slicing), sorting shows
 * state and fires change callbacks only, selection toggles the rendered
 * page's keys and leaves other pages' keys alone, the footer labels
 * interpolate the ui-kit namespace text and follow the language,
 * loading appears as a status row only while rows are empty, an empty
 * table renders the stock EmptyState placeholder with overridable
 * slots, the filter input is a controlled field whose filtering the
 * host owns. Assertions derive every user-facing string from the
 * locale bundles (or English fixtures), never inline translations.
 */

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
import type { DataTableColumn, DataTableProps } from './DataTable.js'

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
      // The axe scans render under a real page h1 (page-has-heading-one
      // is determinate in jsdom -- see the axe helper header), the way
      // a real host page would mount the table. Behavioural tests keep
      // rendering the bare table; only scans opt into the context.
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
    // Renders deliberately wider than any reasonable host container
    // (12 columns at 300px each = 3600px) to exercise the contract the
    // component's own doc comment states: more or wider columns than the
    // host's container scroll inside the TableContainer wrapper rather
    // than overflowing the page. jsdom does no real layout, so this
    // cannot prove a scrollbar renders at any actual width -- it proves
    // the wrapper MUI gives `overflow-x: auto` by default is genuinely
    // present, so dropping `TableContainer` (the thing that gives this
    // for free) would fail this test instead of silently regressing.
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
    // The pin is a per-table opt-in, never the default, and it is measured
    // rather than assumed: an opted-in table that fits its container keeps
    // its last column in normal flow, while one whose content is wider
    // makes both the header cell and the body cells stick to the scroll
    // container's right edge (the browser tier proves the movement and the
    // measurement against real layout).
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

    // jsdom lays nothing out, so an unmocked container reports equal
    // scroll and client widths: the fitting case.
    const fitting = renderTable({ columns: withActions, pinLastColumn: true })
    const fittingHeaders = fitting.container.querySelectorAll('thead th')
    expect(fittingHeaders[fittingHeaders.length - 1]).not.toHaveStyle({
      position: 'sticky',
    })
    fitting.unmount()

    // Simulate the one measurement the pin reads: the table is wider than
    // its scroll container.
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
    // The region is inserted empty; its text arrives on a later frame.
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
    // The announcement region is mounted for the whole empty phase and
    // carries that phase's text: on the empty-table refresh path (rows
    // stay empty, loading flips false -> true) the region must already
    // exist before the loading text lands, because a live region
    // announces content changes that follow its own existence, never
    // text that mounts together with it. Both fills -- the
    // empty-result wording while not loading, the loading text on
    // refresh -- are therefore text changes inside a region the screen
    // reader already knows, each written a frame after its commit. The
    // same DOM node must survive the transition, which is what makes the
    // later text change an announcement rather than another mount. A
    // stateful host drives the flip, the way a control owned by the host
    // would: its own setState re-renders the table in place, so the same
    // region node survives the loading flip. RTL's rerender keeps the
    // providers mounted now too, but the refresh belongs to the host, and
    // its state update is the transition this case exercises.
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
    // The commit that flipped loading cleared the region; the text
    // arrives on a later frame.
    expect(region.textContent).toBe('')
    await waitFor(() =>
      expect(region).toHaveTextContent(zhCN.dataTable.loading),
    )
    expect(utils.getByRole('progressbar')).toBeInTheDocument()
  })

  it('mounts the loading region empty when the empty phase begins already loading (D5 regression)', async () => {
    // The empty-then-refresh route above covers a region that already
    // existed, but the empty phase can also BEGIN already loading: rows
    // empty out and loading flips in the same host commit (a filter
    // that clears the current page while the next query runs). A region
    // born inside that commit would carry its text with it and the
    // announcement would never fire -- live regions speak about content
    // changes that follow their own existence, never text that mounts
    // together with them. AnnouncementRegion therefore spends the
    // region's first committed frame empty and writes the loading text a
    // frame later, whatever transition entered the phase. The assertion
    // between the two writes below is what distinguishes the shapes: the
    // region's text content must still be empty the moment the node
    // first appears. A stateful host drives the flip; flushSync lands
    // its one commit synchronously, and the animation frames that fill
    // the region have not run yet when the assertion sees the birth
    // frame.
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
    // The region's text is the empty-result wording once the load
    // finishes: the stock EmptyState — the empty result's own
    // presentation — sits outside the region as a sibling, so an
    // end-of-load that left the region empty would let a screen-reader
    // user hear the loading text and then silence, with zero rows to
    // navigate to that would disambiguate still-loading from
    // finished-and-found-nothing. The terminal state lands as a text
    // change inside the same region — never a trailing empty string.
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
    // The commit cleared the loading text; the empty-result wording
    // arrives on a later frame.
    expect(region.textContent).toBe('')
    await waitFor(() =>
      expect(region.textContent).toContain(zhCN.dataTable.noData),
    )
    // The EmptyState visual is untouched: the stock placeholder still
    // renders beneath the region, its own title and description.
    expect(
      utils.getByRole('heading', { name: zhCN.emptyState.empty.title }),
    ).toBeInTheDocument()
    expect(
      utils.getByText(zhCN.emptyState.empty.description),
    ).toBeInTheDocument()
  })

  it('keeps the announced empty wording independent of an overridden visible title (P2-2 regression)', async () => {
    // The region announces the empty result in the ui-kit's own wording
    // (the dataTable namespace, like the loading label); a host's
    // emptyTitle override stays the EmptyState's visible title — the
    // single visual source — rather than a second copy rendered inside
    // the live region, which would present the same text twice.
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
    // The loading fill owes its announcability to the birth discipline
    // (see the test above): the region's first committed frame is empty
    // whatever transition enters the empty phase, so any later text
    // fill is a change. The empty-result fill must spend that same
    // first frame empty — an empty phase entered not loading may not be
    // born carrying the empty-result wording, or that wording is never
    // a change and never announces. The assertion inside the act scope
    // pins the birth frame; the one after it pins the fill.
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
    // The header checkbox may hold focus when a batch delete empties the
    // page, so it stays reachable and reports unavailable instead of being
    // natively disabled.
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

  it('fires the next-page callback and disables previous on the first page', () => {
    const onPageChange = vi.fn()
    // The pager buttons' aria-labels come from the MUI locale the theme
    // merges (English here so no translated string needs inlining).
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
    expect(previous).toBeDisabled()
    fireEvent.click(previous)
    expect(onPageChange).not.toHaveBeenCalled()
    fireEvent.click(utils.getByRole('button', { name: 'Go to next page' }))
    expect(onPageChange).toHaveBeenLastCalledWith(1)
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
    // The input is fully controlled, so the host's value loop must run
    // for typing to land -- the harness plays the host.
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
    // The label is visible and associated, not an accessible-name-only
    // fallback: the input carries no aria-label of its own.
    expect(input).not.toHaveAttribute('aria-label')
    const label = utils.getByText('Filter members', { selector: 'label' })
    expect(label.tagName).toBe('LABEL')
    expect(label).toHaveAttribute('for', input.id)
    // The namespace default no longer names the field.
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

  // The table is a CSS container: the root box declares
  // `container-type: inline-size`, and every responsive rule inside is a
  // container query, not a viewport `@media`. jsdom evaluates neither
  // real layout nor container queries, so the proof below is
  // property/snapshot -- the right declarations, and the container that
  // makes them live, were wired into the render. It cannot prove a real
  // container hides or shows a column, which this package's jsdom suite
  // cannot render at all.
  describe('component container (the context every rule below resolves against)', () => {
    it('declares itself a container, so the rules inside it can resolve -- the parent declaration without which no container query fires', () => {
      renderTable()
      // The failure mode the layout design records: without
      // `container-type` on an ancestor, every `@container` rule below
      // matches nothing and the table never reflows. The root box carries
      // the declaration, so the proof is that it reached the generated
      // CSS.
      expect(emittedStyleText()).toMatch(/container-type:inline-size/)
    })
  })

  // The cell-wrap policy is per column and opt-in. jsdom does no layout,
  // so these cases prove the declarations: a column that opts into
  // 'nowrap' renders its header and every body cell with
  // `white-space: nowrap` (the declaration that keeps a short value or
  // an identifier from breaking mid-token), while a column that leaves
  // the field unset keeps normal text flow so prose can still wrap. The
  // scroll fallback those declarations feed is the TableContainer
  // wrapper asserted by the horizontal-scroll case above; together they
  // are what makes a table wider than its container scroll instead of
  // squeezing its cells. No assertion here measures a rendered width.
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
      // The two halves of the contract: the opted-in cells declare
      // nowrap (so their content cannot shrink), and the wrapper still
      // declares the horizontal scroll that catches the resulting
      // overflow. jsdom cannot prove a scrollbar appears at a real
      // width -- the declaration pair is the observable proof.
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

  // The full-value affordance is the identifier column's second channel:
  // the wrap policy keeps the value unbroken, but the complete text can
  // sit past the container's scroll edge. A column that returns the
  // complete value through `cellFullValue` renders it as the body cell's
  // title and beside the cell content as a focusable copy control; a
  // column without the accessor is untouched. jsdom ships no
  // `navigator.clipboard`, so the suite stubs it and asserts the value
  // handed to it, the announcement that follows, and the locale-derived
  // label the control carries.
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
      // user-event installs its own clipboard stub at setup, so the spy
      // must replace it afterwards -- otherwise the component writes to
      // user-event's stub and the spy sees no call.
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

  // `contentMaxWidth` bounds the whole cell -- the rendered text and the
  // `cellFullValue` copy control together -- so the cap cannot drift wider
  // than declared by one control's width. jsdom renders no layout, so the
  // proof is the generated container-query declarations on the wrapper
  // that holds both, the truncation declarations on the text box alone,
  // and the control sitting outside that box. The real-browser companion
  // measurement (the whole-cell scroll width against the cap) lives in the
  // README's verification record.
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
      // `belowMd` is the 0px baseline and `fromMd` the 900px rule, so the
      // two values differ across the md boundary. jsdom neither evaluates
      // container queries nor lays out, so this pins the generated rules;
      // the real-browser measurement at 600/900/1200px pins which one is
      // in force on either side of md.
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
      // No container-query rule was generated: one value at every width.
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
      // The text box is the button's sibling and owns the truncation; the
      // control sits outside it and refuses to shrink, so it is neither
      // clipped nor counted past the wrapper's cap.
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
      // The cap still bounds the text, but the box wraps instead of
      // truncating: a long value then contributes its longest word (or a
      // single character, for an unbroken one) to the column's minimum
      // width, so the table can shrink it to its tier instead of
      // scrolling.
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
      // The prior inline-flex wrapper shape: the rendered text sits
      // directly in it (no truncating sub-box) beside the control.
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
      // The cell inherits the row's alignment, so one declaration covers
      // the identity cell and every control beside it.
      const row = utils.getByText('Ada').closest('tr') as HTMLElement
      expect(row).toHaveStyle({ verticalAlign: 'top' })
      // The copy control sits on the content's first line instead of the
      // middle of a multi-line cell.
      expect(copyWrapperOf(utils)).toHaveStyle({ alignItems: 'flex-start' })
    })
  })

  describe('column priority (container-triggered reflow)', () => {
    it('renders a column with no priority with no visibility override -- unaffected by the feature', () => {
      // BASE_COLUMNS sets no column's priority; this restates the
      // existing "applies column alignment and width to cells" contract
      // as the feature's backward-compatibility proof: a host that sets
      // no column's priority sees byte-identical rendering, at every
      // container width.
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
      // The default is the container's 0px baseline; the restore is a
      // container query at sm. Both are `@container`, so the column drops
      // by the table's own width -- the previous viewport `@media` left
      // it pinned to the viewport and blind to a narrow panel, which is
      // exactly what a `@container` assertion here is checking against.
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
      // The same declarations pinned above, asserted from the other
      // side: the column's hide/show is a container query, so the
      // viewport-keyed rule the feature used to emit must not be present.
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
      // Hiding is pure CSS, so the cell stays a genuine column in jsdom
      // (which does not evaluate the container query either way) -- the
      // point under test is that the column was never conditionally
      // rendered away, which the loading/empty rows' colSpan depends on,
      // and that an always-visible column alongside it is untouched.
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
      // The filter row is the table's toolbar. Its width is a
      // container-keyed value: no priority column is in this render, so
      // the only `@container` rule in the generated CSS is the filter's
      // own (the root box's `container-type` is a declaration, not a
      // query), which is what makes these strings attributable to it.
      renderTable({ filter: { value: '', onValueChange: vi.fn() } })
      const css = emittedStyleText()
      expect(css).toMatch(/@container \(min-width:0px\)\{[^}]*width:100%/)
      expect(css).toMatch(/@container \(min-width:600px\)\{[^}]*width:20rem/)
    })
  })

  describe('pointer capability', () => {
    it('raises the selection controls to the 44x44 touch-target floor under a coarse pointer', () => {
      // A checkbox's clickable box is its root span, not the inner input,
      // so each assertion matches the emitted CSS against that span. A
      // coarse rule for another control on the page cannot satisfy them.
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
      // The footer is the table's own control surface: its page buttons
      // and the rows-per-page selector are compact MUI controls, so on a
      // finger-driven device each one gates up to the 44x44 floor. The
      // assertions are made against the footer's own elements, so a rule
      // emitted for another gated control on the page does not satisfy
      // them.
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
      // The row highlight is a hover affordance (see the component header
      // note): gated to a fine, hovering pointer, so a coarse pointer --
      // a touch device, even at a wide viewport -- gets no highlight that
      // would stick after a tap.
      expect(css).toContain('@media (hover: hover) and (pointer: fine)')
      expect(css).toMatch(
        /@media \(hover: hover\) and \(pointer: fine\)\{[^}]*:hover/,
      )
    })
  })

  // Each scan renders the table under a real page h1 (the renderTable
  // `heading` option): page-has-heading-one is determinate in jsdom now
  // (see the axe helper header), so a scan document without an h1 fails
  // instead of passing by indeterminacy -- the h1 is the page context a
  // real host page supplies.
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
    // emptyHeadingLevel supplied, as a caller that sets headingLevel
    // would: the stock placeholder h6 under the real page h1 is a
    // genuine heading-order skip (pinned by the two tests below).
    renderTable(
      { rows: [], emptyHeadingLevel: 'h2' },
      { heading: 'Members' },
    )
    await expectNoAxeViolations()
  })

  describe('the empty placeholder under a real page heading (P2-3)', () => {
    // The axe case covering the empty state must render the table
    // actually empty: a case rendered with loading: true never shows
    // the stock EmptyState placeholder at all -- and a component-level
    // render has no ancestor heading anyway -- so the real surface's
    // h1 -> h6 skip would stay invisible to the package suite. A real
    // page surface is exactly that shape: a page
    // h1 above an empty table whose stock placeholder defaults to the
    // h6 heading level. These two cases supply the h1 themselves (the
    // same mechanism EmptyState.test.tsx uses) so the skip is
    // reachable, mirroring that suite's pair: the
    // stock default under a real h1 still skips (a compatibility floor,
    // not a claim that h6 is correct), and supplying the level that
    // continues the page order removes the violation entirely.

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
