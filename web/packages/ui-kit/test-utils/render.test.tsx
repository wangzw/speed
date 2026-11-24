
import { useState } from 'react'
import { screen } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { useTranslation } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { beforeEach, describe, expect, it } from 'vitest'
import { createAppTheme } from '../src/theme/createAppTheme.js'
import { createUiKitI18n, renderWithProviders } from './render.js'

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

const themes: (Theme | undefined)[] = []

function ThemeProbe({ label }: { readonly label: string }) {
  themes.push(useTheme() as Theme | undefined)
  return <span data-testid="theme-probe">{label}</span>
}

const activeInstances: I18nInstance[] = []

function I18nProbe({ label }: { readonly label: string }) {
  activeInstances.push(useTranslation().i18n)
  return <span data-testid="i18n-probe">{label}</span>
}

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

    const global = createUiKitI18n('en-US')
    expect(global.language).toBe('en-US')

    rerender(<I18nProbe label="second" />)

    expect(screen.getByTestId('i18n-probe').textContent).toBe('second')
    expect(activeInstances.length).toBeGreaterThanOrEqual(2)
    const first = activeInstances[0]
    const last = activeInstances[activeInstances.length - 1]
    expect(first?.language).toBe('zh-CN')
    expect(last?.language).toBe('zh-CN')
    expect(last).toBe(first)
  })
})
