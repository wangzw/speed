/**
 * AppearanceSection behaviour: the controlled theme-preset choice.
 *
 * What this suite pins: every preset the host supplies renders as an
 * option and the value the host passes is the selected one; a choice
 * reaches the host through onChange with the chosen preset's id and the
 * section itself stays controlled (it holds no selection of its own); and
 * the section survives the two boundary-shaped inputs -- one preset and
 * no presets -- without collapsing its heading or its labelled control.
 * The axe scan runs with the select OPEN, because the open menu is the
 * state a keyboard or screen-reader user meets while choosing a theme.
 *
 * Preset labels here are host data (the component never translates them),
 * so the fixtures may carry English names; every string the section
 * renders itself is asserted from the zh-CN bundle.
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

    const select = presetSelect()
    // The closed control shows the current choice, not an empty value.
    expect(select).toHaveTextContent('Glass')

    fireEvent.mouseDown(select)

    // Every supplied choice is offered; the current one carries the
    // selected state and the others do not.
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

    // The open control is the state in which the choice is made: scan
    // the document with the menu showing.
    await expectNoAxeViolations()
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

    // The section did not move by itself: the host owns the value.
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
    // No presets means no options -- but still a labelled, rendered control.
    expect(screen.queryAllByRole('option')).toHaveLength(0)
    expect(select).toHaveAttribute('aria-expanded', 'true')
  })
})
