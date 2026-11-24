
import { describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import IconButton from '@mui/material/IconButton'
import ListItemButton from '@mui/material/ListItemButton'
import Switch from '@mui/material/Switch'
import { lighten } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import { mergeThemeComponents } from './surface.js'
import { touchFloorComponents } from './touch-floor.js'
import { unavailableComponents } from './unavailable.js'
import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'

interface FlatRule {
  readonly selector: string
  readonly declarations: string
}

function flatRules(css: string): FlatRule[] {
  const rules: FlatRule[] = []
  let cursor = 0
  while (cursor < css.length) {
    const open = css.indexOf('{', cursor)
    if (open < 0) break
    const selector = css.slice(cursor, open).trim()
    let depth = 0
    let end = open
    for (; end < css.length; end += 1) {
      if (css[end] === '{') depth += 1
      else if (css[end] === '}') {
        depth -= 1
        if (depth === 0) break
      }
    }
    rules.push({ selector, declarations: css.slice(open + 1, end) })
    cursor = end + 1
  }
  return rules
}

function declarationsOf(rule: FlatRule): string {
  return rule.declarations.replace(/\s+/g, '')
}

function declares(rule: FlatRule, property: string, value: string): boolean {
  return declarationsOf(rule).includes(
    `${property}:${value}`.replace(/\s+/g, ''),
  )
}

function unavailableRules(): FlatRule[] {
  return flatRules(emittedStyleText()).filter((rule) =>
    rule.selector.includes('[aria-disabled="true"]'),
  )
}

function rulesForClass(
  element: Element,
  rules: readonly FlatRule[],
): FlatRule[] {
  return rules.filter((rule) =>
    Array.from(element.classList).some((name) =>
      rule.selector.includes(`.${name}`),
    ),
  )
}

function rulesMatching(
  element: Element,
  rules: readonly FlatRule[],
): FlatRule[] {
  return rules.filter((rule) => {
    try {
      return element.matches(rule.selector)
    } catch {
      return false
    }
  })
}

function renderTheme(theme: Theme, node: ReactNode) {
  return render(<ThemeProvider theme={theme}>{node}</ThemeProvider>)
}

describe('theme unavailable appearance', () => {
  it('paints the disabled face on buttons whose root carries the attribute', () => {
    const { theme } = createAppTheme()
    renderTheme(
      theme,
      <>
        <Button aria-disabled="true">unavailable text</Button>
        <Button variant="contained" aria-disabled="true">
          unavailable contained
        </Button>
        <Button variant="outlined" aria-disabled="true">
          unavailable outlined
        </Button>
        <IconButton aria-label="unavailable icon" aria-disabled="true">
          <svg aria-hidden="true" />
        </IconButton>
      </>,
    )
    const rules = unavailableRules()
    expect(rules.length).toBeGreaterThan(0)

    const { action } = theme.palette
    const text = screen.getByRole('button', { name: 'unavailable text' })
    const contained = screen.getByRole('button', {
      name: 'unavailable contained',
    })
    const outlined = screen.getByRole('button', {
      name: 'unavailable outlined',
    })
    const icon = screen.getByRole('button', { name: 'unavailable icon' })

    const textRules = rulesMatching(text, rules)
    expect(
      textRules.some((rule) => declares(rule, 'color', action.disabled)),
    ).toBe(true)
    expect(textRules.some((rule) => declares(rule, 'cursor', 'default'))).toBe(
      true,
    )
    expect(
      rulesForClass(text, rules).some(
        (rule) =>
          rule.selector.includes(':hover') &&
          declares(rule, 'background-color', 'transparent'),
      ),
    ).toBe(true)

    const containedRules = rulesMatching(contained, rules)
    expect(
      containedRules.some((rule) =>
        declares(rule, 'background-color', action.disabledBackground),
      ),
    ).toBe(true)
    expect(
      containedRules.some((rule) =>
        declares(rule, 'box-shadow', theme.shadows[0] as string),
      ),
    ).toBe(true)
    expect(
      containedRules.some((rule) => declares(rule, 'color', action.disabled)),
    ).toBe(true)

    const outlinedRules = rulesMatching(outlined, rules)
    expect(
      outlinedRules.some((rule) =>
        declares(
          rule,
          'border',
          `1px solid ${action.disabledBackground}`,
        ),
      ),
    ).toBe(true)

    const iconRules = rulesMatching(icon, rules)
    expect(
      iconRules.some((rule) => declares(rule, 'color', action.disabled)),
    ).toBe(true)
    expect(
      iconRules.some((rule) =>
        declares(rule, 'background-color', 'transparent'),
      ),
    ).toBe(true)
    expect(iconRules.some((rule) => declares(rule, 'cursor', 'default'))).toBe(
      true,
    )

    for (const rule of rules) {
      expect(declarationsOf(rule)).not.toContain('pointer-events')
      expect(declarationsOf(rule)).not.toContain('outline')
    }
  })

  it('paints the disabled face on an unavailable ListItemButton', () => {
    const { theme } = createAppTheme()
    renderTheme(
      theme,
      <>
        <ListItemButton aria-disabled="true">unavailable item</ListItemButton>
        <ListItemButton selected aria-disabled="true">
          unavailable selected item
        </ListItemButton>
      </>,
    )
    const rules = unavailableRules()
    const { action } = theme.palette
    const item = screen.getByRole('button', { name: 'unavailable item' })

    const itemRules = rulesMatching(item, rules)
    expect(
      itemRules.some((rule) => declares(rule, 'color', action.disabled)),
    ).toBe(true)
    expect(itemRules.some((rule) => declares(rule, 'cursor', 'default'))).toBe(
      true,
    )
    expect(
      rulesForClass(item, rules).some(
        (rule) =>
          rule.selector.includes(':hover') &&
          declares(rule, 'background-color', 'transparent'),
      ),
    ).toBe(true)

    const selected = screen.getByRole('button', {
      name: 'unavailable selected item',
    })
    const selectedRules = rulesForClass(selected, rules)
    expect(
      selectedRules.some(
        (rule) =>
          rule.selector.includes('.Mui-selected') &&
          declares(rule, 'background-color', 'transparent'),
      ),
    ).toBe(true)
  })

  it('paints the disabled tones on a switch and checkbox whose input carries the attribute', () => {
    const { theme } = createAppTheme()
    renderTheme(
      theme,
      <>
        <Checkbox
          slotProps={{
            input: {
              'aria-label': 'unavailable checkbox',
              'aria-disabled': true,
            },
          }}
        />
        <Switch
          slotProps={{
            input: {
              'aria-label': 'unavailable switch',
              'aria-disabled': true,
            },
          }}
        />
        <Switch
          defaultChecked
          slotProps={{
            input: {
              'aria-label': 'unavailable checked switch',
              'aria-disabled': true,
            },
          }}
        />
      </>,
    )
    const rules = unavailableRules()
    const { action, grey } = theme.palette

    const checkbox = screen.getByRole('checkbox', {
      name: 'unavailable checkbox',
    })
    const checkboxRoot = checkbox.closest('.MuiCheckbox-root')
    expect(checkboxRoot).not.toBeNull()
    const checkboxRules = rulesMatching(checkboxRoot as Element, rules)
    expect(
      checkboxRules.some((rule) => declares(rule, 'color', action.disabled)),
    ).toBe(true)
    expect(
      checkboxRules.some((rule) => declares(rule, 'cursor', 'default')),
    ).toBe(true)
    expect(
      rulesForClass(checkboxRoot as Element, rules).some(
        (rule) =>
          rule.selector.includes(':hover') &&
          declares(rule, 'background-color', 'transparent'),
      ),
    ).toBe(true)

    const switchInput = screen.getByRole('switch', {
      name: 'unavailable switch',
    })
    const switchRoot = switchInput.closest('.MuiSwitch-root')
    expect(switchRoot).not.toBeNull()
    const thumb = (switchRoot as Element).querySelector('.MuiSwitch-thumb')
    const track = (switchRoot as Element).querySelector('.MuiSwitch-track')
    const switchBase = (switchRoot as Element).querySelector(
      '.MuiSwitch-switchBase',
    )
    expect(thumb).not.toBeNull()
    expect(track).not.toBeNull()
    expect(switchBase).not.toBeNull()

    expect(
      rulesMatching(thumb as Element, rules).some((rule) =>
        declares(rule, 'color', grey[100]),
      ),
    ).toBe(true)
    expect(
      rulesMatching(track as Element, rules).some((rule) =>
        declares(rule, 'opacity', '0.12'),
      ),
    ).toBe(true)
    expect(
      declares(
        rulesForClass(switchBase as Element, rules).find((rule) =>
          rule.selector.includes(':hover'),
        ) ?? { selector: '', declarations: '' },
        'background-color',
        'transparent',
      ),
    ).toBe(true)

    const checkedInput = screen.getByRole('switch', {
      name: 'unavailable checked switch',
    })
    const checkedRoot = checkedInput.closest('.MuiSwitch-root')
    const checkedThumb = (checkedRoot as Element).querySelector(
      '.MuiSwitch-thumb',
    )
    expect(checkedThumb).not.toBeNull()
    expect(
      rulesMatching(checkedThumb as Element, rules).some((rule) =>
        declares(rule, 'color', lighten(theme.palette.primary.main, 0.62)),
      ),
    ).toBe(true)
  })

  it('reads the dark mode disabled tones from the dark end of the ramp', () => {
    const { theme } = createAppTheme(undefined, undefined, 'dark')
    renderTheme(
      theme,
      <>
        <Switch
          slotProps={{
            input: {
              'aria-label': 'unavailable switch',
              'aria-disabled': true,
            },
          }}
        />
      </>,
    )
    const rules = unavailableRules()
    const switchInput = screen.getByRole('switch', {
      name: 'unavailable switch',
    })
    const switchRoot = switchInput.closest('.MuiSwitch-root')
    const thumb = (switchRoot as Element).querySelector('.MuiSwitch-thumb')
    const track = (switchRoot as Element).querySelector('.MuiSwitch-track')

    expect(
      rulesMatching(thumb as Element, rules).some((rule) =>
        declares(rule, 'color', theme.palette.grey[600]),
      ),
    ).toBe(true)
    expect(
      rulesMatching(track as Element, rules).some((rule) =>
        declares(rule, 'opacity', '0.2'),
      ),
    ).toBe(true)
  })

  it('keeps the touch-target floor composed at the same component slots', () => {
    const merged = mergeThemeComponents(
      touchFloorComponents,
      unavailableComponents,
    )
    const { theme } = createAppTheme()
    for (const component of [
      'MuiButton',
      'MuiIconButton',
      'MuiListItemButton',
      'MuiCheckbox',
      'MuiSwitch',
    ] as const) {
      const root = merged?.[component]?.styleOverrides?.root as
        | ((props: { theme: Theme }) => unknown[])
        | undefined
      expect(typeof root).toBe('function')
      const contributions = root?.({ theme })
      expect(contributions).toHaveLength(2)
      expect(contributions?.[0]).toMatchObject({
        [POINTER_COARSE_QUERY]: {
          minWidth: TOUCH_TARGET_MIN_PX,
          minHeight: TOUCH_TARGET_MIN_PX,
        },
      })
    }
  })

  it('keeps the focus-visible ring rule beside the unavailable rules', () => {
    const { theme } = createAppTheme()
    renderTheme(
      theme,
      <Button aria-disabled="true">unavailable text</Button>,
    )
    const button = screen.getByRole('button', { name: 'unavailable text' })
    const focusRules = flatRules(emittedStyleText()).filter(
      (rule) =>
        rule.selector.includes('Mui-focusVisible') &&
        declares(rule, 'outline', `2px solid ${theme.palette.primary.main}`),
    )
    expect(
      focusRules.some((rule) =>
        Array.from(button.classList).some((name) =>
          rule.selector.includes(`.${name}`),
        ),
      ),
    ).toBe(true)
  })
})
