import { useState } from 'react'
import { screen } from '@testing-library/react'
import { useQueryClient } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { beforeEach, describe, expect, it } from 'vitest'
import { createAppTheme } from '@speed/ui-kit'
import { renderWithProviders } from './render.js'

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
