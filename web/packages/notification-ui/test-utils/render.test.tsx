/**
 * The notification-ui render harness keeps the provider tree, and the
 * unit inside it, mounted across a rerender.
 *
 * A rerender that replaces the root element unmounts the whole tree and
 * mounts a fresh one: the focused node is detached, its instance state is
 * gone, and the providers are rebuilt. These cases pin the harness to the
 * opposite behaviour -- RTL re-wraps the rerendered element in the same
 * `wrapper`, so React reconciles the unit in place. They guard the harness
 * itself, not any product unit.
 *
 * The theme probe reads a field only the speed theme carries
 * (`typography.fontFamilyMono`, added by createAppTheme and absent from
 * MUI's default theme), so a lost AppThemeProvider cannot pass by handing
 * back a structurally similar theme. The same probe records the query
 * client the tree renders with: a lost QueryClientProvider makes
 * useQueryClient throw, and catching that lets the probe record the
 * absence as data, so the failure stays a clean assertion.
 */

import { useState } from 'react'
import { screen } from '@testing-library/react'
import { useQueryClient } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { beforeEach, describe, expect, it } from 'vitest'
import { createAppTheme } from '@speed/ui-kit'
import { renderWithProviders } from './render.js'

/** Counts every mount of FocusProbe; a rerender that remounts bumps it. */
let mounts = 0

function FocusProbe({ label }: { readonly label: string }) {
  const [mountCount] = useState(() => {
    mounts += 1
    return mounts
  })
  return (
    <button
      type="button"
      data-testid="focus-probe"
      data-mount-count={mountCount}
    >
      {label}
    </button>
  )
}

type Snapshot = {
  readonly theme: Theme | undefined
  readonly queryClient: QueryClient | null
}

/** One snapshot per render, so the test compares the first with the last. */
const snapshots: Snapshot[] = []

function ThemeProbe({ label }: { readonly label: string }) {
  let queryClient: QueryClient | null = null
  try {
    queryClient = useQueryClient()
  } catch {
    queryClient = null
  }
  snapshots.push({ theme: useTheme() as Theme | undefined, queryClient })
  return <span data-testid="theme-probe">{label}</span>
}

/** The monospace stack the factory fills; MUI's default theme has none. */
const SPEED_MONO = createAppTheme().theme.typography.fontFamilyMono

beforeEach(() => {
  mounts = 0
  snapshots.length = 0
})

describe('renderWithProviders', () => {
  it('keeps the focused node and the mounted instance across a rerender', () => {
    const { rerender } = renderWithProviders(<FocusProbe label="first" />)
    const button = screen.getByTestId('focus-probe')
    button.focus()
    expect(document.activeElement).toBe(button)

    rerender(<FocusProbe label="second" />)

    const updated = screen.getByTestId('focus-probe')
    expect(updated.textContent).toBe('second')
    expect(updated).toBe(button)
    expect(document.activeElement).toBe(button)
    expect(updated.getAttribute('data-mount-count')).toBe('1')
    expect(mounts).toBe(1)
  })

  it('keeps the speed theme and the query client mounted across a rerender', () => {
    const { rerender, queryClient } = renderWithProviders(
      <ThemeProbe label="first" />,
    )
    rerender(<ThemeProbe label="second" />)

    expect(screen.getByTestId('theme-probe').textContent).toBe('second')
    expect(snapshots.length).toBeGreaterThanOrEqual(2)
    const first = snapshots[0]
    const last = snapshots[snapshots.length - 1]
    expect(first?.queryClient).toBe(queryClient)
    expect(last?.queryClient).toBe(queryClient)
    expect(first?.theme?.typography.fontFamilyMono).toBe(SPEED_MONO)
    expect(last?.theme).toBe(first?.theme)
  })
})
