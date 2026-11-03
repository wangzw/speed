/**
 * SettingsPage in a real browser: the settings table at the sm/md/lg
 * floor widths with fixed data (keys and values of representative
 * length, including a long dotted key).
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import axe from 'axe-core'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { SettingsPage } from './SettingsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { uiKitResources } from '@speed/ui-kit'
import type { AdminSetting } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const SETTINGS: readonly AdminSetting[] = [
  {
    key: 'mail.retry',
    namespace: 'mail',
    type: 'int',
    description: 'Retry count',
    domain: '',
    sensitive: false,
    default: '3',
    written: '5',
    has_row: true,
    value: '5',
  },
  {
    key: 'authn.session.absolute-lifetime',
    namespace: 'authn',
    type: 'duration',
    description: 'Absolute session lifetime',
    domain: '',
    sensitive: false,
    default: '24h',
    written: '',
    has_row: false,
    value: '24h',
  },
]

describe('SettingsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the settings table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listSettings: () => Promise.resolve(SETTINGS),
        }),
      )
      renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

      await screen.findByText('mail.retry')
      await expectNoHorizontalOverflow()
    })
  }
})

/**
 * A prose description of the length the settings schema really declares
 * (the longest ones run past 300 characters). Inside the primary
 * identity's 160/200px cap it wraps into a very tall column of thin text;
 * the declaration must get the cell's own width instead.
 */
const LONG_DESCRIPTION =
  'Whether the mail carrier retries a message it refused, measured against the delivery attempts already recorded for that message and against the retry window this deployment configures, so a message is not retried forever and a transient refusal is still retried.'

const LONG_DESCRIPTION_ITEM: AdminSetting = {
  key: 'mail.retry',
  namespace: 'mail',
  type: 'bool',
  description: LONG_DESCRIPTION,
  domain: '',
  sensitive: false,
  default: 'true',
  written: '',
  has_row: false,
  value: 'true',
}

/** The shipped en-US copy-control label for this row's key. */
const COPY_LABEL = (
  uiKitResources['en-US'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue.replace('{{value}}', LONG_DESCRIPTION_ITEM.key)

/** The vertical centre of a rendered element. */
function centreY(element: Element): number {
  const rect = element.getBoundingClientRect()
  return (rect.top + rect.bottom) / 2
}

describe('SettingsPage declaration layout in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`wraps the declaration across the cell in a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listSettings: () => Promise.resolve([LONG_DESCRIPTION_ITEM]),
        }),
      )
      renderWithProviders(<SettingsPage />, {
        operations,
        language: 'en-US',
      })

      const description = await screen.findByText(LONG_DESCRIPTION)
      const row = description.closest('tr')
      expect(row).not.toBeNull()
      await waitFor(() =>
        expect(
          description.getBoundingClientRect().width,
        ).toBeGreaterThan(0),
      )

      // The declaration gets the cell's own width, not the primary
      // identity's 160/200px cap it used to be held to (where this text
      // measured ~130/170px). The key line keeps that cap alone.
      expect(description.getBoundingClientRect().width).toBeGreaterThan(200)

      // Wrapping across the cell keeps the row a handful of prose lines
      // tall instead of stacking the description down a narrow column.
      expect(row?.getBoundingClientRect().height ?? Infinity).toBeLessThan(
        300,
      )

      // The tall key cell does not drop the controls to its middle: the
      // copy control sits on the key line, and the value input starts at
      // the row's top beside the key. Before the row was top-aligned the
      // copy control measured ~83px below the key line here.
      const keyLine = screen.getByText(LONG_DESCRIPTION_ITEM.key)
      const copy = screen.getByRole('button', { name: COPY_LABEL })
      expect(Math.abs(centreY(copy) - centreY(keyLine))).toBeLessThan(12)

      const input = screen.getByRole('textbox', {
        name: LONG_DESCRIPTION_ITEM.key,
      })
      expect(
        Math.abs(
          input.getBoundingClientRect().top -
            keyLine.getBoundingClientRect().top,
        ),
      ).toBeLessThan(24)
    })
  }
})

