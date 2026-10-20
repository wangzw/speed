/**
 * The theme-layer touch-target floor, sampled by control category.
 *
 * The layout design asks for every clickable target to reach 44x44 on a
 * coarse pointer -- a global rule, not a per-surface one -- so this suite
 * renders one real control per category (primary and secondary button,
 * icon button, checkbox, radio, switch, input, link, navigation item,
 * clickable chip) and proves each one carries the floor through the
 * theme alone, with no
 * per-control `touchTargetSx` in the fixture. jsdom evaluates no media
 * condition and no layout, so the proof reads the emitted CSS (see
 * @speed/test-utils/emitted-css); the fine-pointer half proves the
 * opposite direction -- none of the floor leaks into the styles a precise
 * pointer sees.
 */

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ThemeProvider, createTheme } from '@mui/material/styles'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Checkbox, { checkboxClasses } from '@mui/material/Checkbox'
import Radio, { radioClasses } from '@mui/material/Radio'
import Switch, { switchClasses } from '@mui/material/Switch'
import TextField from '@mui/material/TextField'
import Link from '@mui/material/Link'
import ListItemButton from '@mui/material/ListItemButton'
import Chip, { chipClasses } from '@mui/material/Chip'
import { inputBaseClasses } from '@mui/material/InputBase'
import {
  emittedStyleText,
  hasCoarsePointerTouchTarget,
  missingCoarsePointerTouchTargets,
  styleTextOutsideCoarsePointer,
} from '@speed/test-utils/emitted-css'
import { TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'
import { renderWithProviders } from '../../test-utils/render.js'

const FLOOR = `${TOUCH_TARGET_MIN_PX}px`

function Fixture() {
  return (
    <div>
      <Button variant="contained">primary button</Button>
      <Button variant="outlined">secondary button</Button>
      <IconButton aria-label="icon button">i</IconButton>
      <Checkbox slotProps={{ input: { 'aria-label': 'checkbox' } }} />
      <Radio slotProps={{ input: { 'aria-label': 'radio' } }} />
      <Switch slotProps={{ input: { 'aria-label': 'switch' } }} />
      <TextField label="input" />
      <Link href="#link">link</Link>
      <ListItemButton>navigation item</ListItemButton>
      <Chip label="clickable chip" onClick={() => undefined} />
      <Chip label="status chip" />
    </div>
  )
}

const inputRoot = (): Element | null =>
  screen
    .getByRole('textbox', { name: 'input' })
    .closest(`.${inputBaseClasses.root}`)

// A checkbox's (and radio's) clickable element is its root span; the
// inner input is the form control, not the hit area. The switch's root is
// the box that clips (and therefore bounds) its absolutely positioned
// button.
const checkboxRoot = (): Element =>
  screen
    .getByRole('checkbox', { name: 'checkbox' })
    .closest(`.${checkboxClasses.root}`) as Element
const radioRoot = (): Element =>
  screen
    .getByRole('radio', { name: 'radio' })
    .closest(`.${radioClasses.root}`) as Element
const switchRoot = (): Element =>
  screen
    .getByRole('switch', { name: 'switch' })
    .closest(`.${switchClasses.root}`) as Element

describe('theme touch-target floor', () => {
  it('raises every sampled control category to the 44px floor', () => {
    renderWithProviders(<Fixture />)
    const samples = [
      {
        category: 'primary button',
        element: screen.getByRole('button', { name: 'primary button' }),
      },
      {
        category: 'secondary button',
        element: screen.getByRole('button', { name: 'secondary button' }),
      },
      {
        category: 'icon button',
        element: screen.getByRole('button', { name: 'icon button' }),
      },
      { category: 'checkbox', element: checkboxRoot() },
      { category: 'radio', element: radioRoot() },
      { category: 'switch', element: switchRoot() },
      { category: 'input', element: inputRoot() },
      { category: 'link', element: screen.getByRole('link', { name: 'link' }) },
      {
        category: 'navigation item',
        element: screen.getByRole('button', { name: 'navigation item' }),
      },
      {
        category: 'clickable chip',
        element: screen.getByRole('button', { name: 'clickable chip' }),
      },
    ]
    expect(missingCoarsePointerTouchTargets(samples)).toEqual([])
  })

  it('carries the floor on the input element itself, not only on the field wrapper', () => {
    renderWithProviders(<Fixture />)
    const input = screen.getByRole('textbox', { name: 'input' })
    expect(hasCoarsePointerTouchTarget(input)).toBe(true)
    expect(hasCoarsePointerTouchTarget(inputRoot() as Element)).toBe(true)
  })

  it('grows the switch root but pins its track to the stock height', () => {
    renderWithProviders(<Fixture />)
    // The switch's button is clipped by its root, so the root has to grow
    // to the floor. Pinning the track height keeps the thicker 100%-height
    // track out of the coarse rendering: the target grows, the switch
    // drawing does not.
    const css = emittedStyleText()
    expect(css).toContain(`.${switchClasses.track}{height:14px`)
    expect(css).toContain(
      `.${switchClasses.sizeSmall} .${switchClasses.track}{height:10px`,
    )
  })

  it('leaves the status chip at its compact size: only a clickable chip is a target', () => {
    renderWithProviders(<Fixture />)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: 'clickable chip' }),
      ),
    ).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByText('status chip').closest(`.${chipClasses.root}`) as Element,
      ),
    ).toBe(false)
  })

  it('keeps a fine pointer untouched: the floor never leaves the media feature', () => {
    renderWithProviders(<Fixture />)
    // jsdom applies no media query, so the computed values here are what
    // a precise pointer sees. None of them is the touch floor.
    const samples = [
      screen.getByRole('button', { name: 'primary button' }),
      screen.getByRole('button', { name: 'secondary button' }),
      screen.getByRole('button', { name: 'icon button' }),
      checkboxRoot(),
      radioRoot(),
      switchRoot(),
      screen.getByRole('textbox', { name: 'input' }),
      inputRoot() as Element,
      screen.getByRole('link', { name: 'link' }),
      screen.getByRole('button', { name: 'navigation item' }),
      screen.getByRole('button', { name: 'clickable chip' }),
    ]
    for (const element of samples) {
      const style = getComputedStyle(element)
      expect(style.minWidth).not.toBe(FLOOR)
      expect(style.minHeight).not.toBe(FLOOR)
    }
    // The link's inline box is left alone outside the media feature.
    expect(getComputedStyle(screen.getByRole('link', { name: 'link' })).display).toBe(
      'inline',
    )
    // No 44px minimum survives once the coarse-pointer blocks are removed.
    const fine = styleTextOutsideCoarsePointer()
    expect(fine).not.toContain(`min-width:${FLOOR}`)
    expect(fine).not.toContain(`min-height:${FLOOR}`)
  })

  it('renders the same fine-pointer sizes as a stock MUI theme', () => {
    // Differential control: the floor's addition must not move any
    // computed minimum size or display value on a precise pointer, so the
    // same controls under MUI's stock theme measure identically.
    const sampled = (): Record<string, [string, string, string]> => {
      const entries: Array<[string, Element]> = [
        ['primary', screen.getByRole('button', { name: 'primary button' })],
        ['secondary', screen.getByRole('button', { name: 'secondary button' })],
        ['icon', screen.getByRole('button', { name: 'icon button' })],
        ['checkbox', checkboxRoot()],
        ['radio', radioRoot()],
        ['switch', switchRoot()],
        ['input', inputRoot() as Element],
        ['link', screen.getByRole('link', { name: 'link' })],
        ['nav', screen.getByRole('button', { name: 'navigation item' })],
        ['chip', screen.getByRole('button', { name: 'clickable chip' })],
      ]
      return Object.fromEntries(
        entries.map(([key, element]) => {
          const style = getComputedStyle(element)
          return [key, [style.minWidth, style.minHeight, style.display]]
        }),
      )
    }
    const appTheme = renderWithProviders(<Fixture />)
    const themed = sampled()
    appTheme.unmount()
    render(
      <ThemeProvider theme={createTheme()}>
        <Fixture />
      </ThemeProvider>,
    )
    expect(sampled()).toEqual(themed)
  })
})
