/**
 * The fully controlled data-grid view: `rows` render exactly as given, and
 * sorting and filtering are state echo plus callbacks. Horizontal overflow
 * scrolls inside MUI's `TableContainer`, a tested contract.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Checkbox from '@mui/material/Checkbox'
import CircularProgress from '@mui/material/CircularProgress'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableContainer from '@mui/material/TableContainer'
import TableHead from '@mui/material/TableHead'
import TablePagination from '@mui/material/TablePagination'
import TableRow from '@mui/material/TableRow'
import TableSortLabel from '@mui/material/TableSortLabel'
import TextField from '@mui/material/TextField'
import type { SxProps, Theme } from '@mui/material/styles'
import { EmptyState } from './EmptyState.js'
import type { EmptyStateHeadingLevel } from './EmptyState.js'
import { useUiKitTranslation } from '../internal/translation.js'
import { CopyButton } from './CopyButton.js'
import { POINTER_FINE_HOVER_QUERY } from '../internal/pointer.js'
import { visuallyHiddenSx } from '../visually-hidden.js'
import { useActivationGuard } from '../activation-guard.js'
import { AnnouncementRegion } from './AnnouncementRegion.js'

/**
 * Opt-in column visibility priority: 'low' is hidden first as the container
 * shrinks, 'high' last; no priority is never hidden.
 */
export type DataTableColumnPriority = 'high' | 'medium' | 'low'

/**
 * Opt-in per-column line policy: 'wrap' keeps normal text flow, 'nowrap'
 * keeps every cell in the column on one line.
 */
export type DataTableColumnCellWrap = 'wrap' | 'nowrap'

/**
 * A whole-cell width cap: a plain px number, or the `{ belowMd, fromMd }`
 * pair the component turns into a container query at the container's `md`.
 */
export type DataTableColumnContentMaxWidth =
  | number
  | {
      readonly belowMd: number
      readonly fromMd: number
    }

/**
 * A right-aligned column wraps its content in a flex row: the cell's text
 * alignment does not move an inline-flex child.
 */
function alignCellContent(
  column: { readonly align?: 'left' | 'center' | 'right' },
  content: ReactNode,
): ReactNode {
  if (column.align !== 'right') {
    return content
  }
  return (
    <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>{content}</Box>
  )
}

export interface DataTableColumn<T> {
  /** Stable column identity; also the sort-state columnId. */
  readonly id: string
  /** Column header content. */
  readonly header: ReactNode
  /** Whether clicking the header offers sorting on this column. */
  readonly sortable?: boolean
  /** Cell text alignment; defaults to left. */
  readonly align?: 'left' | 'center' | 'right'
  /** Table-cell width hint. */
  readonly width?: number | string
  /** Opt-in visibility tier; omitting it keeps the column always visible. */
  readonly priority?: DataTableColumnPriority
  /** Opt-in line policy for this column's header and cells. */
  readonly cellWrap?: DataTableColumnCellWrap
  /**
   * Returns a row's complete value, rendered as the cell's `title` and as a
   * focusable copy control beside the content. A row answered `undefined`
   * gets neither.
   */
  readonly cellFullValue?: (row: T) => string | undefined
  /**
   * Bounds the cell content and the `cellFullValue` copy control together,
   * so the text truncates inside the cap rather than the control widening it.
   */
  readonly contentMaxWidth?: DataTableColumnContentMaxWidth
  /** Renders one row's cell; rowIndex is the index within `rows`. */
  readonly cell: (row: T, rowIndex: number) => ReactNode
}

/** The controlled sort direction shown on the active column. */
export type DataTableSortDirection = 'asc' | 'desc'

/** The controlled sort state; null means "no active sort". */
export interface DataTableSort {
  /** The id of the sorted column. */
  readonly columnId: string
  /** The sort direction, ascending or descending. */
  readonly direction: DataTableSortDirection
}

/** The controlled filter input: the host owns value and filtering. */
export interface DataTableFilter {
  /** The current filter text; the host owns it and filters its rows. */
  readonly value: string
  /** Fired on every input change; the host filters rows itself. */
  readonly onValueChange: (value: string) => void
  /** The filter field's visible label and accessible name. */
  readonly label?: string
  /** The field's placeholder; defaults to the ui-kit `dataTable.filterLabel`. */
  readonly placeholder?: string
}

