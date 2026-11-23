/**
 * The layout-kit render harness keeps the provider tree, and the unit
 * inside it, mounted across a rerender.
 *
 * This harness already passes its provider tree as RTL's `wrapper`, so
 * these cases pass before the other packages' harnesses are fixed. They
 * stay as the guard: if this harness ever goes back to wrapping `ui` by
 * hand, a rerender replaces the root element, detaches the focused node
 * and remounts the unit, and these cases fail.
 *
 * The theme probe reads a field only the speed theme carries
 * (`typography.fontFamilyMono`, added by createAppTheme and absent from
 * MUI's default theme), so a lost AppThemeProvider cannot pass by handing
 * back a structurally similar theme.
 */

import { useState } from 'react'
import { screen } from '@testing-library/react'
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

/** One theme per render, so the test compares the first with the last. */
const themes: (Theme | undefined)[] = []

function ThemeProbe({ label }: { readonly label: string }) {
  themes.push(useTheme() as Theme | undefined)
  return <span data-testid="theme-probe">{label}</span>
}

/** The monospace stack the factory fills; MUI's default theme has none. */
const SPEED_MONO = createAppTheme().theme.typography.fontFamilyMono

beforeEach(() => {
  mounts = 0
  themes.length = 0
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

  it('keeps the speed theme mounted across a rerender', () => {
    const { rerender } = renderWithProviders(<ThemeProbe label="first" />)
    rerender(<ThemeProbe label="second" />)

    expect(screen.getByTestId('theme-probe').textContent).toBe('second')
    expect(themes.length).toBeGreaterThanOrEqual(2)
    const first = themes[0]
    const last = themes[themes.length - 1]
    expect(first?.typography.fontFamilyMono).toBe(SPEED_MONO)
    expect(last?.typography.fontFamilyMono).toBe(SPEED_MONO)
    expect(last).toBe(first)
  })
})
