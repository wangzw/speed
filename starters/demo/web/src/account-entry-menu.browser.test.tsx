import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { alpha } from '@mui/material/styles'
import axe from 'axe-core'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import type { AppShellNavItem } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { themePresets } from '@speed/theme-presets'
import { createAppTheme } from '@speed/ui-kit'
import { AppServicesProvider } from './app-services.js'
import { AccountEntry } from './chrome.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

const EXPECTED = {
  menuLeftInset: 9,
  avatarInset: 16,
  iconGlyphSize: 24,
  rowBottomGap: 12,
  dividerRowGap: 12,
} as const

const TOLERANCE = 1

const DARK_RULE_ALPHA = 0.16

const MIN_DARK_RULE_CONTRAST = 1.5

const COLOR_MODES = ['light', 'dark'] as const

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

function normalizedColor(value: string): string {
  const probe = document.createElement('div')
  probe.style.color = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
}

function normalizedBackground(value: string): string {
  const probe = document.createElement('div')
  probe.style.backgroundColor = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).backgroundColor
  probe.remove()
  return color
}

function normalizedBoxShadow(value: string): string {
  const probe = document.createElement('div')
  probe.style.boxShadow = value
  document.body.appendChild(probe)
  const shadow = getComputedStyle(probe).boxShadow
  probe.remove()
  return shadow
}

function alphaOf(color: string): number {
  const match = /rgba?\(([^)]+)\)/.exec(color)
  if (match === null) {
    return 1
  }
  const parts = (match[1] ?? '').split(',').map((part) => Number(part.trim()))
  return parts.length === 4 ? (parts[3] ?? 1) : 1
}

interface Rgba {
  readonly r: number
  readonly g: number
  readonly b: number
  readonly a: number
}

function parseColor(value: string): Rgba {
  const match = /rgba?\(([^)]+)\)/.exec(value)
  if (match === null) {
    throw new Error(`unparsed colour: ${value}`)
  }
  const parts = (match[1] ?? '').split(/[,\s/]+/).filter(Boolean).map(Number)
  const [r = 0, g = 0, b = 0, a = 1] = parts
  return { r, g, b, a }
}

function compositeOver(foreground: Rgba, background: Rgba): Rgba {
  const mix = (fg: number, bg: number) =>
    fg * foreground.a + bg * (1 - foreground.a)
  return {
    r: mix(foreground.r, background.r),
    g: mix(foreground.g, background.g),
    b: mix(foreground.b, background.b),
    a: 1,
  }
}

