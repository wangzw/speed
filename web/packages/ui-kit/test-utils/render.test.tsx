/**
 * The ui-kit render harness keeps the provider tree, and the unit inside
 * it, mounted across a rerender.
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
 * back a structurally similar theme. The i18n probe reads the instance
 * useTranslation resolves: the harness must keep handing the tree the
 * instance the test passed, even after a later createI18n call has moved
 * react-i18next's global default (initReactI18next's init sets it) --
 * exactly what a rerender that dropped the provider would expose.
 */

import { useState } from 'react'
import { screen } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { useTranslation } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { beforeEach, describe, expect, it } from 'vitest'
import { createAppTheme } from '../src/theme/createAppTheme.js'
import { createUiKitI18n, renderWithProviders } from './render.js'

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

/** Records the instance useTranslation resolves on every render. */
const activeInstances: I18nInstance[] = []

function I18nProbe({ label }: { readonly label: string }) {
  activeInstances.push(useTranslation().i18n)
  return <span data-testid="i18n-probe">{label}</span>
}

/** The monospace stack the factory fills; MUI's default theme has none. */
const SPEED_MONO = createAppTheme().theme.typography.fontFamilyMono

beforeEach(() => {
  mounts = 0
  themes.length = 0
  activeInstances.length = 0
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

  it('keeps the i18n instance a test passes across a rerender and a new global instance', () => {
    const passed = createUiKitI18n('zh-CN')
    const { rerender } = renderWithProviders(<I18nProbe label="first" />, {
      i18n: passed,
    })

    // Creating another instance installs it as react-i18next's global
    // default (initReactI18next's init sets it), the way a later harness
    // call in the same file would. It speaks en-US, so a tree that fell
    // back to the global reads a different language.
    const global = createUiKitI18n('en-US')
    expect(global.language).toBe('en-US')

    rerender(<I18nProbe label="second" />)

    expect(screen.getByTestId('i18n-probe').textContent).toBe('second')
    expect(activeInstances.length).toBeGreaterThanOrEqual(2)
    const first = activeInstances[0]
    const last = activeInstances[activeInstances.length - 1]
    // useTranslation hands out a per-instance wrapper object rather than
    // the raw instance, so match the resolved language instead of object
    // identity: the passed instance speaks zh-CN, the global that replaced
    // it speaks en-US.
    expect(first?.language).toBe('zh-CN')
    expect(last?.language).toBe('zh-CN')
    // One wrapper across the rerender means the tree stayed on the passed
    // instance instead of resolving the new global on a fresh mount.
    expect(last).toBe(first)
  })
})