/** The controlled pagination state (MUI conventions: page is 0-based). */
export interface DataTablePagination {
  /** The zero-based page currently shown. */
  readonly page: number
  /** Rows per page, as displayed in the footer selector. */
  readonly rowsPerPage: number
  /** Total rows across all pages; -1 when unknown. */
  readonly count: number
  /** Selector options; defaults to MUI's [10, 25, 100]. */
  readonly rowsPerPageOptions?: readonly number[]
  /** Fired when the host should move to another page. */
  readonly onPageChange: (page: number) => void
  /** Fired with the new rows-per-page value (page reset is the host's). */
  readonly onRowsPerPageChange: (rowsPerPage: number) => void
}

export interface DataTableProps<T> {
  /** The rows to render now (see header note: no implicit sorting or slicing). */
  readonly rows: readonly T[]
  /** The columns to render, in display order. */
  readonly columns: readonly DataTableColumn<T>[]
  /**
   * Stable key per row, used as the React key and the selection key; defaults
   * to the row index, which a selectable or reorderable table must not use.
   */
  readonly rowKey?: (row: T, rowIndex: number) => string | number
  /** Renders a status row instead of content while rows are empty. */
  readonly loading?: boolean
  /** The stock empty placeholder title's heading level; defaults to EmptyState's 'h6'. */
  readonly emptyHeadingLevel?: EmptyStateHeadingLevel
  /** Table density; defaults to 'medium'. */
  readonly size?: 'small' | 'medium'
  /**
   * The current sort; give it with `onSortChange` to enable sorting. null is
   * on with no active column. The host applies the sort.
   */
  readonly sort?: DataTableSort | null
  /** Fired when a sortable header is clicked (cycle: new column asc, active column flips). */
  readonly onSortChange?: (sort: DataTableSort) => void
  /** Renders the filter input above the table; filtering stays with the host. */
  readonly filter?: DataTableFilter
  /** Keys of the selected rows (selection keys are `rowKey` results). */
  readonly selectedRowKeys?: readonly (string | number)[]
  /** Presence of this callback turns row selection on. */
  readonly onSelectionChange?: (keys: readonly (string | number)[]) => void
  /** Renders the MUI TablePagination footer with these controlled values. */
  readonly pagination?: DataTablePagination
  /** The table's accessible name; ignored when `ariaLabelledBy` is given. */
  readonly ariaLabel?: string
  /** The id of an element that names the table, replacing the inline name. */
  readonly ariaLabelledBy?: string
  /** Overrides the built-in empty title (rendered inside the stock EmptyState). */
  readonly emptyTitle?: ReactNode
  /** Overrides the built-in empty description. */
  readonly emptyDescription?: ReactNode
  /** Optional action inside the empty placeholder. */
  readonly emptyAction?: ReactNode
  /**
   * `'top'` aligns every body cell, and the copy control, to the top of the
   * row; omit it for MUI's centring.
   */
  readonly rowVerticalAlign?: 'middle' | 'top'
  /**
   * Pins the last column's header and body cells to the right edge while the
   * main columns scroll under them, only when the table actually overflows.
   */
  readonly pinLastColumn?: boolean
  /** Extra styling applied to the outermost box. */
  readonly sx?: SxProps<Theme>
}

function flip(direction: DataTableSortDirection): DataTableSortDirection {
  return direction === 'asc' ? 'desc' : 'asc'
}

/**
 * The primary-identity column's width cap, answering to the table's own
 * container: 160px below `md`, 200px from `md` up.
 */
export const PRIMARY_COLUMN_MAX_WIDTH = {
  belowMd: 160,
  fromMd: 200,
} as const

/**
 * The flat width cap for long-text columns: it deliberately does not grow
 * with the container tier, so several still fit the tier's floor width.
 */
export const LONG_TEXT_COLUMN_MAX_WIDTH = 160

/** Converts a cap into the `sx` value: a number, or the `@`/`@md` pair. */
function contentMaxWidthSx(
  value: DataTableColumnContentMaxWidth,
): number | { '@': number; '@md': number } {
  if (typeof value === 'number') return value
  return { '@': value.belowMd, '@md': value.fromMd }
}

/**
 * `PRIMARY_COLUMN_MAX_WIDTH` as a container-query `sx` value, for a capped
 * cell without a copy control; a column with `cellFullValue` uses `contentMaxWidth`.
 */
export const primaryColumnMaxWidthSx = {
  maxWidth: contentMaxWidthSx(PRIMARY_COLUMN_MAX_WIDTH),
}

/**
 * The container width each priority tier stays visible from: 'low' only from
 * `lg`, 'high` from `sm` up.
 */
const PRIORITY_VISIBLE_FROM: Record<DataTableColumnPriority, 'sm' | 'md' | 'lg'> = {
  high: 'sm',
  medium: 'md',
  low: 'lg',
}