/** The shipped Remove label for the overridden row the case renders. */
const REMOVE_KEY = 'mail.retry'
const REMOVE_LABEL = enUS.settings.removeAria.replace('{{key}}', REMOVE_KEY)

/**
 * The row's Remove control carries the visible text "Remove" beside its
 * accessible name; WCAG 2.5.3 requires that text be part of the name. The
 * rule measures the visible text through a canvas, which jsdom cannot
 * provide, so this tier settles it for the one row shape that offers the
 * control.
 */
/**
 * The theme's container surface painted onto a reference element, so the
 * cases compare against the role the page is supposed to use rather than
 * against values written into the test.
 */
function ContainerSurfaceProbe() {
  const theme = useTheme()
  const role = theme.surface.container
  return (
    <div
      data-testid="container-surface-probe"
      style={{
        backgroundColor: role.fill,
        borderColor: role.borderColor,
        borderStyle: 'solid',
        borderWidth: role.borderWidth,
        borderRadius: theme.shape.borderRadius,
      }}
    />
  )
}

/**
 * The settings page's two blocks -- the namespace navigation and the
 * settings register -- are content sections: each renders through
 * `SectionCard` as a `<section>` painted with the theme's container role.
 * The register is a flush table section, so the table reaches the
 * section's rim. A hand-written outlined `Card` paints the same colors,
 * so only the section element and the flush geometry distinguish the two.
 */
describe('SettingsPage content surfaces in Chromium', () => {
  it('renders both blocks on the container surface, the register flush', async () => {
    await page.viewport(1200, 900)
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(SETTINGS) }),
    )
    renderWithProviders(
      <>
        <ContainerSurfaceProbe />
        <SettingsPage />
      </>,
      { operations, language: 'en-US' },
    )

    const key = await screen.findByText(SETTINGS[0].key)
    const table = key.closest('table')
    expect(table).not.toBeNull()
    const register = table?.closest('section')
    expect(register, 'the settings register is a SectionCard section').not.toBeNull()

    const navigation = screen.getByRole('navigation', {
      name: enUS.settings.nav.label,
    })
    const navigationSection = navigation.querySelector('section')
    expect(
      navigationSection,
      'the namespace navigation is a SectionCard section',
    ).not.toBeNull()

    const expected = getComputedStyle(
      screen.getByTestId('container-surface-probe'),
    )
    for (const block of [navigationSection, register] as Element[]) {
      const paint = getComputedStyle(block)
      expect(paint.backgroundColor).toBe(expected.backgroundColor)
      expect(paint.borderTopWidth).toBe(expected.borderTopWidth)
      expect(paint.borderTopStyle).toBe(expected.borderTopStyle)
      expect(paint.borderTopColor).toBe(expected.borderTopColor)
      expect(paint.borderTopLeftRadius).toBe(expected.borderTopLeftRadius)
    }

    // Flush content: outside the 1px rim, the table touches every edge of
    // the section, so the surface clips its corners rather than the table
    // floating on a padded card.
    const tableRect = table!.getBoundingClientRect()
    const registerRect = register!.getBoundingClientRect()
    expect(Math.abs(tableRect.left - registerRect.left)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.right - registerRect.right)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.top - registerRect.top)).toBeLessThanOrEqual(1.5)
  })
})

describe('SettingsPage row Remove Label in Name in Chromium', () => {
  it('keeps the visible Remove text inside the accessible name', async () => {
    await page.viewport(1200, 900)
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(SETTINGS) }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const remove = await screen.findByRole('button', { name: REMOVE_LABEL })
    const result = await axe.run(remove, {
      runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
    })
    expect(result.incomplete).toEqual([])
    expect(result.violations).toEqual([])
  })
})
