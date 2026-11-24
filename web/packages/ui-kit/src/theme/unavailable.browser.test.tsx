
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import IconButton from '@mui/material/IconButton'
import Switch from '@mui/material/Switch'
import { createAppTheme } from './createAppTheme.js'
import { renderWithProviders } from '../../test-utils/render.js'

const TEXT_DISABLED = 'text disabled'
const TEXT_UNAVAILABLE = 'text unavailable'
const CONTAINED_DISABLED = 'contained disabled'
const CONTAINED_UNAVAILABLE = 'contained unavailable'
const OUTLINED_DISABLED = 'outlined disabled'
const OUTLINED_UNAVAILABLE = 'outlined unavailable'
const ICON_DISABLED = 'icon disabled'
const ICON_UNAVAILABLE = 'icon unavailable'
const CHECKBOX_DISABLED = 'checkbox disabled'
const CHECKBOX_UNAVAILABLE = 'checkbox unavailable'
const SWITCH_DISABLED = 'switch disabled'
const SWITCH_UNAVAILABLE = 'switch unavailable'
const CHECKED_SWITCH_DISABLED = 'checked switch disabled'
const CHECKED_SWITCH_UNAVAILABLE = 'checked switch unavailable'

const PRIMARY_MAIN = createAppTheme().theme.palette.primary.main

const BUTTON_FACE = [
  'color',
  'background-color',
  'border-top-color',
  'border-top-width',
  'border-top-style',
  'box-shadow',
  'cursor',
] as const

const ICON_FACE = ['color', 'background-color', 'cursor'] as const
const CHECKBOX_FACE = ['color', 'background-color', 'cursor'] as const
const THUMB_FACE = ['color', 'background-color'] as const

function computedFace(
  element: Element,
  properties: readonly string[],
): Record<string, string> {
  const style = getComputedStyle(element)
  return Object.fromEntries(
    properties.map((property) => [property, style.getPropertyValue(property)]),
  )
}

function switchParts(name: string) {
  const input = screen.getByRole('switch', { name })
  const root = input.closest('.MuiSwitch-root')
  if (root === null) throw new Error('the switch root is missing')
  return {
    root,
    thumb: root.querySelector('.MuiSwitch-thumb') as Element,
    track: root.querySelector('.MuiSwitch-track') as Element,
    switchBase: root.querySelector('.MuiSwitch-switchBase') as Element,
  }
}

function checkboxRoot(name: string): Element {
  const input = screen.getByRole('checkbox', { name })
  const root = input.closest('.MuiCheckbox-root')
  if (root === null) throw new Error('the checkbox root is missing')
  return root
}

function renderFixture() {
  renderWithProviders(
    <>
      <span
        data-testid="primary-probe"
        style={{ color: PRIMARY_MAIN }}
      />
      <Button disabled>{TEXT_DISABLED}</Button>
      <Button aria-disabled="true">{TEXT_UNAVAILABLE}</Button>
      <Button variant="contained" disabled>
        {CONTAINED_DISABLED}
      </Button>
      <Button variant="contained" aria-disabled="true">
        {CONTAINED_UNAVAILABLE}
      </Button>
      <Button variant="outlined" disabled>
        {OUTLINED_DISABLED}
      </Button>
      <Button variant="outlined" aria-disabled="true">
        {OUTLINED_UNAVAILABLE}
      </Button>
      <IconButton disabled aria-label={ICON_DISABLED}>
        <svg aria-hidden="true" />
      </IconButton>
      <IconButton aria-disabled="true" aria-label={ICON_UNAVAILABLE}>
        <svg aria-hidden="true" />
      </IconButton>
      <Checkbox
        disabled
        slotProps={{ input: { 'aria-label': CHECKBOX_DISABLED } }}
      />
      <Checkbox
        slotProps={{
          input: {
            'aria-label': CHECKBOX_UNAVAILABLE,
            'aria-disabled': true,
          },
        }}
      />
      <Switch
        disabled
        slotProps={{ input: { 'aria-label': SWITCH_DISABLED } }}
      />
      <Switch
        slotProps={{
          input: {
            'aria-label': SWITCH_UNAVAILABLE,
            'aria-disabled': true,
          },
        }}
      />
      <Switch
        disabled
        defaultChecked
        slotProps={{ input: { 'aria-label': CHECKED_SWITCH_DISABLED } }}
      />
      <Switch
        defaultChecked
        slotProps={{
          input: {
            'aria-label': CHECKED_SWITCH_UNAVAILABLE,
            'aria-disabled': true,
          },
        }}
      />
    </>,
    { language: 'en-US' },
  )
}