/**
 * A priority as a container-query `display`: `none` at the container's 0px
 * baseline, `table-cell` restored explicitly from the tier up, because MUI's
 * container-query value only adds rules.
 */
function columnVisibilitySx(
  priority: DataTableColumnPriority | undefined,
): { display: Record<string, string> } | undefined {
  if (priority === undefined) return undefined
  const visibleFrom = PRIORITY_VISIBLE_FROM[priority]
  return { display: { '@': 'none', [`@${visibleFrom}`]: 'table-cell' } }
}

/** Only 'nowrap' emits a declaration; anything else returns undefined. */
function columnCellWrapSx(
  cellWrap: DataTableColumnCellWrap | undefined,
): { whiteSpace: 'nowrap' } | undefined {
  if (cellWrap !== 'nowrap') return undefined
  return { whiteSpace: 'nowrap' }
}

/**
 * The text box inside a capped cell. 'nowrap' truncates at the cap, so the
 * column's minimum width is the cap; 'wrap' breaks anywhere, so the minimum
 * comes from its longest word and the table can shrink below the cap.
 */
function cappedCellTextSx(
  cellWrap: DataTableColumnCellWrap | undefined,
): SxProps<Theme> {
  if (cellWrap === 'wrap') {
    return {
      minWidth: 0,
      overflow: 'hidden',
      overflowWrap: 'anywhere',
      whiteSpace: 'normal',
    }
  }
  return {
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }
}

/**
 * A pinned cell sticks to the scroll container's right edge and stays
 * opaque, compositing the surface fill over an opaque paper base. On a
 * fine pointer it repeats the row's hover layer above that composite.
 */
function pinnedColumnSx(
  pinned: boolean,
  header: boolean,
): SxProps<Theme> | undefined {
  if (!pinned) return undefined
  // Repeated as a gradient layer so it can composite over the opaque base.
  const surfaceLayer = (theme: Theme): string => {
    const fill = theme.surface.container.fill
    return `linear-gradient(${fill}, ${fill})`
  }
  return {
    position: 'sticky',
    right: 0,
    zIndex: header ? 3 : 1,
    // The opaque base hides what scrolls under; the surface fill alone could be translucent.
    backgroundColor: (theme) => theme.palette.background.paper,
    backgroundImage: surfaceLayer,
    boxShadow: (theme) => `inset 1px 0 0 ${theme.palette.divider}`,
    ...(header
      ? {}
      : {
          [POINTER_FINE_HOVER_QUERY]: {
            // The row's own highlight sits behind this cell's opaque base, so repeat it.
            'tr:hover &': {
              backgroundImage: (theme) => {
                const hover = theme.palette.action.hover
                return `linear-gradient(${hover}, ${hover}), ${surfaceLayer(theme)}`
              },
            },
          },
        }),
  }
}