function linearChannel(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

function relativeLuminance(color: Rgba): number {
  return (
    0.2126 * linearChannel(color.r) +
    0.7152 * linearChannel(color.g) +
    0.0722 * linearChannel(color.b)
  )
}

function contrastRatio(first: Rgba, second: Rgba): number {
  const [lighter, darker] = [
    relativeLuminance(first),
    relativeLuminance(second),
  ].sort((left, right) => right - left) as [number, number]
  return (lighter + 0.05) / (darker + 0.05)
}

function paperBehind(element: Element, pageFill: string): Rgba {
  const layers: string[] = []
  let node: Element | null = element
  while (node !== null) {
    const background = getComputedStyle(node).backgroundColor
    if (alphaOf(background) > 0) {
      layers.push(background)
      if (alphaOf(background) >= 1) {
        break
      }
    }
    node = node.parentElement
  }
  let paper = parseColor(pageFill)
  for (const layer of layers.reverse()) {
    paper = compositeOver(parseColor(layer), paper)
  }
  return paper
}

function px(value: number): number {
  return Math.round(value * 100) / 100
}

function checkClose(
  issues: string[],
  label: string,
  actual: number,
  expected: number,
): void {
  if (Math.abs(actual - expected) > TOLERANCE) {
    issues.push(
      `${label}: expected ${expected}px ±${TOLERANCE}px, measured ${px(actual)}px`,
    )
  }
}

function menuItemLabelLeft(item: HTMLElement): number | undefined {
  for (const node of Array.from(item.childNodes)) {
    if (
      node.nodeType === Node.TEXT_NODE &&
      (node.textContent ?? '').trim() !== ''
    ) {
      const range = document.createRange()
      range.selectNodeContents(node)
      return range.getBoundingClientRect().left
    }
  }
  for (const child of Array.from(item.children).slice(1)) {
    if (child.getAttribute('aria-hidden') === 'true') {
      continue
    }
    if ((child.textContent ?? '').trim() === '') {
      continue
    }
    return child.getBoundingClientRect().left
  }
  return undefined
}

function checkDivider(
  issues: string[],
  divider: Element,
  label: string,
  dividerColor: string,
  pageFill: string,
  minimumContrast?: number,
): void {
  const style = getComputedStyle(divider)
  if (style.borderBottomStyle !== 'solid') {
    issues.push(`${label}: border-bottom-style is ${style.borderBottomStyle}`)
  }
  if (style.borderBottomWidth !== '1px') {
    issues.push(`${label}: border-bottom-width is ${style.borderBottomWidth}`)
  }
  if (style.borderBottomColor !== dividerColor) {
    issues.push(
      `${label}: border-bottom-color is ${style.borderBottomColor}, theme divider is ${dividerColor}`,
    )
  }
  if (minimumContrast !== undefined) {
    const paper = paperBehind(divider, pageFill)
    const drawn = compositeOver(parseColor(style.borderBottomColor), paper)
    const ratio = contrastRatio(drawn, paper)
    if (ratio < minimumContrast) {
      issues.push(
        `${label}: contrast over paper is ${px(ratio)}:1, expected at least ${minimumContrast}:1`,
      )
    }
  }
}

describe('the account menu across theme presets', () => {
  for (const preset of themePresets) {
    for (const mode of COLOR_MODES) {
      it(`places its dividers and geometry under ${preset.id} / ${mode}`, async () => {
        await page.viewport(1280, 900)
        const label = `${preset.id} / ${mode}`
        const theme = createAppTheme(preset.tokens, undefined, mode).theme
        const dividerColor = normalizedColor(
          mode === 'dark'
            ? alpha(theme.palette.text.primary, DARK_RULE_ALPHA)
            : theme.palette.divider,
        )
        const minimumContrast =
          mode === 'dark' ? MIN_DARK_RULE_CONTRAST : undefined
        const pageFill = normalizedBackground(theme.palette.background.default)
        const paperBorderColor = normalizedColor(theme.palette.divider)
        const issues: string[] = []

        renderDemoApp({
          session: createTestSession(authenticatedSnapshot()),
          theme: {
            presets: themePresets,
            defaultPreset: preset.id,
            defaultMode: mode,
            projectTokens: {},
          },
        })

        const trigger = await screen.findByTestId('account-entry-trigger')
        const column = trigger.closest('.MuiDrawer-paper') as HTMLElement | null
        if (column === null) {
          throw new Error(`${label}: the account entry is not in the drawer`)
        }
        const entry = screen.getByTestId('account-entry')
        const rowDivider = entry.firstElementChild as HTMLElement
        const avatar = trigger.querySelector('.MuiAvatar-root') as HTMLElement

        const columnRect = column.getBoundingClientRect()
        const columnContentLeft =
          columnRect.left +
          Number.parseFloat(getComputedStyle(column).borderLeftWidth || '0')
        const triggerRect = trigger.getBoundingClientRect()
        const rowDividerRect = rowDivider.getBoundingClientRect()
        const avatarRect = avatar.getBoundingClientRect()

        checkDivider(
          issues,
          rowDivider,
          `${label}: row divider`,
          dividerColor,
          pageFill,
          minimumContrast,
        )

        checkClose(
          issues,
          `${label}: avatar inset`,
          avatarRect.left - columnContentLeft,
          EXPECTED.avatarInset,
        )
        checkClose(
          issues,
          `${label}: row bottom gap`,
          columnRect.bottom - triggerRect.bottom,
          EXPECTED.rowBottomGap,
        )
        checkClose(
          issues,
          `${label}: divider-to-row gap`,
          triggerRect.top - rowDividerRect.bottom,
          EXPECTED.dividerRowGap,
        )

        const navigation = screen.getByRole('navigation')
        const navLink = navigation.querySelector('a')
        const navIconCell = navLink?.querySelector('.MuiListItemIcon-root')
        const navLabel = navLink?.querySelector('.MuiListItemText-root')
        const navIconCellRect =
          navIconCell instanceof HTMLElement
            ? navIconCell.getBoundingClientRect()
            : undefined
        const navLabelLeft =
          navLabel instanceof HTMLElement
            ? navLabel.getBoundingClientRect().left
            : undefined
        if (navIconCellRect === undefined || navLabelLeft === undefined) {
          issues.push(
            `${label}: the navigation entry has no icon cell or label`,
          )
        }
        if (navIconCell instanceof HTMLElement) {
          const navGlyph = navIconCell.querySelector('svg')
          if (navGlyph !== null) {
            checkClose(
              issues,
              `${label}: navigation glyph`,
              navGlyph.getBoundingClientRect().width,
              EXPECTED.iconGlyphSize,
            )
          }
        }

        await userEvent.setup().click(trigger)
        const menu = await screen.findByRole('menu')
        const paper = menu.parentElement as HTMLElement
        await waitFor(() => {
          const transform = getComputedStyle(paper).transform
          const settled =
            transform === 'none' ||
            /^matrix\(1, 0, 0, 1, 0, 0\)$/.test(transform)
          if (!settled) {
            throw new Error(`menu still transforming: ${transform}`)
          }
        })

        const paperRect = paper.getBoundingClientRect()
        const paperStyle = getComputedStyle(paper)

        const menuLeftInset = paperRect.left - columnRect.left
        if (Math.abs(menuLeftInset - EXPECTED.menuLeftInset) > TOLERANCE) {
          issues.push(
            `${label}: menu left edge: expected ${EXPECTED.menuLeftInset}px ±${TOLERANCE}px, measured ${px(menuLeftInset)}px (computed margin-left ${paperStyle.marginLeft})`,
          )
        }
        const themeMinWidth = Number.parseFloat(theme.spacing(30))
        const inlineWidth = paper.style.width
        const inlineMinWidth = paper.style.minWidth
        paper.style.width = 'max-content'
        paper.style.minWidth = '0px'
        const contentWidth = paper.getBoundingClientRect().width
        paper.style.width = inlineWidth
        paper.style.minWidth = inlineMinWidth
        checkClose(
          issues,
          `${label}: menu width`,
          paperRect.width,
          Math.max(themeMinWidth, contentWidth),
        )

        if (navIconCellRect !== undefined && navLabelLeft !== undefined) {
          const items = within(menu).getAllByRole('menuitem')
          for (const item of items) {
            const text = item.textContent ?? ''
            const iconBox = item.firstElementChild
            const glyph = iconBox?.querySelector('svg')
            if (!(iconBox instanceof HTMLElement) || glyph == null) {
              issues.push(
                `${label}: menu entry has no icon box or glyph (${text})`,
              )
              continue
            }
            const iconBoxRect = iconBox.getBoundingClientRect()
            checkClose(
              issues,
              `${label}: item icon cell left (${text})`,
              iconBoxRect.left,
              navIconCellRect.left,
            )
            checkClose(
              issues,
              `${label}: item icon cell width (${text})`,
              iconBoxRect.width,
              navIconCellRect.width,
            )
            checkClose(
              issues,
              `${label}: item glyph (${text})`,
              glyph.getBoundingClientRect().width,
              EXPECTED.iconGlyphSize,
            )
            const labelLeft = menuItemLabelLeft(item)
            if (labelLeft === undefined) {
              issues.push(`${label}: menu entry has no label (${text})`)
              continue
            }
            checkClose(
              issues,
              `${label}: item label left (${text})`,
              labelLeft,
              navLabelLeft,
            )
          }
        }

        const dividers = within(paper).getAllByRole('separator')
        if (dividers.length !== 3) {
          issues.push(
            `${label}: expected 3 menu dividers, found ${dividers.length}`,
          )
        }
        dividers.forEach((divider, index) => {
          checkDivider(
            issues,
            divider,
            `${label}: menu divider ${index + 1}`,
            dividerColor,
            pageFill,
            minimumContrast,
          )
        })

        if (paperStyle.borderTopStyle !== 'solid') {
          issues.push(
            `${label}: menu border style is ${paperStyle.borderTopStyle}`,
          )
        }
        if (paperStyle.borderTopWidth !== '1px') {
          issues.push(
            `${label}: menu border width is ${paperStyle.borderTopWidth}`,
          )
        }
        if (paperStyle.borderTopColor !== paperBorderColor) {
          issues.push(
            `${label}: menu border colour is ${paperStyle.borderTopColor}, theme divider is ${paperBorderColor}`,
          )
        }
        const themeShadow = normalizedBoxShadow(theme.shadows[12])
        if (paperStyle.boxShadow !== themeShadow) {
          issues.push(
            `${label}: menu box-shadow is ${paperStyle.boxShadow}, theme.shadows[12] is ${themeShadow}`,
          )
        }

        const overlayFill = normalizedBackground(theme.surface.overlay.fill)
        if (paperStyle.backgroundColor !== overlayFill) {
          issues.push(
            `${label}: menu background is ${paperStyle.backgroundColor}, theme overlay fill is ${overlayFill}`,
          )
        }
        if (preset.id === 'glass') {
          const plainPaper = normalizedBackground(theme.palette.background.paper)
          if (alphaOf(paperStyle.backgroundColor) >= 1) {
            issues.push(
              `${label}: glass menu background is opaque (${paperStyle.backgroundColor})`,
            )
          }
          if (paperStyle.backgroundColor === plainPaper) {
            issues.push(
              `${label}: glass menu background collapsed to the plain paper colour (${plainPaper})`,
            )
          }
          if (paperStyle.backdropFilter === 'none') {
            issues.push(`${label}: glass menu has no backdrop filter`)
          }
        }

        expect(issues, `${label}: account menu contract`).toEqual([])
      })
    }
  }
})

describe("the account trigger's Label in Name", () => {
  it('keeps the visible initials and name inside its accessible name', async () => {
    await page.viewport(1280, 900)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const trigger = await screen.findByTestId('account-entry-trigger')
    await document.fonts.ready

    const result = await axe.run(trigger, {
      runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
    })
    const findings = [...result.violations, ...result.incomplete].flatMap(
      (rule) => rule.nodes.map((node) => `${rule.id}: ${node.html}`),
    )
    expect(findings).toEqual([])
    expect(
      result.passes.reduce((total, rule) => total + rule.nodes.length, 0),
    ).toBeGreaterThan(0)
  })
})

const ORGANIZATION_ITEM: AppShellNavItem = {
  id: '/organization/admin/invitations',
  label: 'Invitations',
}

function renderAccountEntry(): void {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
  })
  const queryClient = createQueryClient()
  const session = createTestSession(authenticatedSnapshot())
  attachSession(session)
  const services: SpeedAppServices = {
    session,
    api: createHarnessClient(),
    queryClient,
  }
  render(
    <SpeedAppProviders
      i18n={i18n}
      queryClient={queryClient}
      theme={{
        presets: themePresets,
        defaultPreset: 'default',
        defaultMode: 'light',
        projectTokens: {},
      }}
    >
      <AppServicesProvider services={services}>
        <RouteProvider table={[]} fallback={() => null}>
          <AccountEntry organizationItems={[ORGANIZATION_ITEM]} />
        </RouteProvider>
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
}

