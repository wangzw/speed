/**
 * TablePager in jsdom: the state each direction reports. jsdom cannot see a
 * control's focusability, so the focus itself is proven in the browser tier;
 * these cases pin the attributes and the swallowed activation.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { fireEvent, screen } from '@testing-library/react'
import { zhCN as muiZhCN } from '@mui/material/locale'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { TablePager } from './TablePager.js'
import type { TablePagerProps } from './TablePager.js'

const MUI_DEFAULTS = muiZhCN.components?.MuiTablePagination?.defaultProps
const NEXT_LABEL = MUI_DEFAULTS?.getItemAriaLabel?.('next') ?? ''
const PREVIOUS_LABEL = MUI_DEFAULTS?.getItemAriaLabel?.('previous') ?? ''

const DATA_TABLE = zhCN.dataTable as {
  readonly rowsPerPage: string
  readonly displayedRows: string
  readonly displayedRowsUnknown: string
}

function renderTemplate(
  template: string,
  vars: Readonly<Record<string, string | number>>,
): string {
  return Object.entries(vars).reduce(
    (out, [key, value]) => out.replaceAll(`{{${key}}}`, String(value)),
    template,
  )
}

function renderPager(props: Partial<TablePagerProps> = {}) {
  const onPageChange = vi.fn()
  const onRowsPerPageChange = vi.fn()
  const utils = renderWithProviders(
    <div>
      <h1>Pager</h1>
      <TablePager
        page={0}
        rowsPerPage={2}
        count={5}
        rowsPerPageOptions={[2, 5]}
        onPageChange={onPageChange}
        onRowsPerPageChange={onRowsPerPageChange}
        {...props}
      />
    </div>,
  )
  return {
    ...utils,
    onPageChange,
    onRowsPerPageChange,
    next: screen.getByRole('button', { name: NEXT_LABEL }),
    previous: screen.getByRole('button', { name: PREVIOUS_LABEL }),
  }
}

function assertNotNativelyDisabled(control: HTMLElement): void {
  expect(control).not.toBeDisabled()
}

function assertUnavailable(control: HTMLElement): void {
  expect(control).toHaveAttribute('aria-disabled', 'true')
  expect(control).not.toHaveAttribute('aria-busy')
  assertNotNativelyDisabled(control)
}

function assertBusy(control: HTMLElement): void {
  expect(control).toHaveAttribute('aria-disabled', 'true')
  expect(control).toHaveAttribute('aria-busy', 'true')
  assertNotNativelyDisabled(control)
}

function assertAvailable(control: HTMLElement): void {
  expect(control).not.toHaveAttribute('aria-disabled')
  expect(control).not.toHaveAttribute('aria-busy')
  assertNotNativelyDisabled(control)
}

interface StartedReadHarnessProps {
  readonly initialPage: number
  readonly onPageChange: (page: number) => void
}

function StartedReadHarness({
  initialPage,
  onPageChange,
}: StartedReadHarnessProps) {
  const [current, setCurrent] = useState({ page: initialPage, reading: false })
  return (
    <TablePager
      page={current.page}
      rowsPerPage={2}
      count={5}
      rowsPerPageOptions={[2, 5]}
      reading={current.reading}
      onPageChange={(page) => {
        onPageChange(page)
        setCurrent({ page, reading: true })
      }}
      onRowsPerPageChange={() => undefined}
    />
  )
}

describe('TablePager', () => {
  it('renders the footer labels and the selector with the given values', () => {
    const utils = renderPager({ page: 0, rowsPerPage: 2, count: 5 })

    expect(screen.getByText(DATA_TABLE.rowsPerPage)).toBeInTheDocument()
    expect(
      screen.getByText(
        renderTemplate(DATA_TABLE.displayedRows, { from: 1, to: 2, count: 5 }),
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('combobox')).toHaveTextContent('2')
    expect(utils.next).toBeInTheDocument()
    expect(utils.previous).toBeInTheDocument()
  })

  it('uses the unknown-total wording with count: -1', () => {
    renderPager({ count: -1 })

    expect(
      screen.getByText(
        renderTemplate(DATA_TABLE.displayedRowsUnknown, { from: 1, to: 2 }),
      ),
    ).toBeInTheDocument()
  })

  it('marks both directions unavailable while a write is in flight, and swallows activation', () => {
    const utils = renderPager({ unavailable: true })

    assertUnavailable(utils.previous)
    assertUnavailable(utils.next)

    fireEvent.click(utils.previous)
    fireEvent.click(utils.next)
    expect(utils.onPageChange).not.toHaveBeenCalled()
  })

  it('marks the next direction busy while the read it started runs, and the other unavailable', () => {
    const onPageChange = vi.fn()
    renderWithProviders(
      <StartedReadHarness initialPage={0} onPageChange={onPageChange} />,
    )
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })

    fireEvent.click(next)

    expect(onPageChange).toHaveBeenCalledTimes(1)
    expect(onPageChange).toHaveBeenCalledWith(1)
    assertBusy(next)
    assertUnavailable(previous)

    fireEvent.click(next)
    expect(onPageChange).toHaveBeenCalledTimes(1)
  })

  it('marks the previous direction busy while the read it started runs', () => {
    const onPageChange = vi.fn()
    renderWithProviders(
      <StartedReadHarness initialPage={1} onPageChange={onPageChange} />,
    )
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })

    fireEvent.click(previous)

    expect(onPageChange).toHaveBeenCalledWith(0)
    assertBusy(previous)
    assertUnavailable(next)
  })

  it('marks both directions unavailable while a read started elsewhere runs', () => {
    const utils = renderPager({ reading: true })

    assertUnavailable(utils.previous)
    assertUnavailable(utils.next)

    fireEvent.click(utils.next)
    expect(utils.onPageChange).not.toHaveBeenCalled()
  })

  it('forgets a direction record once its page move starts no read, so a later refresh leaves both unavailable', () => {
    const onPageChange = vi.fn()

    function ClientSliceHarness() {
      const [current, setCurrent] = useState({ page: 0, reading: false })
      return (
        <>
          <TablePager
            page={current.page}
            rowsPerPage={2}
            count={5}
            rowsPerPageOptions={[2, 5]}
            reading={current.reading}
            onPageChange={(page) => {
              onPageChange(page)
              setCurrent((previous) => ({
                page,
                reading: previous.reading,
              }))
            }}
            onRowsPerPageChange={() => undefined}
          />
          <button
            type="button"
            onClick={() =>
              setCurrent((previous) => ({ ...previous, reading: true }))
            }
          >
            Refresh
          </button>
        </>
      )
    }

    renderWithProviders(<ClientSliceHarness />)
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })

    fireEvent.click(next)
    expect(onPageChange).toHaveBeenCalledWith(1)
    assertAvailable(next)
    assertAvailable(previous)

    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))

    assertUnavailable(next)
    assertUnavailable(previous)
    fireEvent.click(next)
    expect(onPageChange).toHaveBeenCalledTimes(1)
  })

  it('forgets a direction record whose move the parent ignored, so a later external read leaves both unavailable', () => {
    const onPageChange = vi.fn()

    function IgnoredMoveHarness() {
      const [current, setCurrent] = useState({ page: 0, reading: false })
      return (
        <>
          <TablePager
            page={current.page}
            rowsPerPage={2}
            count={5}
            rowsPerPageOptions={[2, 5]}
            reading={current.reading}
            onPageChange={(page) => {
              // The parent refuses the move: neither `page` nor `reading`
              // changes.
              onPageChange(page)
            }}
            onRowsPerPageChange={() => undefined}
          />
          <button
            type="button"
            onClick={() => setCurrent({ page: 1, reading: true })}
          >
            Jump
          </button>
        </>
      )
    }

    renderWithProviders(<IgnoredMoveHarness />)
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })

    fireEvent.click(next)
    expect(onPageChange).toHaveBeenCalledWith(1)
    // The move was refused: the pager is still on page 0 and no read runs.
    assertAvailable(next)
    assertUnavailable(previous)

    // An external read that starts at the page the refused move named must
    // leave both directions unavailable, not revive the stale record.
    fireEvent.click(screen.getByRole('button', { name: 'Jump' }))
    assertUnavailable(next)
    assertUnavailable(previous)
  })

  it('forgets the direction record when the rows-per-page value changes, and keeps the selector focused', async () => {
    function RowsPerPageHarness() {
      const [current, setCurrent] = useState({
        page: 1,
        rowsPerPage: 2,
        reading: false,
      })
      return (
        <TablePager
          page={current.page}
          rowsPerPage={current.rowsPerPage}
          count={6}
          rowsPerPageOptions={[2, 3]}
          reading={current.reading}
          onPageChange={(page) =>
            setCurrent((previous) => ({ ...previous, page, reading: true }))
          }
          onRowsPerPageChange={(rowsPerPage) =>
            // The host resets the page, as most hosts do on a size change.
            setCurrent((previous) => ({
              ...previous,
              rowsPerPage,
              page: 0,
              reading: true,
            }))
          }
        />
      )
    }

    renderWithProviders(<RowsPerPageHarness />)
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: PREVIOUS_LABEL }))
    expect(
      screen.getByRole('button', { name: PREVIOUS_LABEL }),
    ).toHaveAttribute('aria-busy', 'true')

    const selector = screen.getByRole('combobox')
    await user.click(selector)
    await user.click(screen.getByRole('option', { name: '3' }))

    for (const name of [PREVIOUS_LABEL, NEXT_LABEL]) {
      const direction = screen.getByRole('button', { name })
      expect(direction).toHaveAttribute('aria-disabled', 'true')
      expect(direction).not.toHaveAttribute('aria-busy')
    }
    expect(selector).toHaveFocus()
  })

  it('marks previous unavailable on the first page while next still fires', () => {
    const utils = renderPager({ page: 0 })

    assertUnavailable(utils.previous)
    assertAvailable(utils.next)

    fireEvent.click(utils.previous)
    expect(utils.onPageChange).not.toHaveBeenCalled()

    fireEvent.click(utils.next)
    expect(utils.onPageChange).toHaveBeenCalledWith(1)
  })

  it('marks next unavailable on the last page of a known total', () => {
    const utils = renderPager({ page: 2, count: 5 })

    assertAvailable(utils.previous)
    assertUnavailable(utils.next)

    fireEvent.click(utils.next)
    expect(utils.onPageChange).not.toHaveBeenCalled()

    fireEvent.click(utils.previous)
    expect(utils.onPageChange).toHaveBeenCalledWith(1)
  })

  it('marks next unavailable when the unknown total has no next page', () => {
    const utils = renderPager({ count: -1, hasNext: false })

    assertUnavailable(utils.next)
    fireEvent.click(utils.next)
    expect(utils.onPageChange).not.toHaveBeenCalled()
  })

  it('leaves next available when the unknown total defaults to having one', () => {
    const utils = renderPager({ count: -1 })

    assertAvailable(utils.next)
    fireEvent.click(utils.next)
    expect(utils.onPageChange).toHaveBeenCalledWith(1)
  })

  it('natively disables the rows-per-page selector only while a write is in flight', () => {
    const { unmount } = renderPager({ unavailable: true })
    expect(screen.getByRole('combobox')).toHaveAttribute(
      'aria-disabled',
      'true',
    )
    expect(screen.getByRole('combobox')).not.toHaveAttribute('tabindex')
    unmount()

    renderPager({ reading: true })
    expect(screen.getByRole('combobox')).not.toHaveAttribute('aria-disabled')
    expect(screen.getByRole('combobox')).toHaveAttribute('tabindex', '0')
  })

  it('has no axe violations in the available and unavailable states', async () => {
    const { unmount } = renderPager()
    await expectNoAxeViolations()
    unmount()

    renderPager({ unavailable: true })
    await expectNoAxeViolations()
  })
})
