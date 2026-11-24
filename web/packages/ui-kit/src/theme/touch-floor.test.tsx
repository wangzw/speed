
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ThemeProvider, createTheme } from '@mui/material/styles'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Checkbox, { checkboxClasses } from '@mui/material/Checkbox'
import Radio, { radioClasses } from '@mui/material/Radio'
import Switch, { switchClasses } from '@mui/material/Switch'
import TextField from '@mui/material/TextField'
import Select from '@mui/material/Select'
import MenuItem from '@mui/material/MenuItem'
import Link from '@mui/material/Link'
import ListItemButton from '@mui/material/ListItemButton'
import Chip, { chipClasses } from '@mui/material/Chip'
import { inputBaseClasses } from '@mui/material/InputBase'
import { selectClasses } from '@mui/material/Select'
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
      <Select
        value="one"
        onChange={() => undefined}
        SelectDisplayProps={{ 'aria-label': 'select' }}
      >
        <MenuItem value="one">one</MenuItem>
      </Select>
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
const selectTrigger = (): Element =>
  screen
    .getByRole('combobox', { name: 'select' })
    .closest(`.${selectClasses.select}`) as Element

/** Coarse-pointer rules that name `slotClass` itself, not a generated class that merely contains it. */
function coarseRulesSelecting(slotClass: string): string[] {
  const rules: string[] = []
  for (const block of emittedStyleText()
    .split('@media (pointer: coarse)')
    .slice(1)) {
    for (const match of block.matchAll(/[^{}]+\{[^{}]*\}/g)) {
      if (match[0].includes(`${slotClass}{`)) rules.push(match[0])
    }
  }
  return rules
}

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
      { category: 'select trigger', element: selectTrigger() },
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

  it('carries the floor on the Select trigger div, not only its field wrapper', () => {
    renderWithProviders(<Fixture />)
    const trigger = selectTrigger()
    expect(trigger).toHaveClass(selectClasses.select)
    expect(trigger.tagName).toBe('DIV')
    const slotRules = coarseRulesSelecting(`.${selectClasses.select}`)
    expect(slotRules).not.toHaveLength(0)
    expect(
      slotRules.some(
        (rule) =>
          rule.includes('min-width:44px') &&
          rule.includes('min-height:44px'),
      ),
    ).toBe(true)
    expect(hasCoarsePointerTouchTarget(trigger)).toBe(true)
  })

  it('grows the switch root but pins its track to the stock height', () => {
    renderWithProviders(<Fixture />)
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
    const samples = [
      screen.getByRole('button', { name: 'primary button' }),
      screen.getByRole('button', { name: 'secondary button' }),
      screen.getByRole('button', { name: 'icon button' }),
      checkboxRoot(),
      radioRoot(),
      switchRoot(),
      screen.getByRole('textbox', { name: 'input' }),
      inputRoot() as Element,
      selectTrigger(),
      screen.getByRole('link', { name: 'link' }),
      screen.getByRole('button', { name: 'navigation item' }),
      screen.getByRole('button', { name: 'clickable chip' }),
    ]
    for (const element of samples) {
      const style = getComputedStyle(element)
      expect(style.minWidth).not.toBe(FLOOR)
      expect(style.minHeight).not.toBe(FLOOR)
    }
    expect(getComputedStyle(screen.getByRole('link', { name: 'link' })).display).toBe(
      'inline',
    )
    const fine = styleTextOutsideCoarsePointer()
    expect(fine).not.toContain(`min-width:${FLOOR}`)
    expect(fine).not.toContain(`min-height:${FLOOR}`)
  })

  it('renders the same fine-pointer sizes as a stock MUI theme', () => {
    const sampled = (): Record<string, [string, string, string]> => {
      const entries: Array<[string, Element]> = [
        ['primary', screen.getByRole('button', { name: 'primary button' })],
        ['secondary', screen.getByRole('button', { name: 'secondary button' })],
        ['icon', screen.getByRole('button', { name: 'icon button' })],
        ['checkbox', checkboxRoot()],
        ['radio', radioRoot()],
        ['switch', switchRoot()],
        ['input', inputRoot() as Element],
        ['select', selectTrigger()],
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