describe('the account menu section rhythm', () => {
  it('spaces every divider, including the one before sign-out', async () => {
    await page.viewport(1280, 900)
    const preset =
      themePresets.find((candidate) => candidate.id === 'default') ??
      themePresets[0]
    if (preset === undefined) {
      throw new Error('the theme shelf is empty')
    }
    const theme = createAppTheme(preset.tokens, undefined, 'light').theme
    const rhythm = Number.parseFloat(theme.spacing(1))
    expect(rhythm).toBeGreaterThan(0)

    renderAccountEntry()

    const trigger = await screen.findByTestId('account-entry-trigger')
    await userEvent.setup().click(trigger)
    const menu = await screen.findByRole('menu')
    const paper = menu.parentElement as HTMLElement
    await waitFor(() => {
      const transform = getComputedStyle(paper).transform
      const settled =
        transform === 'none' ||
        /^matrix\(1, 0, 0, 1, 0, 0\)$/.test(transform)
      if (!settled) {
        throw new Error(`menu still transforming: ${transform}`)
      }
    })

    const dividers = Array.from(menu.children).filter(
      (child): child is HTMLElement => child.tagName === 'HR',
    )
    expect(dividers).toHaveLength(3)
    dividers.forEach((divider, index) => {
      const style = getComputedStyle(divider)
      expect(style.marginTop, `divider ${index + 1} margin-top`).toBe(
        `${rhythm}px`,
      )
      expect(style.marginBottom, `divider ${index + 1} margin-bottom`).toBe(
        `${rhythm}px`,
      )
    })

    const group = menu.querySelector('[role="group"]')
    const lastDivider = dividers[dividers.length - 1]
    if (!(group instanceof HTMLElement) || lastDivider === undefined) {
      throw new Error('the menu has no organization group or last divider')
    }
    const signOut = within(menu).getAllByRole('menuitem').at(-1)
    if (signOut === undefined) {
      throw new Error('the menu has no sign-out item')
    }

    const dividerRect = lastDivider.getBoundingClientRect()
    const groupToDivider =
      dividerRect.top - group.getBoundingClientRect().bottom
    const dividerToSignOut =
      signOut.getBoundingClientRect().top - dividerRect.bottom
    expect(
      Math.abs(groupToDivider - rhythm),
      `organization group to last divider gap: expected ${rhythm}px, measured ${px(groupToDivider)}px`,
    ).toBeLessThanOrEqual(TOLERANCE)
    expect(
      Math.abs(dividerToSignOut - rhythm),
      `last divider to sign-out gap: expected ${rhythm}px, measured ${px(dividerToSignOut)}px`,
    ).toBeLessThanOrEqual(TOLERANCE)
  })
})