/** The controlled table view: every knob is props in, callbacks out. */
export function DataTable<T>({
  rows,
  columns,
  rowKey,
  loading = false,
  size = 'medium',
  sort,
  onSortChange,
  filter,
  selectedRowKeys,
  onSelectionChange,
  pagination,
  emptyTitle,
  emptyDescription,
  emptyAction,
  emptyHeadingLevel,
  ariaLabel,
  ariaLabelledBy,
  rowVerticalAlign,
  pinLastColumn = false,
  sx,
}: DataTableProps<T>) {
  const { t } = useUiKitTranslation()

  const emptyAnnouncement = loading
    ? t('dataTable.loading')
    : t('dataTable.noData')

  // Watch both boxes: neither one's own box reports the other moving.
  // jsdom has no layout, so the unit tier proves the declarations only.
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [containerOverflows, setContainerOverflows] = useState(false)
  useEffect(() => {
    if (!pinLastColumn) {
      setContainerOverflows(false)
      return
    }
    const container = containerRef.current
    if (container === null) return
    const measure = (): void => {
      setContainerOverflows(container.scrollWidth > container.clientWidth)
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(container)
    const table = container.querySelector('table')
    if (table !== null) {
      observer.observe(table)
    }
    return () => observer.disconnect()
  }, [pinLastColumn])

  const selectable = onSelectionChange !== undefined
  const sortingEnabled = onSortChange !== undefined && sort !== undefined
  const selected = new Set<string | number>(selectedRowKeys ?? [])

  const keyOf = (row: T, index: number): string | number =>
    rowKey === undefined ? index : rowKey(row, index)

  const pageKeys = rows.map(keyOf)
  const allSelected =
    pageKeys.length > 0 && pageKeys.every((key) => selected.has(key))
  const someSelected = !allSelected && pageKeys.some((key) => selected.has(key))

  const span = columns.length + (selectable ? 1 : 0)

  // The last declared column only, and only while the table actually overflows.
  const lastColumnId =
    columns.length === 0 ? undefined : columns[columns.length - 1]?.id
  const isPinnedColumn = (column: DataTableColumn<T>): boolean =>
    pinLastColumn && containerOverflows && column.id === lastColumnId

  const toggleAll = () => {
    if (!selectable) return
    const current = selectedRowKeys ?? []
    const next = allSelected
      ? current.filter((key) => !pageKeys.includes(key))
      : [...current, ...pageKeys.filter((key) => !selected.has(key))]
    onSelectionChange(next)
  }
  // The header checkbox may hold focus when the page empties, so it stays focusable.
  const handleToggleAll = useActivationGuard(toggleAll, {
    busy: false,
    unavailable: pageKeys.length === 0,
  })

  const toggleRow = (key: string | number) => {
    if (!selectable) return
    const current = selectedRowKeys ?? []
    const next = selected.has(key)
      ? current.filter((existing) => existing !== key)
      : [...current, key]
    onSelectionChange(next)
  }

  // MUI v9 sets aria-checked="mixed" but not input.indeterminate, which axe flags.
  const headerInputRef = useCallback(
    (instance: HTMLInputElement | null) => {
      if (instance !== null) {
        instance.indeterminate = someSelected
      }
    },
    [someSelected],
  )

  const handleSortClick = (columnId: string) => {
    if (!sortingEnabled) return
    const next =
      sort?.columnId === columnId
        ? { columnId, direction: flip(sort.direction) }
        : { columnId, direction: 'asc' as const }
    onSortChange(next)
  }

  const activeSortOf = (columnId: string): DataTableSortDirection | null =>
    sortingEnabled && sort !== null && sort !== undefined &&
    sort.columnId === columnId
      ? sort.direction
      : null

  return (
    // No nested element re-declares `container-type`, so this box is the nearest container.
    <Box sx={{ width: '100%', containerType: 'inline-size', ...sx }}>
      {filter !== undefined && (
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            marginBottom: 1.5,
          }}
        >
          <TextField
            size="small"
            label={filter.label}
            value={filter.value}
            onChange={(event) => filter.onValueChange(event.target.value)}
            slotProps={{
              htmlInput: {
                // Without a visible label the namespace string is the accessible name.
                ...(filter.label === undefined
                  ? { 'aria-label': t('dataTable.filterLabel') }
                  : {}),
                placeholder: filter.placeholder ?? t('dataTable.filterLabel'),
              },
            }}
            // Container-keyed: full row below `sm`, 20rem from it up.
            sx={{ width: { '@': '100%', '@sm': '20rem' }, maxWidth: '100%' }}
          />
        </Box>
      )}
      <TableContainer ref={containerRef}>
        <Table
          aria-label={
            ariaLabelledBy === undefined
              ? (ariaLabel ?? t('dataTable.ariaLabel'))
              : undefined
          }
          aria-labelledby={ariaLabelledBy}
          size={size}
        >
          <TableHead>
            <TableRow>
              {selectable && (
                <TableCell padding="checkbox">
                  <Checkbox
                    checked={allSelected}
                    indeterminate={someSelected}
                    onChange={handleToggleAll}
                    slotProps={{
                      input: {
                        'aria-label': t('dataTable.selectAllRows'),
                        'aria-disabled':
                          pageKeys.length === 0 ? true : undefined,
                        ref: headerInputRef,
                      },
                    }}
                  />
                </TableCell>
              )}
              {columns.map((column) => {
                const activeDirection = activeSortOf(column.id)
                return (
                  <TableCell
                    key={column.id}
                    align={column.align}
                    sx={{
                      width: column.width,
                      ...columnVisibilitySx(column.priority),
                      ...columnCellWrapSx(column.cellWrap),
                      ...pinnedColumnSx(isPinnedColumn(column), true),
                    }}
                    aria-sort={
                      activeDirection === 'asc'
                        ? 'ascending'
                        : activeDirection === 'desc'
                          ? 'descending'
                          : undefined
                    }
                  >
                    {sortingEnabled && column.sortable ? (
                      <TableSortLabel
                        active={activeDirection !== null}
                        direction={activeDirection ?? 'asc'}
                        hideSortIcon={activeDirection === null}
                        onClick={() => handleSortClick(column.id)}
                      >
                        {column.header}
                      </TableSortLabel>
                    ) : (
                      column.header
                    )}
                  </TableCell>
                )
              })}
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={span}
                  align="center"
                  sx={{ border: 0, padding: 0 }}
                >
                  {/* The region stands for the whole empty phase; when not loading it is
                      clipped, never display:none, because a hidden region would not be live. */}
                  <Box
                    sx={
                      loading
                        ? {
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            gap: 1.5,
                            paddingY: 4,
                            color: 'text.secondary',
                          }
                        : { display: 'flex' }
                    }
                  >
                    {loading && (
                      <CircularProgress
                        size={22}
                        aria-label={t('dataTable.loading')}
                      />
                    )}
                    <AnnouncementRegion
                      message={emptyAnnouncement}
                      sx={loading ? undefined : visuallyHiddenSx}
                    />
                  </Box>
                  {!loading && (
                    <EmptyState
                      variant="empty"
                      title={emptyTitle}
                      description={emptyDescription}
                      action={emptyAction}
                      headingLevel={emptyHeadingLevel}
                      sx={{ paddingY: 4, paddingX: 2 }}
                    />
                  )}
                </TableCell>
              </TableRow>
            )}
            {rows.map((row, index) => {
              const key = keyOf(row, index)
              return (
                <TableRow
                  key={key}
                  // Hover only on a fine pointer; `vertical-align` on the row covers every cell.
                  sx={{
                    ...(rowVerticalAlign === 'top'
                      ? { verticalAlign: 'top' }
                      : {}),
                    [POINTER_FINE_HOVER_QUERY]: {
                      '&:hover': { backgroundColor: 'action.hover' },
                    },
                  }}
                >
                  {selectable && (
                    <TableCell padding="checkbox">
                      <Checkbox
                        checked={selected.has(key)}
                        onChange={() => toggleRow(key)}
                        slotProps={{
                          input: {
                            'aria-label': t('dataTable.selectRow', {
                              row: index + 1,
                            }),
                          },
                        }}
                      />
                    </TableCell>
                  )}
                  {columns.map((column) => {
                    const fullValue = column.cellFullValue?.(row)
                    const cap = column.contentMaxWidth
                    const capped = cap !== undefined
                    return (
                      <TableCell
                        key={column.id}
                        align={column.align}
                        title={fullValue}
                        sx={{
                          width: column.width,
                          ...columnVisibilitySx(column.priority),
                          ...columnCellWrapSx(column.cellWrap),
                          ...pinnedColumnSx(isPinnedColumn(column), false),
                        }}
                      >
                        {alignCellContent(column, fullValue === undefined ? (
                          capped ? (
                            <Box
                              sx={{
                                maxWidth: contentMaxWidthSx(cap),
                                ...cappedCellTextSx(column.cellWrap),
                              }}
                            >
                              {column.cell(row, index)}
                            </Box>
                          ) : (
                            column.cell(row, index)
                          )
                        ) : (
                          <Box
                            sx={{
                              display: 'inline-flex',
                              // A top-aligned cell puts the copy control on the identity line.
                              alignItems:
                                rowVerticalAlign === 'top'
                                  ? 'flex-start'
                                  : 'center',
                              columnGap: 0.5,
                              ...(capped
                                ? { maxWidth: contentMaxWidthSx(cap) }
                                : {}),
                            }}
                          >
                            {capped ? (
                              // The text yields width to the control, which stays whole.
                              <Box sx={cappedCellTextSx(column.cellWrap)}>
                                {column.cell(row, index)}
                              </Box>
                            ) : (
                              column.cell(row, index)
                            )}
                            <CopyButton
                              value={fullValue}
                              pinned={capped}
                              label={t('dataTable.copyValue', {
                                value: fullValue,
                              })}
                              copiedLabel={t('dataTable.copiedValue', {
                                value: fullValue,
                              })}
                            />
                          </Box>
                        ))}
                      </TableCell>
                    )
                  })}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </TableContainer>
      {pagination !== undefined && (
        <TablePagination
          component="div"
          count={pagination.count}
          page={pagination.page}
          rowsPerPage={pagination.rowsPerPage}
          rowsPerPageOptions={pagination.rowsPerPageOptions}
          onPageChange={(_event, page) => pagination.onPageChange(page)}
          onRowsPerPageChange={(event) =>
            pagination.onRowsPerPageChange(Number(event.target.value))
          }
          labelRowsPerPage={t('dataTable.rowsPerPage')}
          labelDisplayedRows={({ from, to, count: total }) =>
            total === -1
              ? t('dataTable.displayedRowsUnknown', { from, to })
              : t('dataTable.displayedRows', { from, to, count: total })
          }
        />
      )}
    </Box>
  )
}