function button(name: string): HTMLElement {
  return screen.getByRole('button', { name })
}

describe('the unavailable appearance in Chromium', () => {
  it('computes the same face as the natively disabled control', () => {
    renderFixture()

    for (const [disabledName, unavailableName] of [
      [TEXT_DISABLED, TEXT_UNAVAILABLE],
      [CONTAINED_DISABLED, CONTAINED_UNAVAILABLE],
      [OUTLINED_DISABLED, OUTLINED_UNAVAILABLE],
    ] as const) {
      expect(
        computedFace(button(unavailableName), BUTTON_FACE),
        `the ${unavailableName} face differs from its disabled twin`,
      ).toEqual(computedFace(button(disabledName), BUTTON_FACE))
    }

    expect(computedFace(button(ICON_UNAVAILABLE), ICON_FACE)).toEqual(
      computedFace(button(ICON_DISABLED), ICON_FACE),
    )

    expect(computedFace(checkboxRoot(CHECKBOX_UNAVAILABLE), CHECKBOX_FACE)).toEqual(
      computedFace(checkboxRoot(CHECKBOX_DISABLED), CHECKBOX_FACE),
    )

    const disabledSwitch = switchParts(SWITCH_DISABLED)
    const unavailableSwitch = switchParts(SWITCH_UNAVAILABLE)
    expect(computedFace(unavailableSwitch.thumb, THUMB_FACE)).toEqual(
      computedFace(disabledSwitch.thumb, THUMB_FACE),
    )
    expect(computedFace(unavailableSwitch.track, ['opacity'])).toEqual(
      computedFace(disabledSwitch.track, ['opacity']),
    )
    expect(computedFace(unavailableSwitch.switchBase, ['cursor'])).toEqual(
      computedFace(disabledSwitch.switchBase, ['cursor']),
    )

    const disabledChecked = switchParts(CHECKED_SWITCH_DISABLED)
    const unavailableChecked = switchParts(CHECKED_SWITCH_UNAVAILABLE)
    expect(computedFace(unavailableChecked.thumb, THUMB_FACE)).toEqual(
      computedFace(disabledChecked.thumb, THUMB_FACE),
    )
  })

  it('keeps pointer events and draws the focus ring for keyboard focus', async () => {
    renderWithProviders(
      <Button aria-disabled="true">{TEXT_UNAVAILABLE}</Button>,
      { language: 'en-US' },
    )
    const unavailable = button(TEXT_UNAVAILABLE)
    expect(getComputedStyle(unavailable).pointerEvents).not.toBe('none')

    await userEvent.tab()
    expect(document.activeElement).toBe(unavailable)

    const style = getComputedStyle(unavailable)
    const probe = document.createElement('span')
    probe.style.color = PRIMARY_MAIN
    document.body.append(probe)
    const primaryColor = getComputedStyle(probe).color
    probe.remove()

    expect(style.outlineStyle).toBe('solid')
    expect(style.outlineWidth).toBe('2px')
    expect(style.outlineColor).toBe(primaryColor)
    expect(style.outlineOffset).toBe('2px')
  })

  it('paints no hover fill on an unavailable control', async () => {
    renderFixture()

    const SETTLED_MS = 400
    for (const name of [
      TEXT_UNAVAILABLE,
      CONTAINED_UNAVAILABLE,
      ICON_UNAVAILABLE,
    ]) {
      const element = button(name)
      const before = getComputedStyle(element).backgroundColor
      await userEvent.hover(element)
      await new Promise((resolve) => setTimeout(resolve, SETTLED_MS))
      expect(
        getComputedStyle(element).backgroundColor,
        `hovering ${name} painted a fill`,
      ).toBe(before)
      expect(getComputedStyle(element).cursor).toBe('default')
    }
  })
})
