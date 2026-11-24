/**
 * TablePager in Chromium: the direction an operator activates keeps the
 * focus the activation gave it, through a running read, a settled read at
 * the list boundary, and a write that makes it unavailable.
 *
 * jsdom cannot tell a natively disabled button from an `aria-disabled` one,
 * so the Tab-order assertions are only meaningful here.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { zhCN as muiZhCN } from '@mui/material/locale'
import { renderWithProviders } from '../../test-utils/render.js'
import { TablePager } from './TablePager.js'

const MUI_DEFAULTS = muiZhCN.components?.MuiTablePagination?.defaultProps
const NEXT_LABEL = MUI_DEFAULTS?.getItemAriaLabel?.('next') ?? ''
const PREVIOUS_LABEL = MUI_DEFAULTS?.getItemAriaLabel?.('previous') ?? ''
const AFTER_LABEL = 'After the pager'

describe('TablePager focus in Chromium', () => {
  it('keeps focus on the direction that started the page read, and Tab order still leaves it', async () => {
    await page.viewport(900, 700)
    const user = userEvent.setup()

    function Harness() {
      const [current, setCurrent] = useState({ page: 0, reading: false })
      return (
        <>
          <TablePager
            page={current.page}
            rowsPerPage={2}
            count={5}
            rowsPerPageOptions={[2, 5]}
            reading={current.reading}
            onPageChange={(next) => setCurrent({ page: next, reading: true })}
            onRowsPerPageChange={() => undefined}
          />
          <button type="button">{AFTER_LABEL}</button>
        </>
      )
    }

    renderWithProviders(<Harness />)
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })
    const next = screen.getByRole('button', { name: NEXT_LABEL })

    await user.click(next)

    expect(document.activeElement).toBe(next)
    expect(next).toHaveAttribute('aria-busy', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).not.toBeDisabled()

    await user.tab()
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: AFTER_LABEL }),
    )

    await user.tab({ shift: true })
    expect(document.activeElement).toBe(next)

    await user.tab({ shift: true })
    expect(document.activeElement).toBe(previous)
  })

  it('keeps focus on the direction when the read settles at the last page', async () => {
    await page.viewport(900, 700)
    const user = userEvent.setup()

    function Harness() {
      const [current, setCurrent] = useState({ page: 0, reading: false })
      return (
        <>
          <TablePager
            page={current.page}
            rowsPerPage={2}
            count={4}
            rowsPerPageOptions={[2, 5]}
            reading={current.reading}
            onPageChange={(next) => setCurrent({ page: next, reading: true })}
            onRowsPerPageChange={() => undefined}
          />
          <button
            type="button"
            onClick={() =>
              setCurrent((previous) => ({ ...previous, reading: false }))
            }
          >
            Settle the read
          </button>
        </>
      )
    }

    renderWithProviders(<Harness />)
    const next = screen.getByRole('button', { name: NEXT_LABEL })

    await user.click(next)
    expect(document.activeElement).toBe(next)
    expect(next).toHaveAttribute('aria-busy', 'true')

    fireEvent.click(screen.getByRole('button', { name: 'Settle the read' }))

    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(next).not.toHaveAttribute('aria-busy')
    expect(document.activeElement).toBe(next)
    expect(next).not.toBeDisabled()
  })

  it('keeps focus on the direction that alone turns unavailable, where MUI would move it', async () => {
    await page.viewport(900, 700)

    function Harness() {
      const [count, setCount] = useState(5)
      return (
        <>
          <TablePager
            page={1}
            rowsPerPage={2}
            count={count}
            rowsPerPageOptions={[2, 5]}
            onPageChange={() => undefined}
            onRowsPerPageChange={() => undefined}
          />
          <button type="button" onClick={() => setCount(4)}>
            Shrink the list
          </button>
        </>
      )
    }

    renderWithProviders(<Harness />)
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const previous = screen.getByRole('button', { name: PREVIOUS_LABEL })

    next.focus()
    expect(document.activeElement).toBe(next)
    expect(next).not.toHaveAttribute('aria-disabled')
    expect(previous).not.toHaveAttribute('aria-disabled')

    fireEvent.click(screen.getByRole('button', { name: 'Shrink the list' }))

    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(previous).not.toHaveAttribute('aria-disabled')
    expect(next).not.toBeDisabled()
    expect(document.activeElement).toBe(next)
  })

  it('keeps focus on the focused direction when it turns unavailable and available again', async () => {
    await page.viewport(900, 700)

    function Harness() {
      const [unavailable, setUnavailable] = useState(false)
      return (
        <>
          <TablePager
            page={1}
            rowsPerPage={2}
            count={5}
            rowsPerPageOptions={[2, 5]}
            unavailable={unavailable}
            onPageChange={() => undefined}
            onRowsPerPageChange={() => undefined}
          />
          <button
            type="button"
            onClick={() => setUnavailable((value) => !value)}
          >
            Toggle the write
          </button>
        </>
      )
    }

    renderWithProviders(<Harness />)
    const next = screen.getByRole('button', { name: NEXT_LABEL })
    const toggle = screen.getByRole('button', { name: 'Toggle the write' })

    next.focus()
    expect(document.activeElement).toBe(next)

    fireEvent.click(toggle)
    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(document.activeElement).toBe(next)

    fireEvent.click(toggle)
    await waitFor(() =>
      expect(next).not.toHaveAttribute('aria-disabled'),
    )
    expect(document.activeElement).toBe(next)
  })
})
