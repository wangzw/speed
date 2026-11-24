/**
 * AppearanceSection checks the controlled theme-preset choice.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  AppearanceSection,
  type ThemePresetChoice,
} from './AppearanceSection.js'

const presets: readonly ThemePresetChoice[] = [
  { id: 'default', label: 'Default' },
  { id: 'classic', label: 'Classic' },
  { id: 'glass', label: 'Glass' },
]

function presetSelect(): HTMLElement {
  return screen.getByRole('combobox', {
    name: zhCN.appearance.theme.label,
  })
}

describe('AppearanceSection', () => {
  it('lists every supplied preset with the current value selected, and passes axe while open', async () => {
    renderWithProviders(
      <AppearanceSection
        presets={presets}
        value="glass"
        onChange={() => {}}
      />,
    )

    expect(
      screen.getByRole('heading', { name: zhCN.appearance.title }),
    ).toBeTruthy()
    expect(screen.getByText(zhCN.appearance.description)).toBeTruthy()
    expect(presetSelect()).toHaveAccessibleDescription(
      zhCN.appearance.theme.helper,
    )

    const select = presetSelect()
    expect(select).toHaveTextContent('Glass')

    fireEvent.mouseDown(select)

    for (const preset of presets) {
      expect(screen.getByRole('option', { name: preset.label })).toBeTruthy()
    }
    expect(screen.getByRole('option', { name: 'Glass' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    expect(screen.getByRole('option', { name: 'Classic' })).toHaveAttribute(
      'aria-selected',
      'false',
    )

    await expectNoAxeViolations()
  })

  it('drops the visible label under hideLabel while the control keeps its accessible name', () => {
    renderWithProviders(
      <AppearanceSection
        presets={presets}
        value="glass"
        onChange={() => {}}
        hideLabel
      />,
    )

    expect(screen.queryByText(zhCN.appearance.theme.label)).toBeNull()
    const select = presetSelect()
    expect(select).toHaveTextContent('Glass')
    expect(
      screen.getByRole('heading', { name: zhCN.appearance.title }),
    ).toBeTruthy()
  })

  it('reports the chosen preset id through onChange and holds no selection of its own', () => {
    const onChange = vi.fn()
    renderWithProviders(
      <AppearanceSection
        presets={presets}
        value="default"
        onChange={onChange}
      />,
    )

    fireEvent.mouseDown(presetSelect())
    fireEvent.click(screen.getByRole('option', { name: 'Classic' }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('classic')

    expect(presetSelect()).toHaveTextContent('Default')
  })

  it('renders a single-preset list as a normal section instead of collapsing', () => {
    renderWithProviders(
      <AppearanceSection
        presets={[{ id: 'default', label: 'Default' }]}
        value="default"
        onChange={() => {}}
      />,
    )

    expect(
      screen.getByRole('heading', { name: zhCN.appearance.title }),
    ).toBeTruthy()
    const select = presetSelect()
    expect(select).toHaveTextContent('Default')

    fireEvent.mouseDown(select)
    expect(screen.getAllByRole('option')).toHaveLength(1)
  })

  it('renders an empty preset list without collapsing the section or its control', () => {
    renderWithProviders(
      <AppearanceSection presets={[]} value="" onChange={() => {}} />,
    )

    expect(
      screen.getByRole('heading', { name: zhCN.appearance.title }),
    ).toBeTruthy()
    expect(screen.getByText(zhCN.appearance.description)).toBeTruthy()

    const select = presetSelect()
    fireEvent.mouseDown(select)
    expect(screen.queryAllByRole('option')).toHaveLength(0)
    expect(select).toHaveAttribute('aria-expanded', 'true')
  })
})
