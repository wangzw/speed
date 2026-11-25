/**
 * The table footer: MUI's TablePagination shell with two paging directions
 * that stay focusable in every state. Both directions carry
 * `aria-disabled` when they cannot be used and swallow activation instead
 * of being natively disabled, so a keyboard or screen-reader operator keeps
 * the focus the activation gave them. MUI's own actions component is
 * replaced because it moves focus away from a direction that becomes
 * unavailable; that move is the defect this component exists to avoid.
 */

import { createContext, useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import IconButton from '@mui/material/IconButton'
import TablePagination from '@mui/material/TablePagination'
import type { TablePaginationActionsProps } from '@mui/material/TablePaginationActions'
import { useActivationGuard } from '../activation-guard.js'
import { ChevronLeftIcon, ChevronRightIcon } from '../internal/icons.js'
import { useUiKitTranslation } from '../internal/translation.js'
import type { DataTablePagination } from './DataTable.js'

export interface TablePagerProps extends DataTablePagination {
  /**
   * With `count: -1`, whether a next page exists; ignored when the total is
   * known. Defaults to true.
   */
  readonly hasNext?: boolean
}

type PagerDirection = 'previous' | 'next'

interface StartedRead {
  readonly direction: PagerDirection
  /** The page the activated direction moves to. */
  readonly page: number
}

interface DirectionState {
  readonly busy: boolean
  readonly unavailable: boolean
}

interface PagerActionsState {
  readonly previous: DirectionState
  readonly next: DirectionState
  readonly onPrevious: () => void
  readonly onNext: () => void
}

const PagerActionsContext = createContext<PagerActionsState | null>(null)

interface DirectionButtonProps {
  readonly label: string
  readonly state: DirectionState
  readonly icon: ReactNode
  readonly onActivate: () => void
}

/**
 * One direction. It keeps the plain button element across every state, so a
 * focused direction reconciles in place rather than remounting.
 */
function DirectionButton({
  label,
  state,
  icon,
  onActivate,
}: DirectionButtonProps) {
  const activate = useActivationGuard(onActivate, {
    busy: state.busy,
    unavailable: state.unavailable,
  })
  return (
    <IconButton
      type="button"
      color="inherit"
      aria-label={label}
      title={label}
      aria-disabled={state.busy || state.unavailable ? true : undefined}
      aria-busy={state.busy ? true : undefined}
      onClick={activate}
    >
      {icon}
    </IconButton>
  )
}

/**
 * MUI renders this through `ActionsComponent`, below the provider that
 * carries the state. It is module-level so its type is stable across
 * renders.
 */
function PagerActions({
  className,
  getItemAriaLabel,
}: TablePaginationActionsProps) {
  const state = useContext(PagerActionsContext)
  if (state === null) return null
  return (
    <div className={className}>
      <DirectionButton
        label={getItemAriaLabel('previous')}
        state={state.previous}
        icon={<ChevronLeftIcon />}
        onActivate={state.onPrevious}
      />
      <DirectionButton
        label={getItemAriaLabel('next')}
        state={state.next}
        icon={<ChevronRightIcon />}
        onActivate={state.onNext}
      />
    </div>
  )
}

/**
 * The controlled table footer. A write in flight makes both directions
 * unavailable; a page read makes the direction that started it busy and the
 * other unavailable; a read started elsewhere makes both unavailable; a
 * list boundary makes that direction unavailable. A direction is remembered
 * only across a move whose read is in flight, so a page move that starts no
 * read, or one the caller refuses, leaves the next read external. The record
 * does not distinguish scopes: a caller whose filter or tenant change resets
 * the page to 0 renders the footer with that scope as its React key, so the
 * read after the change counts as started elsewhere. A change to
 * `rowsPerPage` drops the record. `DataTable` keys the footer it renders by
 * its own filter value and sort, so a change to either remounts only the
 * footer; a host keys the whole table by a scope that a control outside the
 * table changes, since the footer is out of its reach.
 */
export function TablePager({
  page,
  rowsPerPage,
  count,
  rowsPerPageOptions,
  onPageChange,
  onRowsPerPageChange,
  unavailable = false,
  reading = false,
  hasNext = true,
}: TablePagerProps) {
  const { t } = useUiKitTranslation()
  const [startedRead, setStartedRead] = useState<StartedRead | null>(null)

  // A page move that starts no read (a client-side slice, a cached page)
  // never sets `reading`, so the record would survive it and make a later
  // refresh's read look like this direction's.
  useEffect(() => {
    if (!reading) {
      setStartedRead(null)
    }
  }, [reading, page])

  // A caller may refuse the move outright, changing neither `page` nor
  // `reading`; the effect above never runs then, and the record would attach
  // to a later external read that starts on the refused page. The refusal
  // shows only in a render, so the record is dropped there: a page that has
  // not reached the record's target while no read runs is a refused move.
  useEffect(() => {
    if (startedRead !== null && !reading && page !== startedRead.page) {
      setStartedRead(null)
    }
  })

  // A rows-per-page change usually resets the page, where the read of the new
  // scope can still land on the page the record names.
  useEffect(() => {
    setStartedRead(null)
  }, [rowsPerPage])

  // A read the caller started elsewhere names no direction, so both
  // directions are then unavailable.
  const readDirection =
    reading && startedRead !== null && startedRead.page === page
      ? startedRead.direction
      : null
  const previousBusy = !unavailable && readDirection === 'previous'
  const nextBusy = !unavailable && readDirection === 'next'
  const atLastPage =
    count === -1
      ? hasNext === false
      : page >= Math.ceil(count / rowsPerPage) - 1
  const previousUnavailable =
    unavailable || page === 0 || (reading && !previousBusy)
  const nextUnavailable =
    unavailable || atLastPage || (reading && !nextBusy)
  const move = (direction: PagerDirection, target: number): void => {
    setStartedRead({ direction, page: target })
    onPageChange(target)
  }

  const actionsState: PagerActionsState = {
    previous: { busy: previousBusy, unavailable: previousUnavailable },
    next: { busy: nextBusy, unavailable: nextUnavailable },
    onPrevious: () => move('previous', page - 1),
    onNext: () => move('next', page + 1),
  }

  return (
    <PagerActionsContext.Provider value={actionsState}>
      <TablePagination
        component="div"
        count={count}
        page={page}
        rowsPerPage={rowsPerPage}
        rowsPerPageOptions={rowsPerPageOptions}
        onPageChange={(_event, nextPage) => onPageChange(nextPage)}
        onRowsPerPageChange={(event) =>
          onRowsPerPageChange(Number(event.target.value))
        }
        labelRowsPerPage={t('dataTable.rowsPerPage')}
        labelDisplayedRows={({ from, to, count: total }) =>
          total === -1
            ? t('dataTable.displayedRowsUnknown', { from, to })
            : t('dataTable.displayedRows', { from, to, count: total })
        }
        ActionsComponent={PagerActions}
        slotProps={{ select: { disabled: unavailable } }}
      />
    </PagerActionsContext.Provider>
  )
}
